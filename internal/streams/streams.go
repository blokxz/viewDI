// Package streams runs the detail view's live data (docs/02-architecture.md
// §4.3): opening an instance subscribes to its log, its stats and its
// inspect document; closing it cancels the subscription and every goroutine
// it started exits.
//
// Each stream reconnects with backoff when the daemon fails and resumes
// where it stopped (logs from the last timestamp). Log lines are sanitised
// here, off the UI goroutine, and buffered with a bound: when the UI falls
// behind, the oldest pending lines are dropped and counted. Deliveries are
// coalesced to at most one batch per MinInterval.
package streams

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/textsafe"
)

// Defaults.
const (
	DefaultTail     = 500                    // log lines of history
	DefaultInterval = time.Second            // stats interval
	MinInterval     = 100 * time.Millisecond // between two batches (≤ 10 Hz)
	MaxPending      = 5000                   // log lines held for the UI
	maxSamples      = 300
	backoffMin      = time.Second
	backoffMax      = 30 * time.Second
	idleRecheck     = 30 * time.Second // reconnect check after a clean end
)

// Request says which streams a subscription runs.
type Request struct {
	Logs     bool
	Tail     int // history lines; 0 = DefaultTail
	Stats    bool
	Interval time.Duration // stats interval; 0 = DefaultInterval
	Inspect  bool          // Inspect + InspectRaw, at start and on Refresh
}

// State is the state of one stream.
type State int

// Stream states.
const (
	Connecting State = iota
	Live
	Ended    // the source ended cleanly (instance stopped); resumes on Refresh
	Retrying // the daemon failed; reconnecting with backoff
	Removed  // the instance does not exist
	Failed   // permission denied or another error that retrying cannot fix
)

func (s State) String() string {
	return [...]string{"connecting", "live", "ended", "retrying", "removed", "failed"}[s]
}

// Status describes one stream.
type Status struct {
	State State
	Err   string    // last error, for Retrying and Failed
	Retry time.Time // next attempt, for Retrying
}

// Inspected is an inspect result.
type Inspected struct {
	Instance engine.Instance
	Raw      []byte
	Err      error
}

// Batch is what a subscription delivered since the previous batch.
type Batch struct {
	Lines   []engine.LogLine // sanitised, in order
	Dropped int              // lines lost because the UI fell behind
	Samples []engine.Sample
	Inspect *Inspected // nil when unchanged
	Logs    *Status    // nil when unchanged
	Stats   *Status    // nil when unchanged
}

func (b *Batch) empty() bool {
	return len(b.Lines) == 0 && b.Dropped == 0 && len(b.Samples) == 0 && b.Inspect == nil && b.Logs == nil && b.Stats == nil
}

// Options tunes a registry.
type Options struct {
	Clock  clock.Clock
	Logger *slog.Logger
	// MaxPending overrides MaxPending (tests).
	MaxPending int
}

// Registry starts subscriptions on the engines it was given.
type Registry struct {
	ctx     context.Context
	engines map[engine.ID]engine.Engine
	clk     clock.Clock
	log     *slog.Logger
	max     int
	wg      sync.WaitGroup
	nextID  atomic.Uint64
	active  atomic.Int64
}

// New returns a registry whose subscriptions end at the latest when ctx does.
func New(ctx context.Context, opts Options, engines ...engine.Engine) *Registry {
	r := &Registry{ctx: ctx, engines: map[engine.ID]engine.Engine{}, clk: opts.Clock, log: opts.Logger, max: opts.MaxPending}
	if r.clk == nil {
		r.clk = clock.Real{}
	}
	if r.log == nil {
		r.log = slog.New(slog.DiscardHandler)
	}
	if r.max <= 0 {
		r.max = MaxPending
	}
	for _, e := range engines {
		r.engines[e.ID()] = e
	}
	return r
}

// Wait blocks until every subscription goroutine exited.
func (r *Registry) Wait() { r.wg.Wait() }

// Active returns the number of subscriptions not yet fully stopped.
func (r *Registry) Active() int { return int(r.active.Load()) }

// Subscription is one instance's live data. It is safe for concurrent use.
type Subscription struct {
	ID  uint64
	Key engine.Key

	r      *Registry
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	pending Batch
	notify  chan struct{} // a batch is pending
	last    time.Time     // previous delivery
	wakes   []chan struct{}
}

// Subscribe starts the requested streams for the instance key. It never
// blocks: failures arrive as stream statuses.
func (r *Registry) Subscribe(key engine.Key, req Request) *Subscription {
	ctx, cancel := context.WithCancel(r.ctx)
	s := &Subscription{ID: r.nextID.Add(1), Key: key, r: r, ctx: ctx, cancel: cancel, notify: make(chan struct{}, 1)}
	if req.Tail == 0 {
		req.Tail = DefaultTail
	}
	if req.Interval <= 0 {
		req.Interval = DefaultInterval
	}
	eid, id, _ := strings.Cut(string(key), "/")
	eng, ok := r.engines[engine.ID(eid)]
	var jobs []func(context.Context, engine.Engine, string, <-chan struct{})
	if req.Logs {
		jobs = append(jobs, func(ctx context.Context, e engine.Engine, id string, wake <-chan struct{}) {
			s.runLogs(ctx, e, id, req.Tail, wake)
		})
	}
	if req.Stats {
		jobs = append(jobs, func(ctx context.Context, e engine.Engine, id string, wake <-chan struct{}) {
			s.runStats(ctx, e, id, req.Interval, wake)
		})
	}
	if req.Inspect {
		jobs = append(jobs, s.runInspect)
	}
	if !ok {
		st := Status{State: Failed, Err: "unknown engine " + eid}
		s.update(func(b *Batch) { b.Logs, b.Stats = &st, &st })
		cancel()
		return s
	}
	var group sync.WaitGroup
	r.active.Add(1)
	r.wg.Add(1)
	for _, job := range jobs {
		wake := make(chan struct{}, 1)
		s.wakes = append(s.wakes, wake)
		group.Add(1)
		go func() {
			defer group.Done()
			defer func() {
				if p := recover(); p != nil {
					r.log.Error("stream panic", "key", key, "panic", p, "stack", string(debug.Stack()))
					st := Status{State: Failed, Err: fmt.Sprint("internal error: ", p)}
					s.update(func(b *Batch) { b.Logs, b.Stats = &st, &st })
				}
			}()
			job(ctx, eng, id, wake)
		}()
	}
	go func() {
		defer r.wg.Done()
		defer r.active.Add(-1)
		group.Wait()
	}()
	return s
}

// Close cancels the subscription; its goroutines exit promptly. Close never
// blocks and may be called more than once.
func (s *Subscription) Close() { s.cancel() }

// Refresh re-reads the inspect document and reconnects ended or retrying
// streams now: the app calls it when the instance changed.
func (s *Subscription) Refresh() {
	for _, w := range s.wakes {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

// Next waits for the next batch. It reports false once the subscription or
// ctx ended. Batches are at least MinInterval apart.
func (s *Subscription) Next(ctx context.Context) (Batch, bool) {
	for {
		select {
		case <-ctx.Done():
			return Batch{}, false
		case <-s.ctx.Done():
			return Batch{}, false
		case <-s.notify:
		}
		s.mu.Lock()
		wait := s.last.Add(MinInterval).Sub(s.r.clk.Now())
		s.mu.Unlock()
		if wait > 0 {
			select {
			case <-ctx.Done():
				return Batch{}, false
			case <-s.ctx.Done():
				return Batch{}, false
			case <-s.r.clk.After(wait):
			}
		}
		s.mu.Lock()
		b := s.pending
		s.pending = Batch{}
		s.last = s.r.clk.Now()
		s.mu.Unlock()
		if !b.empty() {
			return b, true
		}
	}
}

func (s *Subscription) update(f func(*Batch)) {
	s.mu.Lock()
	f(&s.pending)
	if n := len(s.pending.Lines) - s.r.max; n > 0 {
		s.pending.Dropped += n
		// Copy instead of reslicing so the dropped lines can be collected.
		s.pending.Lines = append([]engine.LogLine(nil), s.pending.Lines[n:]...)
	}
	if n := len(s.pending.Samples) - maxSamples; n > 0 {
		s.pending.Samples = s.pending.Samples[n:]
	}
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *Subscription) setLogs(st Status)  { s.update(func(b *Batch) { b.Logs = &st }) }
func (s *Subscription) setStats(st Status) { s.update(func(b *Batch) { b.Stats = &st }) }

// backoff waits before the next attempt, or until a wake or ctx. It returns
// the next delay and false if ctx ended.
func (s *Subscription) backoff(ctx context.Context, d time.Duration, wake <-chan struct{}) (time.Duration, bool) {
	select {
	case <-ctx.Done():
		return d, false
	case <-wake:
		return backoffMin, true
	case <-s.r.clk.After(d):
		return min(d*2, backoffMax), true
	}
}

// failure maps a stream error to a status: whether to stop for good.
func failure(err error, retry time.Time) (Status, bool) {
	switch engine.Classify(err) {
	case engine.ClassNotFound:
		return Status{State: Removed, Err: err.Error()}, true
	case engine.ClassPermission:
		return Status{State: Failed, Err: err.Error()}, true
	}
	return Status{State: Retrying, Err: err.Error(), Retry: retry}, false
}

func (s *Subscription) runLogs(ctx context.Context, e engine.Engine, id string, tail int, wake <-chan struct{}) {
	var since time.Time
	var lastText string
	delay := backoffMin
	first := true
	for {
		opts := engine.LogOptions{Tail: tail, Follow: true}
		if !first {
			opts.Tail = 0
			if !since.IsZero() {
				opts.Tail, opts.Since = -1, since // everything since the last line seen
			}
		}
		stream, err := e.Logs(ctx, id, opts)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			st, stop := failure(err, s.r.clk.Now().Add(delay))
			s.setLogs(st)
			if stop {
				return
			}
			var ok bool
			if delay, ok = s.backoff(ctx, delay, wake); !ok {
				return
			}
			continue
		}
		first = false
		s.setLogs(Status{State: Live})
		live := false
		for l := range stream.C() {
			if !since.IsZero() && (l.Time.Before(since) || (l.Time.Equal(since) && l.Text == lastText)) {
				continue // already delivered before the reconnect
			}
			if !l.Time.IsZero() {
				since, lastText = l.Time, l.Text
			}
			l.Text = textsafe.Clean(l.Text)
			s.update(func(b *Batch) { b.Lines = append(b.Lines, l) })
			live = true
		}
		if ctx.Err() != nil {
			return
		}
		if live {
			delay = backoffMin
		}
		if err := stream.Err(); err != nil {
			st, stop := failure(err, s.r.clk.Now().Add(delay))
			s.setLogs(st)
			if stop {
				return
			}
			var ok bool
			if delay, ok = s.backoff(ctx, delay, wake); !ok {
				return
			}
			continue
		}
		s.setLogs(Status{State: Ended})
		if _, ok := s.backoff(ctx, idleRecheck, wake); !ok {
			return
		}
	}
}

func (s *Subscription) runStats(ctx context.Context, e engine.Engine, id string, every time.Duration, wake <-chan struct{}) {
	delay := backoffMin
	for {
		stream, err := e.WatchStats(ctx, id, every)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			st, stop := failure(err, s.r.clk.Now().Add(delay))
			s.setStats(st)
			if stop {
				return
			}
			var ok bool
			if delay, ok = s.backoff(ctx, delay, wake); !ok {
				return
			}
			continue
		}
		s.setStats(Status{State: Live})
		for smp := range stream.C() {
			s.update(func(b *Batch) { b.Samples = append(b.Samples, smp) })
			delay = backoffMin
		}
		if ctx.Err() != nil {
			return
		}
		if err := stream.Err(); err != nil {
			st, stop := failure(err, s.r.clk.Now().Add(delay))
			s.setStats(st)
			if stop {
				return
			}
			var ok bool
			if delay, ok = s.backoff(ctx, delay, wake); !ok {
				return
			}
			continue
		}
		s.setStats(Status{State: Ended})
		if _, ok := s.backoff(ctx, idleRecheck, wake); !ok {
			return
		}
	}
}

func (s *Subscription) runInspect(ctx context.Context, e engine.Engine, id string, wake <-chan struct{}) {
	for {
		in, err := e.Inspect(ctx, id)
		var raw []byte
		if err == nil {
			raw, err = e.InspectRaw(ctx, id)
		}
		if ctx.Err() != nil {
			return
		}
		res := &Inspected{Instance: in, Raw: raw, Err: err}
		s.update(func(b *Batch) { b.Inspect = res })
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}
