// Package alerts evaluates alert rules over the engine state and metrics
// (unhealthy healthcheck, restart loop, sustained memory or CPU pressure,
// degraded or unavailable engine). Alerts have stable IDs, so a condition
// that stays true is one alert however often it is evaluated, including
// across reconnects; alerts resolve on their own when the condition clears.
//
// Evaluation is pure and cheap: it runs in the UI goroutine on store data.
package alerts

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
)

// Severity orders alerts.
type Severity int

// Severities.
const (
	Info Severity = iota
	Warn
	Error
)

// Rule names an alert rule.
type Rule string

// Rules.
const (
	RuleEngineDegraded    Rule = "engine-degraded"
	RuleEngineUnavailable Rule = "engine-unavailable"
	RuleUnhealthy         Rule = "unhealthy"
	RuleRestartLoop       Rule = "restart-loop"
	RuleMemory            Rule = "memory"
	RuleCPU               Rule = "cpu"
)

// Alert is one active alert.
type Alert struct {
	ID       string // rule + subject; stable while the condition holds
	Rule     Rule
	Severity Severity
	Engine   engine.ID
	Key      engine.Key // instance alerts; "" for engine alerts
	Subject  string     // instance or engine name
	Text     string
	Since    time.Time
}

// Config holds the thresholds.
type Config struct {
	MemoryPercent float64
	MemoryFor     time.Duration
	CPUPercent    float64
	CPUFor        time.Duration
	RestartCount  int
	RestartWindow time.Duration
	UnhealthyFor  time.Duration
}

// DefaultConfig matches config.toml's defaults.
func DefaultConfig() Config {
	return Config{
		MemoryPercent: 90, MemoryFor: 30 * time.Second, CPUPercent: 90, CPUFor: time.Minute,
		RestartCount: 3, RestartWindow: 10 * time.Minute,
	}
}

// EngineHealth is the connection state an engine rule needs.
type EngineHealth struct {
	ID          engine.ID
	Connected   bool
	Degraded    bool
	Unavailable bool
	Since       time.Time
	Err         string
}

// Input is everything one evaluation looks at.
type Input struct {
	Engines   []EngineHealth
	Instances []engine.Instance
	Points    map[engine.Key]metrics.Point // latest point per running instance
}

// Evaluator holds alert state between evaluations.
type Evaluator struct {
	cfg      Config
	pending  map[string]time.Time // condition true since, not yet alerting
	active   map[string]Alert
	starts   map[engine.Key][]time.Time
	names    map[engine.Key]string
	seen     map[string]bool // conditions true in the current evaluation
	version  uint64
	snapshot []Alert
}

// New returns an evaluator.
func New(cfg Config) *Evaluator {
	return &Evaluator{
		cfg: cfg, pending: map[string]time.Time{}, active: map[string]Alert{},
		starts: map[engine.Key][]time.Time{}, names: map[engine.Key]string{}, seen: map[string]bool{},
	}
}

// ObserveEvent records instance starts for the restart-loop rule.
func (e *Evaluator) ObserveEvent(ev engine.Event) {
	if !ev.IsStart() {
		return
	}
	k := ev.Key()
	e.starts[k] = append(e.starts[k], ev.Time)
	if ev.Name != "" {
		e.names[k] = ev.Name
	}
}

// Version increments whenever the active set or an alert text changes.
func (e *Evaluator) Version() uint64 { return e.version }

// Active returns the active alerts, most severe first, then newest.
func (e *Evaluator) Active() []Alert { return e.snapshot }

// Evaluate updates the active set at now. It reports whether anything
// changed.
func (e *Evaluator) Evaluate(now time.Time, in Input) bool {
	next := map[string]Alert{}
	connected := map[engine.ID]bool{}
	for _, h := range in.Engines {
		connected[h.ID] = h.Connected
		switch {
		case h.Unavailable:
			e.raise(next, now, 0, Alert{
				Rule: RuleEngineUnavailable, Severity: Error, Engine: h.ID, Subject: h.ID.Label(),
				Text: "engine unavailable: " + h.Err,
			}, h.Since)
		case h.Degraded:
			e.raise(next, now, 0, Alert{
				Rule: RuleEngineDegraded, Severity: Warn, Engine: h.ID, Subject: h.ID.Label(),
				Text: "engine degraded, reconnecting: " + h.Err,
			}, h.Since)
		}
	}
	live := map[engine.Key]bool{}
	for _, inst := range in.Instances {
		k := inst.Key()
		live[k] = true
		if !connected[inst.Engine] {
			// Stale data: keep whatever was active rather than resolving
			// and re-raising on reconnect.
			for id, a := range e.active {
				if a.Key == k {
					next[id] = a
				}
			}
			continue
		}
		if inst.State == engine.StateRunning && inst.Health == engine.HealthUnhealthy {
			e.raise(next, now, e.cfg.UnhealthyFor, Alert{
				Rule: RuleUnhealthy, Severity: Warn, Engine: inst.Engine, Key: k, Subject: inst.Name,
				Text: "unhealthy (healthcheck failing)",
			}, time.Time{})
		}
		if n := e.recentStarts(k, now); n >= e.cfg.RestartCount {
			e.raise(next, now, 0, Alert{
				Rule: RuleRestartLoop, Severity: Warn, Engine: inst.Engine, Key: k, Subject: inst.Name,
				Text: fmt.Sprintf("restarted %d times in %s", n, e.cfg.RestartWindow),
			}, time.Time{})
		}
		p, ok := in.Points[k]
		if !ok || inst.State != engine.StateRunning {
			continue
		}
		if pct := p.MemPercent(); pct >= e.cfg.MemoryPercent {
			e.raise(next, now, e.cfg.MemoryFor, Alert{
				Rule: RuleMemory, Severity: Warn, Engine: inst.Engine, Key: k, Subject: inst.Name,
				Text: fmt.Sprintf("memory %.0f%% of limit (%s/%s)", pct, metrics.Bytes(p.MemUsed), metrics.Bytes(p.MemLimit)),
			}, time.Time{})
		}
		if c := p.CPUOfLimit(); p.Has&engine.HasCPU != 0 && c >= e.cfg.CPUPercent {
			limit := "one core"
			if p.CPULimit > 0 {
				limit = fmt.Sprintf("%g CPUs", p.CPULimit)
			}
			e.raise(next, now, e.cfg.CPUFor, Alert{
				Rule: RuleCPU, Severity: Warn, Engine: inst.Engine, Key: k, Subject: inst.Name,
				Text: fmt.Sprintf("CPU %.0f%% of %s for %s+", c, limit, e.cfg.CPUFor),
			}, time.Time{})
		}
	}
	// Forget state of instances that are gone.
	for k := range e.starts {
		if !live[k] {
			delete(e.starts, k)
			delete(e.names, k)
		}
	}
	for id := range e.pending {
		if _, alerting := next[id]; alerting || !e.seen[id] {
			delete(e.pending, id)
		}
	}
	changed := len(next) != len(e.active)
	for id, a := range next {
		if old, ok := e.active[id]; !ok || old.Text != a.Text {
			changed = true
		}
	}
	e.active = next
	clear(e.seen)
	if changed {
		e.version++
		e.snapshot = make([]Alert, 0, len(next))
		for _, a := range next {
			e.snapshot = append(e.snapshot, a)
		}
		slices.SortFunc(e.snapshot, func(a, b Alert) int {
			return cmp.Or(cmp.Compare(b.Severity, a.Severity), b.Since.Compare(a.Since), cmp.Compare(a.ID, b.ID))
		})
	}
	return changed
}

// raise records that a's condition holds at now. Conditions with a hold
// time only alert once they held that long; since (when set) dates an alert
// that needs no hold. An alert that is already active keeps its Since.
func (e *Evaluator) raise(next map[string]Alert, now time.Time, hold time.Duration, a Alert, since time.Time) {
	subject := string(a.Key)
	if subject == "" {
		subject = string(a.Engine)
	}
	a.ID = string(a.Rule) + ":" + subject
	e.seen[a.ID] = true
	if old, ok := e.active[a.ID]; ok {
		a.Since = old.Since
		next[a.ID] = a
		return
	}
	if hold <= 0 {
		a.Since = since
		if a.Since.IsZero() {
			a.Since = now
		}
		next[a.ID] = a
		return
	}
	start, ok := e.pending[a.ID]
	if !ok {
		e.pending[a.ID], start = now, now
	}
	if now.Sub(start) >= hold {
		a.Since = start
		next[a.ID] = a
	}
}

// recentStarts counts the starts of k within the restart window, dropping
// older ones.
func (e *Evaluator) recentStarts(k engine.Key, now time.Time) int {
	ts := e.starts[k]
	cut := now.Add(-e.cfg.RestartWindow)
	i := 0
	for i < len(ts) && ts[i].Before(cut) {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(e.starts, k)
		return 0
	}
	e.starts[k] = ts
	return len(ts)
}
