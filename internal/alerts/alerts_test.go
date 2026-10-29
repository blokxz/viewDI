package alerts

import (
	"strings"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
)

var t0 = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func connected(ids ...engine.ID) []EngineHealth {
	var out []EngineHealth
	for _, id := range ids {
		out = append(out, EngineHealth{ID: id, Connected: true})
	}
	return out
}

func vm() engine.Instance {
	return engine.Instance{Engine: engine.Incus, ID: "default/k3s", Name: "k3s", State: engine.StateRunning}
}

func memPoint(used uint64) map[engine.Key]metrics.Point {
	return map[engine.Key]metrics.Point{vm().Key(): {Has: engine.HasMem, MemUsed: used, MemLimit: 100}}
}

func TestMemoryNeedsHoldAndResolves(t *testing.T) {
	e := New(DefaultConfig())
	in := Input{Engines: connected(engine.Incus), Instances: []engine.Instance{vm()}, Points: memPoint(95)}
	if e.Evaluate(t0, in) || len(e.Active()) != 0 {
		t.Fatal("no alert before the hold time")
	}
	e.Evaluate(t0.Add(20*time.Second), in)
	if !e.Evaluate(t0.Add(30*time.Second), in) {
		t.Fatal("alert after 30 s")
	}
	a := e.Active()
	if len(a) != 1 || a[0].Rule != RuleMemory || !a[0].Since.Equal(t0) || !strings.Contains(a[0].Text, "95%") {
		t.Fatalf("active %+v", a)
	}
	// Still true: same alert, no change.
	if e.Evaluate(t0.Add(40*time.Second), in) {
		t.Fatal("unchanged condition must not report a change")
	}
	// Clears: resolves at once.
	in.Points = memPoint(50)
	if !e.Evaluate(t0.Add(41*time.Second), in) || len(e.Active()) != 0 {
		t.Fatal("must auto-resolve")
	}
	// A blip shorter than the hold time never alerts.
	in.Points = memPoint(99)
	e.Evaluate(t0.Add(50*time.Second), in)
	in.Points = memPoint(10)
	e.Evaluate(t0.Add(60*time.Second), in)
	in.Points = memPoint(99)
	e.Evaluate(t0.Add(70*time.Second), in)
	if len(e.Active()) != 0 {
		t.Fatal("the hold time restarts after the condition cleared")
	}
}

func TestNoDuplicateAcrossReconnect(t *testing.T) {
	e := New(DefaultConfig())
	in := Input{Engines: connected(engine.Incus), Instances: []engine.Instance{vm()}, Points: memPoint(95)}
	e.Evaluate(t0, in)
	e.Evaluate(t0.Add(time.Minute), in)
	if len(e.Active()) != 1 {
		t.Fatal("setup")
	}
	// Outage: no points, engine degraded. The instance alert is kept.
	down := Input{Engines: []EngineHealth{{ID: engine.Incus, Degraded: true, Since: t0.Add(61 * time.Second), Err: "connection lost"}}, Instances: []engine.Instance{vm()}}
	e.Evaluate(t0.Add(62*time.Second), down)
	a := e.Active()
	if len(a) != 2 || a[0].Rule != RuleEngineDegraded || a[1].Rule != RuleMemory {
		t.Fatalf("during outage %+v", a)
	}
	// Reconnect: the degraded alert resolves, the memory alert is the same one.
	e.Evaluate(t0.Add(70*time.Second), in)
	a = e.Active()
	if len(a) != 1 || a[0].Rule != RuleMemory || !a[0].Since.Equal(t0) {
		t.Fatalf("after reconnect %+v", a)
	}
}

func TestRestartLoopAndUnhealthy(t *testing.T) {
	e := New(DefaultConfig())
	web := engine.Instance{Engine: engine.Docker, ID: "abc", Name: "nginx", State: engine.StateRunning, Health: engine.HealthUnhealthy}
	for i := range 3 {
		e.ObserveEvent(engine.Event{Engine: engine.Docker, InstanceID: "abc", Name: "nginx", Action: "start", Time: t0.Add(time.Duration(i) * time.Minute)})
		e.ObserveEvent(engine.Event{Engine: engine.Docker, InstanceID: "abc", Action: "die", Time: t0.Add(time.Duration(i) * time.Minute)})
	}
	in := Input{Engines: connected(engine.Docker), Instances: []engine.Instance{web}}
	e.Evaluate(t0.Add(3*time.Minute), in)
	a := e.Active()
	if len(a) != 2 {
		t.Fatalf("active %+v", a)
	}
	rules := map[Rule]Alert{}
	for _, x := range a {
		rules[x.Rule] = x
	}
	if !strings.Contains(rules[RuleRestartLoop].Text, "restarted 3 times") || rules[RuleUnhealthy].Subject != "nginx" {
		t.Fatalf("%+v", rules)
	}
	// Out of the window: resolves.
	web.Health = engine.HealthHealthy
	in.Instances = []engine.Instance{web}
	e.Evaluate(t0.Add(11*time.Minute), in)
	if len(e.Active()) != 0 {
		t.Fatalf("after window %+v", e.Active())
	}
}

func TestEngineAlertsAndCPU(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CPUFor = 0
	e := New(cfg)
	inst := engine.Instance{Engine: engine.Docker, ID: "x", Name: "x", State: engine.StateRunning}
	in := Input{
		Engines:   []EngineHealth{{ID: engine.Docker, Connected: true}, {ID: engine.Incus, Unavailable: true, Err: "permission denied", Since: t0}},
		Instances: []engine.Instance{inst},
		Points:    map[engine.Key]metrics.Point{inst.Key(): {Has: engine.HasCPU, CPU: 190, CPULimit: 2}},
	}
	e.Evaluate(t0.Add(time.Second), in)
	a := e.Active()
	if len(a) != 2 || a[0].Severity != Error || a[0].Rule != RuleEngineUnavailable || a[1].Rule != RuleCPU || !strings.Contains(a[1].Text, "95% of 2 CPUs") {
		t.Fatalf("%+v", a)
	}
	if !a[0].Since.Equal(t0) {
		t.Fatal("engine alerts are dated by the outage start")
	}
}
