package store

import (
	"time"

	"github.com/blokxz/viewDI/internal/alerts"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
)

// History is the number of points kept per instance and per engine (5 min
// at the default 1 s sampling interval).
const History = 300

// maxEvents is the length of the recent-events feed.
const maxEvents = 100

// Sampling source names.
const (
	SourceCgroup = "cgroup"
	SourceAPI    = "api"
	SourceMixed  = "cgroup+api"
)

// SamplingHealth describes how an engine's metrics are being read.
type SamplingHealth struct {
	Source    string        // SourceCgroup, SourceAPI, SourceMixed; "" = not sampled
	Instances int           // instances sampled in the last pass
	Failing   int           // consecutive failed passes
	Err       string        // last error
	Interval  time.Duration // current sampling interval
}

// MetricsSampled is one sampler pass: the latest rates of every running
// instance that could be read. Metrics are lossy (latest wins): a dropped
// pass only leaves a gap in the history.
type MetricsSampled struct {
	At     time.Time
	Points map[engine.Key]metrics.Point
	Health map[engine.ID]SamplingHealth
}

// EventRecorded adds an instance event to the recent-events feed and to
// the restart-loop rule.
type EventRecorded struct{ Event engine.Event }

func (MetricsSampled) isChange() {}
func (EventRecorded) isChange()  {}

// metricsState is the store's metric history. Snapshots read it live:
// everything runs on the UI goroutine.
type metricsState struct {
	latest   map[engine.Key]metrics.Point // replaced wholesale on every pass
	totals   map[engine.ID]metrics.Point
	series   map[engine.Key]*metrics.Ring[metrics.Point]
	engines  map[engine.ID]*metrics.Ring[metrics.Point]
	sampling map[engine.ID]SamplingHealth
	events   *metrics.Ring[engine.Event]
}

func newMetricsState() *metricsState {
	return &metricsState{
		latest: map[engine.Key]metrics.Point{}, totals: map[engine.ID]metrics.Point{},
		series: map[engine.Key]*metrics.Ring[metrics.Point]{}, engines: map[engine.ID]*metrics.Ring[metrics.Point]{},
		sampling: map[engine.ID]SamplingHealth{}, events: metrics.NewRing[engine.Event](maxEvents),
	}
}

func (s *Store) applySample(c MetricsSampled) {
	m := s.metrics
	m.latest = c.Points
	m.sampling = c.Health
	byEngine := map[engine.ID][]metrics.Point{}
	for k, p := range c.Points {
		r, ok := m.series[k]
		if !ok {
			r = metrics.NewRing[metrics.Point](History)
			m.series[k] = r
		}
		r.Push(p)
		byEngine[k.Engine()] = append(byEngine[k.Engine()], p)
	}
	totals := map[engine.ID]metrics.Point{}
	for id, h := range c.Health {
		if h.Source == "" {
			// Not sampled this pass (degraded): keep the last total, which
			// the dashboards show dimmed as stale data.
			if old, ok := m.totals[id]; ok {
				totals[id] = old
			}
			continue
		}
		if len(byEngine[id]) == 0 && h.Instances > 0 {
			continue // first pass: counters read, no rates yet
		}
		total := metrics.Sum(c.At, byEngine[id])
		totals[id] = total
		r, ok := m.engines[id]
		if !ok {
			r = metrics.NewRing[metrics.Point](History)
			m.engines[id] = r
		}
		r.Push(total)
	}
	m.totals = totals
}

// pruneSeries drops the history of instances that no longer exist.
func (s *Store) pruneSeries() {
	for k := range s.metrics.series {
		e, ok := s.engines[k.Engine()]
		if !ok {
			delete(s.metrics.series, k)
			continue
		}
		if _, ok := e.instances[k]; !ok {
			delete(s.metrics.series, k)
		}
	}
}

// ConfigureAlerts replaces the alert thresholds.
func (s *Store) ConfigureAlerts(cfg alerts.Config) { s.alerts = alerts.New(cfg) }

// Evaluate runs the alert rules at now. It reports whether the active
// alerts changed (and invalidates the snapshot if so).
func (s *Store) Evaluate(now time.Time) bool {
	in := alerts.Input{Points: s.metrics.latest}
	for _, id := range s.ids {
		e := s.engines[id]
		h := e.health
		in.Engines = append(in.Engines, alerts.EngineHealth{
			ID: id, Connected: h.State == StateConnected,
			Degraded: h.State == StateDegraded, Unavailable: h.State == StateUnavailable,
			Since: h.Since, Err: h.Err,
		})
		for _, inst := range e.instances {
			in.Instances = append(in.Instances, inst)
		}
	}
	expired := s.expirePending(now)
	if !s.alerts.Evaluate(now, in) && !expired {
		return false
	}
	s.version++
	s.snap = nil
	return true
}

// Point returns the latest rates of an instance.
func (s *Snapshot) Point(k engine.Key) (metrics.Point, bool) {
	if s == nil || s.metrics == nil {
		return metrics.Point{}, false
	}
	p, ok := s.latest[k]
	return p, ok
}

// EnginePoint returns the latest aggregate of an engine.
func (s *Snapshot) EnginePoint(id engine.ID) (metrics.Point, bool) {
	if s == nil || s.metrics == nil {
		return metrics.Point{}, false
	}
	p, ok := s.totals[id]
	return p, ok
}

// Series returns up to n of an instance's newest points, oldest first.
func (s *Snapshot) Series(k engine.Key, n int) []metrics.Point {
	if s == nil || s.metrics == nil {
		return nil
	}
	r, ok := s.metrics.series[k]
	if !ok {
		return nil
	}
	return r.Tail(nil, n)
}

// EngineSeries returns up to n of an engine's newest aggregates, oldest first.
func (s *Snapshot) EngineSeries(id engine.ID, n int) []metrics.Point {
	if s == nil || s.metrics == nil {
		return nil
	}
	r, ok := s.metrics.engines[id]
	if !ok {
		return nil
	}
	return r.Tail(nil, n)
}

// Sampling returns how an engine's metrics are read.
func (s *Snapshot) Sampling(id engine.ID) SamplingHealth {
	if s == nil || s.metrics == nil {
		return SamplingHealth{}
	}
	return s.sampling[id]
}

// Points returns the latest point of every sampled instance. Read only.
func (s *Snapshot) Points() map[engine.Key]metrics.Point {
	if s == nil {
		return nil
	}
	return s.latest
}
