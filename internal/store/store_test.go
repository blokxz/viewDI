package store

import (
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

func inst(e engine.ID, id string, st engine.State) engine.Instance {
	return engine.Instance{Engine: e, ID: id, Name: id, State: st}
}

func TestApplyAndSnapshot(t *testing.T) {
	s := New(engine.Docker, engine.Incus)
	if s.Apply(InstanceRemoved{Engine: engine.Docker, ID: "nope"}) {
		t.Fatal("removing an unknown instance is not a change")
	}
	at := time.Unix(100, 0)
	s.Apply(Synced{Engine: engine.Docker, At: at, Info: engine.Info{Version: "29"}, Instances: []engine.Instance{
		inst(engine.Docker, "b", engine.StateRunning), inst(engine.Docker, "a", engine.StateStopped),
	}})
	s.Apply(Synced{Engine: engine.Incus, Instances: []engine.Instance{inst(engine.Incus, "default/x", engine.StatePaused)}})
	v := s.Version()
	snap := s.Snapshot()
	if snap != s.Snapshot() {
		t.Fatal("snapshot must be cached until a change")
	}
	keys := []engine.Key{"docker/a", "docker/b", "incus/default/x"}
	for i, in := range snap.Instances {
		if in.Key() != keys[i] {
			t.Fatalf("order: %v", snap.Instances)
		}
	}
	d := snap.Engine(engine.Docker)
	if d.Counts != (Counts{Total: 2, Running: 1, Stopped: 1}) || !d.InfoKnown || !d.LastSync.Equal(at) {
		t.Fatalf("docker view %+v", d)
	}

	s.Apply(InstanceUpserted{Instance: inst(engine.Docker, "a", engine.StateRunning)})
	s.Apply(InstanceRemoved{Engine: engine.Incus, ID: "default/x"})
	if s.Version() != v+2 {
		t.Fatalf("version %d", s.Version())
	}
	snap2 := s.Snapshot()
	if snap2 == snap || snap2.Engine(engine.Docker).Counts.Running != 2 || snap2.Engine(engine.Incus).Counts.Total != 0 {
		t.Fatalf("after changes: %+v", snap2.Engines)
	}
	if in, ok := snap2.Lookup("docker/a"); !ok || in.State != engine.StateRunning {
		t.Fatal("lookup")
	}
	// The old snapshot is unaffected.
	if in, _ := snap.Lookup("docker/a"); in.State != engine.StateStopped {
		t.Fatal("snapshots must be immutable")
	}
}

func TestResyncDropsMissedRemovals(t *testing.T) {
	s := New(engine.Docker)
	s.Apply(Synced{Engine: engine.Docker, Instances: []engine.Instance{inst(engine.Docker, "a", 0), inst(engine.Docker, "b", 0)}})
	s.Apply(Synced{Engine: engine.Docker, Instances: []engine.Instance{inst(engine.Docker, "a", 0)}})
	if _, ok := s.Snapshot().Lookup("docker/b"); ok {
		t.Fatal("a full sync must replace the index")
	}
}

func TestStaleAndHealth(t *testing.T) {
	s := New(engine.Incus)
	h := Health{State: StateDegraded, Err: "refused"}
	if !s.Apply(StatusChanged{Engine: engine.Incus, Health: h}) || s.Apply(StatusChanged{Engine: engine.Incus, Health: h}) {
		t.Fatal("identical status must not count as a change")
	}
	if s.Snapshot().Engine(engine.Incus).Stale {
		t.Fatal("nothing to be stale before the first sync")
	}
	s.Apply(Synced{Engine: engine.Incus})
	if !s.Snapshot().Engine(engine.Incus).Stale {
		t.Fatal("degraded engine with data is stale")
	}
	s.Apply(StatusChanged{Engine: engine.Incus, Health: Health{State: StateConnected}})
	if s.Snapshot().Engine(engine.Incus).Stale {
		t.Fatal("connected engine is fresh")
	}
	if s.Apply(StatusChanged{Engine: "podman", Health: h}) {
		t.Fatal("unknown engine")
	}
}
