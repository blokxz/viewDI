package store

import (
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/alerts"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
)

func TestMetricsHistoryAndTotals(t *testing.T) {
	s := New(engine.Docker, engine.Incus)
	a, b := inst(engine.Docker, "a", engine.StateRunning), inst(engine.Docker, "b", engine.StateRunning)
	s.Apply(Synced{Engine: engine.Docker, Instances: []engine.Instance{a, b}})
	t0 := time.Unix(1000, 0)
	for i := range History + 5 {
		s.Apply(MetricsSampled{
			At: t0.Add(time.Duration(i) * time.Second),
			Points: map[engine.Key]metrics.Point{
				a.Key(): {CPU: float64(i), MemUsed: 10},
				b.Key(): {CPU: 1, MemUsed: 5},
			},
			Health: map[engine.ID]SamplingHealth{engine.Docker: {Source: SourceCgroup, Instances: 2}, engine.Incus: {}},
		})
	}
	snap := s.Snapshot()
	if p, ok := snap.Point(a.Key()); !ok || p.CPU != float64(History+4) {
		t.Fatalf("latest %+v", p)
	}
	if tot, ok := snap.EnginePoint(engine.Docker); !ok || tot.CPU != float64(History+5) || tot.MemUsed != 15 {
		t.Fatalf("total %+v", tot)
	}
	if _, ok := snap.EnginePoint(engine.Incus); ok {
		t.Fatal("an engine not sampled has no total")
	}
	ser := snap.Series(a.Key(), 1000)
	if len(ser) != History || ser[len(ser)-1].CPU != float64(History+4) {
		t.Fatalf("series len %d", len(ser))
	}
	if len(snap.EngineSeries(engine.Docker, 10)) != 10 || snap.Sampling(engine.Docker).Source != SourceCgroup {
		t.Fatal("engine series / sampling")
	}
	// Removing an instance drops its history.
	s.Apply(InstanceRemoved{Engine: engine.Docker, ID: "a"})
	if s.Snapshot().Series(a.Key(), 10) != nil {
		t.Fatal("history of a removed instance must be dropped")
	}
}

func TestEventsFeedAndAlerts(t *testing.T) {
	s := New(engine.Docker)
	s.ConfigureAlerts(alerts.Config{RestartCount: 2, RestartWindow: time.Minute, MemoryPercent: 90, CPUPercent: 90})
	web := inst(engine.Docker, "web", engine.StateRunning)
	s.Apply(Synced{Engine: engine.Docker, Instances: []engine.Instance{web}},
		StatusChanged{Engine: engine.Docker, Health: Health{State: StateConnected}})
	t0 := time.Unix(1000, 0)
	for i := range 3 {
		s.Apply(EventRecorded{Event: engine.Event{Engine: engine.Docker, InstanceID: "web", Name: "web", Action: "start", Time: t0.Add(time.Duration(i) * time.Second)}})
	}
	snap := s.Snapshot()
	if len(snap.Events) != 3 || !snap.Events[0].Time.Equal(t0.Add(2*time.Second)) {
		t.Fatalf("events newest first: %+v", snap.Events)
	}
	v := s.Version()
	if !s.Evaluate(t0.Add(3*time.Second)) || s.Version() != v+1 {
		t.Fatal("restart loop must alert")
	}
	if a := s.Snapshot().Alerts; len(a) != 1 || a[0].Rule != alerts.RuleRestartLoop {
		t.Fatalf("alerts %+v", a)
	}
	if s.Evaluate(t0.Add(4 * time.Second)) {
		t.Fatal("unchanged alerts are not a change")
	}
}
