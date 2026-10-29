package supervisor

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/store"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// rig runs one supervisor over a fake engine on a fake clock.
type rig struct {
	t     *testing.T
	clk   *clock.Fake
	world *fake.World
	eng   *fake.Engine
	sup   *Supervisor
	out   chan store.Change
	st    *store.Store
	stop  func()
}

func newRig(t *testing.T, id engine.ID, setup func(*fake.World)) *rig {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	w := fake.NewWorld(clk)
	if setup != nil {
		setup(w)
	}
	r := &rig{t: t, clk: clk, world: w, eng: w.Engine(id), out: make(chan store.Change, Buffer), st: store.New(engine.IDs...)}
	r.sup = New(r.eng, "/run/"+string(id)+".sock", r.out, Options{Clock: clk, Seed: 1})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.sup.Run(ctx) }()
	r.stop = func() { cancel(); <-done }
	t.Cleanup(r.stop)
	return r
}

// until advances the fake clock in small steps until a change satisfies
// pred, failing after maxAdvance of fake time.
func (r *rig) until(desc string, maxAdvance time.Duration, pred func(store.Change) bool) store.Change {
	r.t.Helper()
	var advanced time.Duration
	for {
		select {
		case c := <-r.out:
			r.st.Apply(c)
			if pred(c) {
				return c
			}
			continue
		case <-time.After(5 * time.Millisecond):
		}
		if advanced > maxAdvance {
			r.t.Fatalf("%s: not seen after %s of fake time", desc, maxAdvance)
		}
		r.clk.Advance(50 * time.Millisecond)
		advanced += 50 * time.Millisecond
	}
}

func isState(s store.ConnState) func(store.Change) bool {
	return func(c store.Change) bool {
		sc, ok := c.(store.StatusChanged)
		return ok && sc.Health.State == s
	}
}

func (r *rig) health(c store.Change) store.Health { return c.(store.StatusChanged).Health }

func TestConnectSyncsThenConnected(t *testing.T) {
	r := newRig(t, engine.Docker, fake.Homelab)
	first := <-r.out
	if !isState(store.StateConnecting)(first) {
		t.Fatalf("first change %#v", first)
	}
	r.until("synced", time.Second, func(c store.Change) bool { _, ok := c.(store.Synced); return ok })
	r.until("connected", time.Second, isState(store.StateConnected))
	snap := r.st.Snapshot().Engine(engine.Docker)
	if snap.Counts.Total != 9 || snap.Info.Version == "" {
		t.Fatalf("after sync: %+v", snap.Counts)
	}
}

func TestEventsCoalesceIntoOneInspect(t *testing.T) {
	r := newRig(t, engine.Docker, fake.Homelab)
	r.until("connected", time.Second, isState(store.StateConnected))
	calls := r.eng.Calls()
	for range 5 {
		r.eng.SetState("web-api", engine.StateStopped)
		r.eng.SetState("web-api", engine.StateRunning)
	}
	r.eng.SetState("web-api", engine.StatePaused)
	c := r.until("upsert", time.Second, func(c store.Change) bool { _, ok := c.(store.InstanceUpserted); return ok })
	if in := c.(store.InstanceUpserted).Instance; in.Name != "web-api" || in.State != engine.StatePaused {
		t.Fatalf("upsert %+v", in)
	}
	// One inspect for eleven events (pings may add calls; none happen within 50 ms).
	if got := r.eng.Calls() - calls; got != 1 {
		t.Fatalf("%d daemon calls for a burst, want 1", got)
	}
	r.eng.Remove("builder")
	c = r.until("removal", time.Second, func(c store.Change) bool { _, ok := c.(store.InstanceRemoved); return ok })
	if c.(store.InstanceRemoved).ID != r.eng.InstanceID("", "builder") {
		t.Fatalf("removed %+v", c)
	}
}

func TestDisconnectDegradesAndRecovers(t *testing.T) {
	r := newRig(t, engine.Incus, fake.Homelab)
	r.until("connected", time.Second, isState(store.StateConnected))
	start := r.clk.Now()
	r.eng.Disconnect()
	h := r.health(r.until("degraded", 5*time.Second, isState(store.StateDegraded)))
	// The event stream ends with EOF at once: well within the 5 s budget.
	if d := r.clk.Now().Sub(start); d > 500*time.Millisecond {
		t.Fatalf("degraded after %s", d)
	}
	if wait := h.RetryAt.Sub(h.Since); wait < 800*time.Millisecond || wait > 1200*time.Millisecond {
		t.Fatalf("first backoff %s, want 1 s ± 20 %%", wait)
	}
	if !strings.Contains(h.Err, "connection lost") || h.Socket != "/run/incus.sock" {
		t.Fatalf("health %+v", h)
	}
	// Second failure doubles the delay.
	h = r.health(r.until("degraded again", 5*time.Second, isState(store.StateDegraded)))
	if wait := h.RetryAt.Sub(h.Since); wait < 1600*time.Millisecond || wait > 2400*time.Millisecond {
		t.Fatalf("second backoff %s, want 2 s ± 20 %%", wait)
	}
	r.eng.Recover()
	r.until("reconnected", 5*time.Second, isState(store.StateConnected))
	if r.st.Snapshot().Engine(engine.Incus).Stale {
		t.Fatal("fresh after reconnect")
	}
}

func TestBackoffCapsAt30s(t *testing.T) {
	r := newRig(t, engine.Docker, nil)
	r.stop()
	r.sup.attempt = 10
	for range 3 {
		if d := r.sup.backoff(); d > 36*time.Second || d < 24*time.Second {
			t.Fatalf("capped backoff %s", d)
		}
	}
}

func TestPermissionIsUnavailableWithHint(t *testing.T) {
	r := newRig(t, engine.Incus, func(w *fake.World) { fake.Homelab(w); w.Incus.FailErrno(syscall.EACCES) })
	h := r.health(r.until("unavailable", time.Second, isState(store.StateUnavailable)))
	if !strings.Contains(h.Hint, "incus-admin") || h.Class != engine.ClassPermission {
		t.Fatalf("health %+v", h)
	}
	if wait := h.RetryAt.Sub(h.Since); wait != 60*time.Second {
		t.Fatalf("reprobe in %s", wait)
	}
	// ctrl+r retries now.
	r.eng.Recover()
	r.sup.Reconnect()
	r.until("connected after ctrl+r", 500*time.Millisecond, isState(store.StateConnected))
}

func TestPermissionRevokedWhileConnected(t *testing.T) {
	r := newRig(t, engine.Docker, fake.Homelab)
	r.until("connected", time.Second, isState(store.StateConnected))
	r.eng.FailErrno(syscall.EACCES)
	h := r.health(r.until("unavailable", 5*time.Second, isState(store.StateUnavailable)))
	if !strings.Contains(h.Hint, "docker group") {
		t.Fatalf("hint %q", h.Hint)
	}
}

func TestMissingSocketBeforeAndAfterConnecting(t *testing.T) {
	r := newRig(t, engine.Docker, func(w *fake.World) { w.Docker.FailErrno(syscall.ENOENT) })
	h := r.health(r.until("unavailable", time.Second, isState(store.StateUnavailable)))
	if !strings.Contains(h.Hint, "not found at /run/docker.sock") {
		t.Fatalf("hint %q", h.Hint)
	}
	r.eng.Recover()
	r.sup.Reconnect()
	r.until("connected", time.Second, isState(store.StateConnected))
	// A daemon that stops and removes its socket is a transient outage.
	r.eng.FailErrno(syscall.ENOENT)
	r.until("degraded", 5*time.Second, isState(store.StateDegraded))
}

func TestHungDaemonDetectedByWatchdog(t *testing.T) {
	r := newRig(t, engine.Incus, fake.Homelab)
	r.until("connected", time.Second, isState(store.StateConnected))
	start := r.clk.Now()
	r.eng.Hang()
	h := r.health(r.until("degraded", 10*time.Second, isState(store.StateDegraded)))
	// Ping interval (2 s) + call timeout (5 s).
	if d := r.clk.Now().Sub(start); d > 7500*time.Millisecond {
		t.Fatalf("hang detected after %s", d)
	}
	if !strings.Contains(h.Err, "not responding") {
		t.Fatalf("err %q", h.Err)
	}
}

func TestPeriodicResyncCorrectsDrift(t *testing.T) {
	r := newRig(t, engine.Docker, fake.Homelab)
	r.until("connected", time.Second, isState(store.StateConnected))
	r.eng.Quietly(func() { r.eng.SetState("redis-cache", engine.StateStopped) }) // event lost
	r.until("synced", 31*time.Second, func(c store.Change) bool { _, ok := c.(store.Synced); return ok })
	in, _ := r.st.Snapshot().Lookup(engine.MakeKey(engine.Docker, r.eng.InstanceID("", "redis-cache")))
	if in.State != engine.StateStopped {
		t.Fatalf("state after resync %v", in.State)
	}
}

func TestProjectScope(t *testing.T) {
	r := newRig(t, engine.Incus, fake.Homelab)
	r.until("connected", time.Second, isState(store.StateConnected))
	r.sup.SetProject("lab")
	c := r.until("scoped sync", time.Second, func(c store.Change) bool {
		s, ok := c.(store.Synced)
		return ok && s.Project == "lab"
	})
	for _, in := range c.(store.Synced).Instances {
		if in.Project != "lab" {
			t.Fatalf("instance %s from %s", in.Name, in.Project)
		}
	}
	// Events outside the project are ignored.
	r.eng.SetState("web-lab", engine.StateStopped)
	r.eng.SetState("dev-arch", engine.StateStopped)
	c = r.until("lab upsert", time.Second, func(c store.Change) bool { _, ok := c.(store.InstanceUpserted); return ok })
	if c.(store.InstanceUpserted).Instance.Name != "dev-arch" {
		t.Fatalf("upsert %+v", c)
	}
}

type panicky struct{ *fake.Engine }

func (p panicky) ListInstances(context.Context, engine.ListOptions) ([]engine.Instance, error) {
	panic("adapter bug")
}

func TestProviderPanicDegrades(t *testing.T) {
	clk := clock.NewFake(time.Now())
	w := fake.NewWorld(clk)
	out := make(chan store.Change, Buffer)
	s := New(panicky{w.Docker}, "x", out, Options{Clock: clk, Seed: 1})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	defer func() { cancel(); <-done }()
	for c := range out {
		if sc, ok := c.(store.StatusChanged); ok && sc.Health.State == store.StateDegraded {
			if !strings.Contains(sc.Health.Err, "adapter bug") {
				t.Fatalf("err %q", sc.Health.Err)
			}
			return
		}
	}
}

func TestGroup(t *testing.T) {
	w := fake.NewWorld(nil)
	fake.Homelab(w)
	g := NewGroup(Options{Seed: 7}, Target{Engine: w.Docker}, Target{Engine: w.Incus})
	g.SetInitialProject("lab")
	ctx, cancel := context.WithCancel(context.Background())
	g.Start(ctx)
	connected := map[engine.ID]bool{}
	for c := range g.Changes() {
		if sc, ok := c.(store.StatusChanged); ok && sc.Health.State == store.StateConnected {
			connected[sc.Engine] = true
		}
		if s, ok := c.(store.Synced); ok && s.Engine == engine.Incus && s.Project != "lab" {
			t.Fatalf("initial project ignored: %q", s.Project)
		}
		if len(connected) == 2 {
			break
		}
	}
	g.Reconnect()
	g.SetProject("")
	cancel()
	g.Wait()
	if fmt.Sprint(g.Engines()) != "[docker incus]" {
		t.Fatal(g.Engines())
	}
}

func TestHints(t *testing.T) {
	if Hint(engine.Docker, engine.ClassTransient, "x") != "" {
		t.Fatal("transient errors need no hint")
	}
	if !strings.Contains(Hint(engine.Incus, engine.ClassMissing, "/var/lib/incus/unix.socket"), "/var/lib/incus/unix.socket") {
		t.Fatal("missing-socket hint names the path")
	}
}
