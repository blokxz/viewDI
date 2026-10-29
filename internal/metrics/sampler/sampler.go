// Package sampler schedules metric collection (docs/05-quality-plan.md
// §4.2): one pass per interval over every running instance of every
// connected engine, reading cgroup v2 files where possible and falling
// back to the engine's Stats call (one batch per engine) otherwise. The
// interval adapts to what the UI shows; results are lossy (latest wins).
package sampler

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
	"github.com/blokxz/viewDI/internal/metrics/cgroup"
	"github.com/blokxz/viewDI/internal/store"
)

// Source is one engine to sample; supervisor.Supervisor implements it.
type Source interface {
	Engine() engine.Engine
	Running() []engine.Instance
	Connected() bool
}

// Options tunes the sampler. Zero fields take the defaults.
type Options struct {
	Clock    clock.Clock
	Interval time.Duration // initial interval (1 s)
	Timeout  time.Duration // per Stats call (5 s)
	// Cgroup reads cgroup v2 files; nil samples through the engine APIs
	// only (--mock, or hosts without a readable unified hierarchy).
	Cgroup *cgroup.Reader
	// RetryCgroup is how long an instance whose cgroup could not be read
	// stays on the API path before the cgroup is tried again (60 s).
	RetryCgroup time.Duration
	Logger      *slog.Logger
}

// Sampler collects metrics until its context ends.
type Sampler struct {
	opts     Options
	clk      clock.Clock
	log      *slog.Logger
	sources  []Source
	out      chan store.MetricsSampled
	interval atomic.Int64
	wake     chan struct{}

	// Run-goroutine state.
	prev    map[engine.Key]prevSample
	paths   map[engine.Key]pathEntry
	failing map[engine.ID]int
	lastErr map[engine.ID]string
}

type prevSample struct {
	s      engine.Sample
	cgroup bool // counters from a different source are not comparable
}

type pathEntry struct {
	dir      string
	pid      int
	badUntil time.Time
}

// New returns a sampler over sources.
func New(opts Options, sources ...Source) *Sampler {
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = engine.DefaultTimeout
	}
	if opts.RetryCgroup <= 0 {
		opts.RetryCgroup = time.Minute
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	s := &Sampler{
		opts: opts, clk: opts.Clock, log: opts.Logger, sources: sources,
		out: make(chan store.MetricsSampled, 1), wake: make(chan struct{}, 1),
		prev: map[engine.Key]prevSample{}, paths: map[engine.Key]pathEntry{},
		failing: map[engine.ID]int{}, lastErr: map[engine.ID]string{},
	}
	s.interval.Store(int64(opts.Interval))
	return s
}

// Out delivers the passes. It holds at most one: a slow reader only ever
// sees the newest.
func (s *Sampler) Out() <-chan store.MetricsSampled { return s.out }

// Interval returns the current sampling interval.
func (s *Sampler) Interval() time.Duration { return time.Duration(s.interval.Load()) }

// SetInterval changes the interval and starts the next pass at once. Safe
// from any goroutine; it never blocks.
func (s *Sampler) SetInterval(d time.Duration) {
	if d <= 0 || time.Duration(s.interval.Swap(int64(d))) == d {
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run samples until ctx ends.
func (s *Sampler) Run(ctx context.Context) {
	for {
		s.publish(s.Pass(ctx))
		select {
		case <-ctx.Done():
			return
		case <-s.clk.After(s.Interval()):
		case <-s.wake:
		}
	}
}

// publish sends m, replacing an unread older pass.
func (s *Sampler) publish(m store.MetricsSampled) {
	for {
		select {
		case s.out <- m:
			return
		default:
		}
		select {
		case <-s.out:
		default:
		}
	}
}

// Pass samples every source once.
func (s *Sampler) Pass(ctx context.Context) store.MetricsSampled {
	now := s.clk.Now()
	m := store.MetricsSampled{At: now, Points: map[engine.Key]metrics.Point{}, Health: map[engine.ID]store.SamplingHealth{}}
	seen := map[engine.Key]bool{}
	for _, src := range s.sources {
		eng := src.Engine()
		id := eng.ID()
		if !src.Connected() {
			m.Health[id] = store.SamplingHealth{Failing: s.failing[id], Err: s.lastErr[id], Interval: s.Interval()}
			continue
		}
		var api []engine.Instance
		byID := map[string]engine.Instance{}
		cg, viaAPI := 0, 0
		for _, in := range src.Running() {
			seen[in.Key()] = true
			if sample, ok := s.readCgroup(in, now); ok {
				s.record(m.Points, in.Key(), sample, true)
				cg++
				continue
			}
			api = append(api, in)
			byID[in.ID] = in
		}
		if len(api) > 0 {
			ids := make([]string, len(api))
			for i, in := range api {
				ids[i] = in.ID
			}
			cctx, cancel := clock.WithTimeout(ctx, s.clk, s.opts.Timeout)
			samples, err := eng.Stats(cctx, ids)
			cancel()
			if err != nil {
				s.failing[id]++
				s.lastErr[id] = err.Error()
				s.log.Debug("stats failed", "engine", string(id), "err", err)
			} else {
				s.failing[id], s.lastErr[id] = 0, ""
				for _, sample := range samples {
					if in, ok := byID[sample.InstanceID]; ok {
						s.record(m.Points, in.Key(), sample, false)
						viaAPI++
					}
				}
			}
		}
		h := store.SamplingHealth{Instances: cg + viaAPI, Failing: s.failing[id], Err: s.lastErr[id], Interval: s.Interval()}
		switch {
		case cg > 0 && viaAPI > 0:
			h.Source = store.SourceMixed
		case cg > 0:
			h.Source = store.SourceCgroup
		default:
			h.Source = store.SourceAPI
		}
		m.Health[id] = h
	}
	for k := range s.prev {
		if !seen[k] {
			delete(s.prev, k)
			delete(s.paths, k)
		}
	}
	return m
}

// record turns a raw sample into a point against the previous one.
func (s *Sampler) record(points map[engine.Key]metrics.Point, k engine.Key, sample engine.Sample, fromCgroup bool) {
	if p, ok := s.prev[k]; ok && p.cgroup == fromCgroup {
		if pt, ok := metrics.Rate(p.s, sample); ok {
			points[k] = pt
		}
	}
	s.prev[k] = prevSample{s: sample, cgroup: fromCgroup}
}

// readCgroup reads an instance through its cgroup when it can.
func (s *Sampler) readCgroup(in engine.Instance, now time.Time) (engine.Sample, bool) {
	if s.opts.Cgroup == nil || in.Kind == engine.KindVM {
		return engine.Sample{}, false
	}
	k := in.Key()
	e, ok := s.paths[k]
	if ok && now.Before(e.badUntil) {
		return engine.Sample{}, false
	}
	if !ok || e.pid != in.PID || e.dir == "" {
		dir, err := s.opts.Cgroup.Resolve(in)
		if err != nil {
			s.paths[k] = pathEntry{pid: in.PID, badUntil: now.Add(s.opts.RetryCgroup)}
			return engine.Sample{}, false
		}
		e = pathEntry{dir: dir, pid: in.PID}
		s.paths[k] = e
	}
	sample, err := s.opts.Cgroup.Read(e.dir, in.PID, now)
	if err != nil {
		s.paths[k] = pathEntry{pid: in.PID, badUntil: now.Add(s.opts.RetryCgroup)}
		return engine.Sample{}, false
	}
	sample.InstanceID = in.ID
	return sample, true
}
