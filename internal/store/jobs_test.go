package store

import (
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

func job(id uint64, k engine.Key, a engine.Action, st JobState, at time.Time) JobUpdated {
	verb := string(a)
	if a == "" {
		verb = "delete"
	}
	return JobUpdated{Job: Job{ID: id, Key: k, Name: "a", Verb: verb, Action: a, Lifecycle: true, State: st, Queued: at, Ended: at}}
}

func TestPendingUntilConfirmed(t *testing.T) {
	s := New(engine.Docker)
	t0 := time.Unix(1000, 0)
	s.Apply(Synced{Engine: engine.Docker, Instances: []engine.Instance{inst(engine.Docker, "a", engine.StateRunning)}})
	k := engine.Key("docker/a")

	s.Apply(job(1, k, engine.Stop, JobRunning, t0))
	if p, ok := s.Snapshot().Pending(k); !ok || p.Label() != "stopping" || p.Done {
		t.Fatalf("pending %+v %v", p, ok)
	}
	s.Apply(job(1, k, engine.Stop, JobSucceeded, t0.Add(time.Second)))
	if p, ok := s.Snapshot().Pending(k); !ok || !p.Done {
		t.Fatal("success must wait for the engine's confirmation")
	}
	s.Apply(InstanceUpserted{Instance: inst(engine.Docker, "a", engine.StateStopped)})
	if _, ok := s.Snapshot().Pending(k); ok {
		t.Fatal("the stop event did not confirm the job")
	}
	if got := s.Snapshot().Jobs(); len(got) != 1 || got[0].State != JobSucceeded {
		t.Fatalf("history %+v", got)
	}
}

func TestPendingAlreadyInEffectConfirmsAtOnce(t *testing.T) {
	s := New(engine.Docker)
	s.Apply(Synced{Engine: engine.Docker, Instances: []engine.Instance{inst(engine.Docker, "a", engine.StateRunning)}})
	k := engine.Key("docker/a")
	s.Apply(job(1, k, engine.Start, JobRunning, time.Unix(1, 0)))
	s.Apply(job(1, k, engine.Start, JobSucceeded, time.Unix(2, 0)))
	if _, ok := s.Snapshot().Pending(k); ok {
		t.Fatal("start of a running instance stays pending")
	}
}

func TestPendingFailureAndExpiry(t *testing.T) {
	s := New(engine.Docker)
	t0 := time.Unix(1000, 0)
	s.Apply(Synced{Engine: engine.Docker, Instances: []engine.Instance{
		inst(engine.Docker, "a", engine.StateRunning), inst(engine.Docker, "b", engine.StateRunning),
	}})
	s.Apply(job(1, "docker/a", engine.Pause, JobRunning, t0))
	s.Apply(job(1, "docker/a", engine.Pause, JobFailed, t0))
	if _, ok := s.Snapshot().Pending("docker/a"); ok {
		t.Fatal("a failed job stays pending")
	}

	s.Apply(job(2, "docker/b", "", JobRunning, t0))
	s.Apply(job(2, "docker/b", "", JobSucceeded, t0))
	if s.Evaluate(t0.Add(ConfirmTimeout / 2)); len(s.TakeUnconfirmed()) != 0 {
		t.Fatal("expired too early")
	}
	if !s.Evaluate(t0.Add(ConfirmTimeout + time.Second)) {
		t.Fatal("expiry is a change")
	}
	if un := s.TakeUnconfirmed(); len(un) != 1 || un[0].ID != 2 || s.HasPending() {
		t.Fatalf("unconfirmed %+v", un)
	}
	// A later removal is still welcome; nothing is pending any more.
	s.Apply(InstanceRemoved{Engine: engine.Docker, ID: "b"})
	if len(s.TakeUnconfirmed()) != 0 {
		t.Fatal("unconfirmed reported twice")
	}
}

func TestRestartNeedsANewStart(t *testing.T) {
	s := New(engine.Docker)
	t0 := time.Unix(1000, 0)
	a := inst(engine.Docker, "a", engine.StateRunning)
	a.Started = t0
	s.Apply(Synced{Engine: engine.Docker, Instances: []engine.Instance{a}})
	s.Apply(job(1, "docker/a", engine.Restart, JobRunning, t0.Add(time.Hour)))
	s.Apply(job(1, "docker/a", engine.Restart, JobSucceeded, t0.Add(time.Hour)))
	if _, ok := s.Snapshot().Pending("docker/a"); !ok {
		t.Fatal("restart confirmed before the instance restarted")
	}
	a.Started = t0.Add(time.Hour)
	s.Apply(InstanceUpserted{Instance: a})
	if _, ok := s.Snapshot().Pending("docker/a"); ok {
		t.Fatal("new start time did not confirm the restart")
	}
}

func TestCreateJobLeavesNoPendingMark(t *testing.T) {
	s := New()
	k := engine.MakeKey(engine.Docker, "")
	s.Apply(JobUpdated{Job: Job{ID: 1, Key: k, Name: "new-web", Verb: "create", State: JobRunning, Progress: engine.Progress{Phase: "pulling", Current: 1, Total: 2}}})
	if s.HasPending() {
		t.Fatal("a create marked an instance pending")
	}
	s.Apply(JobUpdated{Job: Job{ID: 1, Key: k, Name: "new-web", Verb: "create", State: JobSucceeded, Result: "abc"}})
	if js := s.Snapshot().Jobs(); len(js) != 1 || js[0].Result != "abc" || s.HasPending() {
		t.Fatalf("jobs %+v", js)
	}
}
