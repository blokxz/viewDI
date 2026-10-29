// Package store holds the application state the UI renders: per-engine
// connection health, daemon info and the instance index keyed by stable
// keys. The supervisor describes what happened as Changes; the root model
// applies them in Update (single goroutine) and hands views immutable
// Snapshots.
package store

import (
	"cmp"
	"maps"
	"slices"
	"time"

	"github.com/blokxz/viewDI/internal/alerts"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
)

// ConnState is an engine's connection state (docs/02-architecture.md §4.2).
type ConnState int

// Connection states.
const (
	StateUnknown     ConnState = iota // no report yet
	StateConnecting                   // dialling, listing
	StateConnected                    // live
	StateDegraded                     // transient failure, reconnecting with backoff
	StateUnavailable                  // needs the user (permission, not installed)
)

func (s ConnState) String() string {
	switch s {
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateDegraded:
		return "degraded"
	case StateUnavailable:
		return "unavailable"
	}
	return "unknown"
}

// Health is an engine's connection status as reported by its supervisor.
type Health struct {
	State   ConnState
	Since   time.Time    // when State was entered
	RetryAt time.Time    // next reconnect attempt (Degraded, Unavailable)
	Err     string       // last error
	Class   engine.Class // class of the last error
	Hint    string       // how to fix it (Unavailable)
	Socket  string       // the endpoint in use
}

// Change is one state change, produced by the supervisor.
type Change interface{ isChange() }

// StatusChanged reports a new connection status.
type StatusChanged struct {
	Engine engine.ID
	Health Health
}

// Synced replaces everything known about an engine after a full list.
type Synced struct {
	Engine    engine.ID
	At        time.Time
	Project   string // project the list was scoped to ("" = all)
	Info      engine.Info
	Instances []engine.Instance
	Networks  []engine.Network
	Volumes   []engine.Volume
}

// InstanceUpserted adds or replaces one instance (after an event).
type InstanceUpserted struct{ Instance engine.Instance }

// InstanceRemoved drops one instance.
type InstanceRemoved struct {
	Engine engine.ID
	ID     string
}

func (StatusChanged) isChange()    {}
func (Synced) isChange()           {}
func (InstanceUpserted) isChange() {}
func (InstanceRemoved) isChange()  {}

// Store is the mutable state. It is owned by the UI goroutine.
type Store struct {
	ids     []engine.ID
	engines map[engine.ID]*engineState
	metrics *metricsState
	jobs    *jobState
	alerts  *alerts.Evaluator
	version uint64
	snap    *Snapshot
}

type engineState struct {
	health    Health
	info      engine.Info
	infoKnown bool
	lastSync  time.Time
	project   string
	instances map[engine.Key]engine.Instance
	networks  []engine.Network
	volumes   []engine.Volume
}

// New returns an empty store for the given engines (display order).
func New(ids ...engine.ID) *Store {
	s := &Store{ids: ids, engines: map[engine.ID]*engineState{}, metrics: newMetricsState(), jobs: newJobState(), alerts: alerts.New(alerts.DefaultConfig())}
	for _, id := range ids {
		s.engines[id] = &engineState{instances: map[engine.Key]engine.Instance{}}
	}
	return s
}

// Apply applies changes in order and reports whether anything changed.
// Changes for unknown engines are ignored.
func (s *Store) Apply(changes ...Change) bool {
	changed := false
	for _, c := range changes {
		if s.apply(c) {
			changed = true
		}
	}
	if changed && len(s.jobs.pending) > 0 {
		s.confirmAll()
	}
	if changed {
		s.version++
		s.snap = nil
	}
	return changed
}

func (s *Store) apply(c Change) bool {
	switch c := c.(type) {
	case StatusChanged:
		e, ok := s.engines[c.Engine]
		if !ok || e.health == c.Health {
			return false
		}
		e.health = c.Health
		return true
	case Synced:
		e, ok := s.engines[c.Engine]
		if !ok {
			return false
		}
		e.info, e.infoKnown, e.lastSync, e.project = c.Info, true, c.At, c.Project
		e.instances = make(map[engine.Key]engine.Instance, len(c.Instances))
		for _, in := range c.Instances {
			e.instances[in.Key()] = in
		}
		e.networks, e.volumes = c.Networks, c.Volumes
		s.pruneSeries()
		return true
	case InstanceUpserted:
		e, ok := s.engines[c.Instance.Engine]
		if !ok {
			return false
		}
		e.instances[c.Instance.Key()] = c.Instance
		return true
	case InstanceRemoved:
		e, ok := s.engines[c.Engine]
		if !ok {
			return false
		}
		k := engine.MakeKey(c.Engine, c.ID)
		if _, ok := e.instances[k]; !ok {
			return false
		}
		delete(e.instances, k)
		delete(s.metrics.series, k)
		return true
	case MetricsSampled:
		s.applySample(c)
		return true
	case EventRecorded:
		s.metrics.events.Push(c.Event)
		s.alerts.ObserveEvent(c.Event)
		return true
	case JobUpdated:
		return s.applyJob(c)
	}
	return false
}

// Version increments on every effective change.
func (s *Store) Version() uint64 { return s.version }

// Snapshot returns an immutable view of the current state. It is cached
// until the next change, so calling it every frame is cheap. Callers must
// not modify it.
func (s *Store) Snapshot() *Snapshot {
	if s.snap != nil {
		return s.snap
	}
	m := s.metrics
	snap := &Snapshot{
		Version: s.version, metrics: m, latest: m.latest, totals: m.totals, sampling: m.sampling,
		Alerts: s.alerts.Active(), pending: maps.Clone(s.jobs.pending),
	}
	snap.jobs = slices.Collect(maps.Values(s.jobs.jobs))
	slices.SortFunc(snap.jobs, func(a, b Job) int { return cmp.Compare(b.ID, a.ID) })
	events := m.events.Tail(nil, maxEvents)
	snap.Events = make([]engine.Event, len(events))
	for i, ev := range events { // newest first
		snap.Events[len(events)-1-i] = ev
	}
	total := 0
	for _, id := range s.ids {
		total += len(s.engines[id].instances)
	}
	snap.Instances = make([]engine.Instance, 0, total)
	for _, id := range s.ids {
		e := s.engines[id]
		ev := EngineView{
			ID: id, Health: e.health, Info: e.info, InfoKnown: e.infoKnown,
			LastSync: e.lastSync, Project: e.project, Networks: e.networks, Volumes: e.volumes,
		}
		ev.Stale = e.infoKnown && (e.health.State == StateDegraded || e.health.State == StateUnavailable || e.health.State == StateConnecting)
		for _, k := range slices.Sorted(maps.Keys(e.instances)) {
			in := e.instances[k]
			ev.Counts.add(in)
			snap.Instances = append(snap.Instances, in)
		}
		snap.Engines = append(snap.Engines, ev)
	}
	s.snap = snap
	return snap
}

// Snapshot is an immutable view of the store.
type Snapshot struct {
	Version   uint64
	Engines   []EngineView      // display order
	Instances []engine.Instance // every engine, sorted by key
	Alerts    []alerts.Alert    // active alerts, most severe first
	Events    []engine.Event    // recent instance events, newest first

	pending map[engine.Key]Pending
	jobs    []Job // newest first

	metrics  *metricsState
	latest   map[engine.Key]metrics.Point
	totals   map[engine.ID]metrics.Point
	sampling map[engine.ID]SamplingHealth
}

// EngineView is one engine in a snapshot.
type EngineView struct {
	ID        engine.ID
	Health    Health
	Info      engine.Info
	InfoKnown bool // at least one full sync happened
	LastSync  time.Time
	Project   string
	Stale     bool // data shown is from before the current outage
	Counts    Counts
	Networks  []engine.Network
	Volumes   []engine.Volume
}

// Counts summarises instances by state.
type Counts struct {
	Total, Running, Stopped, Paused, VMs int
}

func (c *Counts) add(in engine.Instance) {
	c.Total++
	switch in.State {
	case engine.StateRunning, engine.StateRestarting:
		c.Running++
	case engine.StatePaused:
		c.Paused++
	default:
		c.Stopped++
	}
	if in.Kind == engine.KindVM {
		c.VMs++
	}
}

// Engine returns the view of one engine (zero value if unknown).
func (s *Snapshot) Engine(id engine.ID) EngineView {
	if s == nil {
		return EngineView{ID: id}
	}
	for _, e := range s.Engines {
		if e.ID == id {
			return e
		}
	}
	return EngineView{ID: id}
}

// Projects returns the Incus projects known from the last sync.
func (s *Snapshot) Projects() []string {
	return slices.Clone(s.Engine(engine.Incus).Info.Projects)
}

// Lookup finds an instance by key.
func (s *Snapshot) Lookup(k engine.Key) (engine.Instance, bool) {
	if s == nil {
		return engine.Instance{}, false
	}
	i, ok := slices.BinarySearchFunc(s.Instances, k, func(in engine.Instance, k engine.Key) int { return cmp.Compare(in.Key(), k) })
	if !ok {
		return engine.Instance{}, false
	}
	return s.Instances[i], true
}
