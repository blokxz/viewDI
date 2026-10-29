package fake

import (
	"context"
	"errors"
	"io"
	"syscall"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/engine/enginetest"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func homelabExpect(id engine.ID) enginetest.Expect {
	if id == engine.Incus {
		return enginetest.Expect{
			Engine: engine.Incus,
			Instances: []enginetest.Instance{
				{Name: "web-lab", Project: "default", Kind: engine.KindContainer, State: engine.StateRunning},
				{Name: "k3s-node1", Project: "default", Kind: engine.KindVM, State: engine.StateRunning},
				{Name: "test-alpine", Project: "default", Kind: engine.KindContainer, State: engine.StateStopped},
				{Name: "ci-runner", Project: "lab", Kind: engine.KindContainer, State: engine.StatePaused},
			},
			Networks: []string{"incusbr0"},
			Volumes:  []string{"backups"},
			Toggle:   "web-lab",
			Projects: []string{"default", "lab"},
		}
	}
	return enginetest.Expect{
		Engine: engine.Docker,
		Instances: []enginetest.Instance{
			{Name: "web-api", Kind: engine.KindContainer, State: engine.StateRunning},
			{Name: "postgres-dev", Kind: engine.KindContainer, State: engine.StateStopped},
			{Name: "builder", Kind: engine.KindContainer, State: engine.StateCreated},
		},
		Networks: []string{"bridge", "web_default"},
		Volumes:  []string{"web_dbdata"},
		Toggle:   "web-api",
	}
}

func TestContract(t *testing.T) {
	for _, id := range engine.IDs {
		t.Run(string(id), func(t *testing.T) {
			enginetest.Run(t, enginetest.Harness{
				New: func(*testing.T) engine.Engine {
					w := NewWorld(nil)
					Homelab(w)
					return w.Engine(id)
				},
				Expect: homelabExpect(id),
				Trigger: func(t *testing.T, e engine.Engine) {
					f := e.(*Engine)
					name := homelabExpect(id).Toggle
					if !f.SetState(name, engine.StateStopped) || !f.SetState(name, engine.StateRunning) {
						t.Fatal("toggle failed")
					}
				},
			})
		})
	}
}

func TestFaults(t *testing.T) {
	w := NewWorld(nil)
	Homelab(w)
	d := w.Docker
	ctx := context.Background()

	d.FailErrno(syscall.EACCES)
	if err := d.Ping(ctx); engine.Classify(err) != engine.ClassPermission {
		t.Fatalf("EACCES: %v (%v)", err, engine.Classify(err))
	}
	d.FailErrno(syscall.ENOENT)
	if err := d.Ping(ctx); engine.Classify(err) != engine.ClassMissing {
		t.Fatalf("ENOENT: %v", err)
	}
	d.Disconnect()
	if err := d.Ping(ctx); engine.Classify(err) != engine.ClassTransient {
		t.Fatalf("refused: %v", err)
	}
	d.Recover()
	if err := d.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	d.Hang()
	hctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := d.Ping(hctx); !engine.IsTimeout(err) {
		t.Fatalf("hang: %v", err)
	}
	d.Recover()
}

func TestDisconnectEndsFeedWithEOF(t *testing.T) {
	w := NewWorld(nil)
	Homelab(w)
	feed, err := w.Incus.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	w.Incus.Disconnect()
	for range feed.C() {
	}
	if !errors.Is(feed.Err(), io.EOF) {
		t.Fatalf("feed err %v, want EOF", feed.Err())
	}
}

func TestHangStallsEventsUntilRecover(t *testing.T) {
	w := NewWorld(nil)
	Homelab(w)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feed, err := w.Docker.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}
	w.Docker.Hang()
	w.Docker.SetState("web-api", engine.StateStopped)
	select {
	case ev := <-feed.C():
		t.Fatalf("event %v delivered while hung", ev)
	case <-time.After(30 * time.Millisecond):
	}
	w.Docker.Recover()
	select {
	case ev := <-feed.C():
		if ev.Action != "die" || ev.Name != "web-api" {
			t.Fatalf("event %+v", ev)
		}
	case <-time.After(enginetest.Timeout):
		t.Fatal("event not delivered after recovery")
	}
}

func TestIncusActionNames(t *testing.T) {
	w := NewWorld(nil)
	Homelab(w)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feed, _ := w.Incus.Events(ctx)
	w.Incus.SetState("ci-runner", engine.StateRunning)
	w.Incus.Remove("test-alpine")
	want := []struct {
		action string
		kind   engine.EventKind
	}{{"instance-resumed", engine.EventChanged}, {"instance-deleted", engine.EventRemoved}}
	for _, wnt := range want {
		ev := <-feed.C()
		if ev.Action != wnt.action || ev.Kind != wnt.kind {
			t.Fatalf("got %+v, want %v", ev, wnt)
		}
	}
}

func TestLarge(t *testing.T) {
	w := NewWorld(nil)
	Large(w, 500)
	ctx := context.Background()
	d, _ := w.Docker.ListInstances(ctx, engine.ListOptions{})
	i, _ := w.Incus.ListInstances(ctx, engine.ListOptions{})
	if len(d)+len(i) != 500 || len(d) != 300 {
		t.Fatalf("docker %d incus %d", len(d), len(i))
	}
}

func TestScenarioRunOnFakeClock(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	w := NewWorld(clk)
	s := NewScenario("t", Homelab)
	s.At(2*time.Second).Stop(engine.Docker, "web-api")
	s.At(3*time.Second).Hang(engine.Incus, 5*time.Second)
	s.Build(w)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	ran := make(chan string, 8)
	go func() {
		defer close(done)
		s.Run(ctx, w, func(st Step) { ran <- st.Desc })
	}()
	advance := func(d time.Duration) {
		t.Helper()
		if !clk.WaitPending(1, enginetest.Timeout) {
			t.Fatal("scenario not waiting")
		}
		clk.Advance(d)
	}
	advance(2 * time.Second)
	if got := <-ran; got != "docker web-api → stopped" {
		t.Fatalf("step %q", got)
	}
	advance(time.Second)
	<-ran
	if hung, _ := w.Incus.hung(); !hung {
		t.Fatal("incus should hang")
	}
	advance(5 * time.Second)
	<-ran
	<-done
	if hung, _ := w.Incus.hung(); hung {
		t.Fatal("incus should have recovered")
	}
	if in, _ := w.Docker.Instance("web-api"); in.State != engine.StateStopped || !in.Started.IsZero() {
		t.Fatalf("web-api = %+v", in)
	}
}

func TestBundledScenariosBuild(t *testing.T) {
	for name, s := range Scenarios() {
		w := NewWorld(nil)
		s.Build(w)
		if len(w.Docker.Names()) == 0 {
			t.Errorf("%s: empty world", name)
		}
	}
}
