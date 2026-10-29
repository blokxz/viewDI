package jobs

import (
	"context"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/provider/fake"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func setup(t *testing.T) (*fake.World, *Runner) {
	t.Helper()
	w := fake.NewWorld(nil)
	fake.Homelab(w)
	ctx, cancel := context.WithCancel(context.Background())
	r := New(ctx, Options{}, w.Engines()...)
	t.Cleanup(func() {
		cancel()
		r.Wait()
	})
	return w, r
}

func key(t *testing.T, w *fake.World, id engine.ID, name string) engine.Key {
	t.Helper()
	in, ok := w.Engine(id).Instance(name)
	if !ok {
		t.Fatalf("no %s", name)
	}
	return in.Key()
}

// final collects updates until every listed job is over.
func final(t *testing.T, r *Runner, ids ...ID) map[ID]Job {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := map[ID]Job{}
	seen := map[ID][]State{}
	for {
		done := true
		for _, id := range ids {
			if !out[id].State.Final() {
				done = false
			}
		}
		if done {
			for _, id := range ids {
				if st := seen[id]; st[len(st)-1] != out[id].State {
					t.Fatalf("job %d: states %v", id, st)
				}
			}
			return out
		}
		js, ok := r.Next(ctx)
		if !ok {
			t.Fatalf("no final state; have %+v", out)
		}
		for _, j := range js {
			out[j.ID] = j
			seen[j.ID] = append(seen[j.ID], j.State)
		}
	}
}

func TestActionSucceeds(t *testing.T) {
	w, r := setup(t)
	id := r.Submit(Request{Key: key(t, w, engine.Docker, "web-api"), Name: "web-api", Action: engine.Stop})
	j := final(t, r, id)[id]
	if j.State != Succeeded || j.Title() != "stop web-api" || j.Started.IsZero() || j.Ended.Before(j.Started) {
		t.Fatalf("job %+v", j)
	}
	if in, _ := w.Docker.Instance("web-api"); in.State != engine.StateStopped {
		t.Fatalf("web-api is %v", in.State)
	}
}

func TestConflictFails(t *testing.T) {
	w, r := setup(t)
	id := r.Submit(Request{Key: key(t, w, engine.Docker, "web-api"), Name: "web-api", Delete: true})
	j := final(t, r, id)[id]
	if j.State != Failed || j.Class != engine.ClassConflict || j.Err != "instance is running: stop it first" {
		t.Fatalf("job %+v", j)
	}
}

func TestDeleteWithStop(t *testing.T) {
	w, r := setup(t)
	id := r.Submit(Request{Key: key(t, w, engine.Incus, "web-lab"), Name: "web-lab", Delete: true, DeleteOptions: engine.DeleteOptions{Stop: true}})
	if j := final(t, r, id)[id]; j.State != Succeeded {
		t.Fatalf("job %+v", j)
	}
	if _, ok := w.Incus.Instance("web-lab"); ok {
		t.Fatal("web-lab still exists")
	}
}

// Stage 4 acceptance: the daemon dies mid-operation → the job fails with a
// clear message.
func TestDaemonDiesMidOperation(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	w := fake.NewWorld(clk)
	fake.Homelab(w)
	ctx, cancel := context.WithCancel(context.Background())
	r := New(ctx, Options{Clock: clk}, w.Engines()...)
	t.Cleanup(func() { cancel(); r.Wait() })
	w.Docker.SetActionDelay(time.Second)
	id := r.Submit(Request{Key: key(t, w, engine.Docker, "redis-cache"), Name: "redis-cache", Action: engine.Restart})
	// Two timers wait: the job timeout and the action in progress.
	if !clk.WaitPending(2, 5*time.Second) {
		t.Fatal("the action never started")
	}
	w.Docker.Disconnect()
	clk.Advance(time.Second)
	j := final(t, r, id)[id]
	if j.State != Failed || j.Class != engine.ClassTransient || j.Err == "" {
		t.Fatalf("job %+v", j)
	}
}

func TestQueueAndCancel(t *testing.T) {
	w, r := setup(t)
	w.Docker.SetActionDelay(time.Hour) // the first jobs never finish by themselves
	var ids []ID
	for _, n := range []string{"web-api", "web-db", "redis-cache", "nginx-proxy", "grafana"} {
		ids = append(ids, r.Submit(Request{Key: key(t, w, engine.Docker, n), Name: n, Action: engine.Stop}))
	}
	// One of the five waits for a slot (goroutines start in any order).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	latest := map[ID]State{}
	for {
		js, ok := r.Next(ctx)
		if !ok {
			t.Fatal("jobs never started")
		}
		for _, j := range js {
			latest[j.ID] = j.State
		}
		running, queued := 0, 0
		for _, st := range latest {
			switch st {
			case Running:
				running++
			case Queued:
				queued++
			}
		}
		if running > MaxRunning {
			t.Fatalf("%d jobs running", running)
		}
		if running == MaxRunning && queued == 1 && len(latest) == 5 {
			break
		}
	}
	for _, id := range ids {
		if !r.Cancel(id) {
			t.Fatalf("cancel %d refused", id)
		}
	}
	for id, j := range final(t, r, ids...) {
		if j.State != Canceled {
			t.Errorf("job %d %v", id, j.State)
		}
	}
	if r.Cancel(ids[0]) {
		t.Error("cancel of a finished job accepted")
	}
}

func TestUnknownEngine(t *testing.T) {
	_, r := setup(t)
	id := r.Submit(Request{Key: "podman/x", Name: "x", Action: engine.Start})
	if j := final(t, r, id)[id]; j.State != Failed {
		t.Fatalf("job %+v", j)
	}
}
