// Package supervisor keeps one engine connected: it runs the connection
// state machine (Connecting → Connected → Degraded/Unavailable, see
// docs/02-architecture.md §4.2), subscribes to events before every full
// list so nothing is missed, coalesces event bursts into one inspect per
// instance, watches the daemon with a ping, resyncs periodically, and
// reports everything as store.Changes on a bounded channel.
//
// Supervisors own their goroutines; they stop when the context passed to
// Run ends.
package supervisor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"strings"
	"time"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
)

// Options tunes a supervisor. Zero fields take the defaults.
type Options struct {
	Clock          clock.Clock
	Timeout        time.Duration // per daemon call (5 s)
	PingInterval   time.Duration // watchdog period (2 s)
	ResyncInterval time.Duration // periodic full list (30 s)
	BackoffMin     time.Duration // first reconnect delay (1 s)
	BackoffMax     time.Duration // reconnect delay cap (30 s)
	Reprobe        time.Duration // retry period when unavailable (60 s)
	Coalesce       time.Duration // event batching window (50 ms)
	// Seed seeds the backoff jitter; 0 picks a random seed.
	Seed   uint64
	Logger *slog.Logger
}

func (o Options) withDefaults() Options {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&o.Timeout, engine.DefaultTimeout)
	def(&o.PingInterval, 2*time.Second)
	def(&o.ResyncInterval, 30*time.Second)
	def(&o.BackoffMin, time.Second)
	def(&o.BackoffMax, 30*time.Second)
	def(&o.Reprobe, 60*time.Second)
	def(&o.Coalesce, 50*time.Millisecond)
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return o
}

// Supervisor keeps one engine connected.
type Supervisor struct {
	eng      engine.Engine
	endpoint string // socket shown in hints
	opts     Options
	clk      clock.Clock
	log      *slog.Logger
	rng      *rand.Rand
	out      chan<- store.Change

	reconnect chan struct{}
	project   chan string

	// Run-goroutine state.
	scope         string // Incus project ("" = all)
	everConnected bool
	attempt       int
}

// New returns a supervisor for eng that reports on out. endpoint is the
// daemon socket or URI, used in hints.
func New(eng engine.Engine, endpoint string, out chan<- store.Change, opts Options) *Supervisor {
	opts = opts.withDefaults()
	seed := opts.Seed
	if seed == 0 {
		seed = rand.Uint64()
	}
	return &Supervisor{
		eng: eng, endpoint: strings.TrimPrefix(endpoint, "unix://"), opts: opts, clk: opts.Clock,
		log: opts.Logger.With("engine", string(eng.ID())), rng: rand.New(rand.NewPCG(seed, 0x5eed)),
		out: out, reconnect: make(chan struct{}, 1), project: make(chan string, 1),
	}
}

// ID returns the supervised engine's ID.
func (s *Supervisor) ID() engine.ID { return s.eng.ID() }

// Reconnect asks for an immediate reconnect (or a resync when connected).
// It never blocks.
func (s *Supervisor) Reconnect() {
	select {
	case s.reconnect <- struct{}{}:
	default:
	}
}

// SetProject scopes Incus listings to project ("" = all). Latest wins; it
// never blocks.
func (s *Supervisor) SetProject(p string) {
	for {
		select {
		case s.project <- p:
			return
		default:
		}
		select {
		case <-s.project: // drop the stale request
		default:
		}
	}
}

// SetInitialProject sets the scope before Run starts.
func (s *Supervisor) SetInitialProject(p string) { s.scope = p }

// Run supervises until ctx ends.
func (s *Supervisor) Run(ctx context.Context) {
	for ctx.Err() == nil {
		s.status(ctx, store.Health{State: store.StateConnecting})
		err := s.session(ctx)
		if ctx.Err() != nil {
			return
		}
		class := engine.Classify(err)
		s.log.Warn("session ended", "err", err, "class", class.String())
		if class == engine.ClassPermission || (class == engine.ClassMissing && !s.everConnected) {
			retry := s.clk.Now().Add(s.opts.Reprobe)
			s.status(ctx, store.Health{
				State: store.StateUnavailable, RetryAt: retry, Err: errText(err), Class: class,
				Hint: Hint(s.eng.ID(), class, s.endpoint),
			})
			s.attempt = 0
			s.wait(ctx, s.opts.Reprobe)
			continue
		}
		delay := s.backoff()
		s.status(ctx, store.Health{
			State: store.StateDegraded, RetryAt: s.clk.Now().Add(delay), Err: errText(err), Class: class,
		})
		s.wait(ctx, delay)
	}
}

// backoff returns the next reconnect delay: BackoffMin doubling up to
// BackoffMax, with ±20 % jitter.
func (s *Supervisor) backoff() time.Duration {
	d := s.opts.BackoffMin << min(s.attempt, 16)
	if d > s.opts.BackoffMax || d <= 0 {
		d = s.opts.BackoffMax
	}
	s.attempt++
	jitter := 0.8 + 0.4*s.rng.Float64()
	return time.Duration(float64(d) * jitter)
}

// wait sleeps for d, cut short by a reconnect request or a project change.
func (s *Supervisor) wait(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-s.clk.After(d):
	case <-s.reconnect:
		s.attempt = 0
	case p := <-s.project:
		s.scope = p
	}
}

// session connects, syncs and follows the engine until something fails.
// It returns the error that ended the session.
func (s *Supervisor) session(ctx context.Context) (err error) {
	defer func() { // F12: a provider panic ends the session, not the program
		if r := recover(); r != nil {
			s.log.Error("provider panic", "panic", r, "stack", string(debug.Stack()))
			err = engine.Errorf(engine.ClassTransient, "panic", fmt.Errorf("%v", r))
		}
	}()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := s.call(sctx, s.eng.Ping); err != nil {
		return err
	}
	feed, err := s.eng.Events(sctx) // subscribe before listing: no gap
	if err != nil {
		return err
	}
	if err := s.sync(sctx); err != nil {
		return err
	}
	s.everConnected, s.attempt = true, 0
	s.status(ctx, store.Health{State: store.StateConnected})

	pingC := s.clk.After(s.opts.PingInterval)
	resyncC := s.clk.After(s.opts.ResyncInterval)
	var flushC <-chan time.Time
	pending := map[engine.Key]engine.Event{}
	var order []engine.Key

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-s.reconnect:
			if err := s.sync(sctx); err != nil {
				return err
			}
			resyncC = s.clk.After(s.opts.ResyncInterval)

		case p := <-s.project:
			s.scope = p
			if err := s.sync(sctx); err != nil {
				return err
			}

		case ev, ok := <-feed.C():
			if !ok {
				if err := feed.Err(); err != nil {
					return err
				}
				return engine.Errorf(engine.ClassTransient, "events", io.EOF)
			}
			if _, seen := pending[ev.Key()]; !seen {
				order = append(order, ev.Key())
			}
			pending[ev.Key()] = ev // latest wins within the window
			if flushC == nil {
				flushC = s.clk.After(s.opts.Coalesce)
			}

		case <-flushC:
			flushC = nil
			for _, k := range order {
				if err := s.apply(sctx, pending[k]); err != nil {
					return err
				}
			}
			clear(pending)
			order = order[:0]

		case <-pingC:
			if err := s.call(sctx, s.eng.Ping); err != nil {
				return err
			}
			pingC = s.clk.After(s.opts.PingInterval)

		case <-resyncC: // F3: corrects drift from a silently stalled stream
			if err := s.sync(sctx); err != nil {
				return err
			}
			resyncC = s.clk.After(s.opts.ResyncInterval)
		}
	}
}

// apply turns one coalesced event into a store change.
func (s *Supervisor) apply(ctx context.Context, ev engine.Event) error {
	if ev.Kind == engine.EventRemoved {
		s.emit(ctx, store.InstanceRemoved{Engine: ev.Engine, ID: ev.InstanceID})
		return nil
	}
	cctx, cancel := clock.WithTimeout(ctx, s.clk, s.opts.Timeout)
	defer cancel()
	in, err := s.eng.Inspect(cctx, ev.InstanceID)
	if err != nil && engine.Classify(err) == engine.ClassNotFound { // removed before we looked (F10)
		s.emit(ctx, store.InstanceRemoved{Engine: ev.Engine, ID: ev.InstanceID})
		return nil
	}
	if err != nil {
		return err
	}
	if s.scope != "" && in.Project != "" && in.Project != s.scope {
		return nil // outside the selected project
	}
	s.emit(ctx, store.InstanceUpserted{Instance: in})
	return nil
}

// sync lists everything and emits a Synced change.
func (s *Supervisor) sync(ctx context.Context) error {
	cctx, cancel := clock.WithTimeout(ctx, s.clk, s.opts.Timeout)
	defer cancel()
	opts := engine.ListOptions{Project: s.scope}
	info, err := s.eng.Info(cctx)
	if err != nil {
		return err
	}
	list, err := s.eng.ListInstances(cctx, opts)
	if err != nil {
		return err
	}
	nets, err := s.eng.Networks(cctx, opts)
	if err != nil {
		return err
	}
	vols, err := s.eng.Volumes(cctx, opts)
	if err != nil {
		return err
	}
	s.emit(ctx, store.Synced{
		Engine: s.eng.ID(), At: s.clk.Now(), Project: s.scope, Info: info,
		Instances: list, Networks: nets, Volumes: vols,
	})
	return nil
}

// call runs one daemon call with the per-call timeout.
func (s *Supervisor) call(ctx context.Context, f func(context.Context) error) error {
	cctx, cancel := clock.WithTimeout(ctx, s.clk, s.opts.Timeout)
	defer cancel()
	return f(cctx)
}

func (s *Supervisor) status(ctx context.Context, h store.Health) {
	h.Since = s.clk.Now()
	h.Socket = s.endpoint
	s.emit(ctx, store.StatusChanged{Engine: s.eng.ID(), Health: h})
}

// emit delivers a change; changes are never dropped, so this blocks while
// the channel is full (the UI drains it continuously).
func (s *Supervisor) emit(ctx context.Context, c store.Change) {
	select {
	case s.out <- c:
	case <-ctx.Done():
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	if engine.IsTimeout(err) {
		return "daemon not responding (timeout)"
	}
	if engine.IsEOF(err) {
		return "connection lost"
	}
	return err.Error()
}
