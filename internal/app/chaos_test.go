package app

import (
	"bytes"
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics/sampler"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/supervisor"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/themes"
)

// testSource joins the supervisors and the sampler, like main does.
type testSource struct {
	*supervisor.Group
	s *sampler.Sampler
}

func (t testSource) Metrics() <-chan store.MetricsSampled { return t.s.Out() }
func (t testSource) SetInterval(d time.Duration)          { t.s.SetInterval(d) }

// program runs the real Bubble Tea program on fake engines through real
// supervisors. Everything stops (and goleak checks) when the test ends.
func program(t *testing.T, w *fake.World, opts supervisor.Options) *teatest.TestModel {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	g := supervisor.NewGroup(opts, supervisor.Target{Engine: w.Docker, Endpoint: "mock://docker"}, supervisor.Target{Engine: w.Incus, Endpoint: "mock://incus"})
	g.Start(ctx)
	srcs := make([]sampler.Source, 0, 2)
	for _, sup := range g.Supervisors() {
		srcs = append(srcs, sup)
	}
	smp := sampler.New(sampler.Options{Interval: 200 * time.Millisecond}, srcs...)
	sampled := make(chan struct{})
	go func() { defer close(sampled); smp.Run(ctx) }()
	reg, _ := theme.LoadRegistry(themes.FS, "")
	th, _ := reg.Resolve()
	// Short toasts: their expiry timers must not outlive the test (goleak).
	m := New(Options{Themes: reg, Theme: th, Keys: keys.MustDefault(), Source: testSource{g, smp}, Context: ctx, ToastTTL: 50 * time.Millisecond})
	tm := teatest.NewTestModel(t, m,
		teatest.WithInitialTermSize(120, 40),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.Ascii)),
	)
	t.Cleanup(func() {
		_ = tm.Quit()
		tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
		cancel() // ends the pump, the supervisors and the sampler
		g.Wait()
		<-sampled
	})
	return tm
}

// waitFor waits until the output written from now on contains every text.
// The output reader is consumed, so all texts are checked in one wait.
func waitFor(t *testing.T, tm *teatest.TestModel, within time.Duration, texts ...string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		for _, s := range texts {
			if !bytes.Contains(b, []byte(s)) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(within), teatest.WithCheckInterval(5*time.Millisecond))
}

// Resilience principle 1 (docs/05-quality-plan.md §3.1): the UI goroutine
// never waits on I/O. Both engines hang on every call; keys must still be
// processed promptly.
func TestUIRespondsWhileEnginesHang(t *testing.T) {
	w := fake.NewWorld(nil)
	fake.Homelab(w)
	w.Docker.Hang()
	w.Incus.Hang()
	tm := program(t, w, supervisor.Options{Timeout: time.Hour}) // calls never time out during the test
	waitFor(t, tm, 5*time.Second, "connecting…")
	for _, step := range []struct{ key, want string }{
		{"3", "Connecting to the engines"},
		{"4", "reads snapshots only"},
		{"6", "registry digest checks"},
		{"3", "Connecting to the engines"},
	} {
		start := time.Now()
		tm.Type(step.key)
		waitFor(t, tm, time.Second, step.want)
		// The renderer runs at 60 fps here; one frame plus scheduling noise.
		if d := time.Since(start); d > 500*time.Millisecond {
			t.Fatalf("key %s took %s with hung engines", step.key, d)
		}
	}
	w.Docker.Recover()
	w.Incus.Recover()
}

// Stage 1 acceptance: stopping incusd shows the banner within 5 s, Docker
// data is unaffected, and recovery needs no user action.
func TestIncusOutageBannerAndRecovery(t *testing.T) {
	w := fake.NewWorld(nil)
	fake.Homelab(w)
	tm := program(t, w, supervisor.Options{Seed: 3})
	tm.Type("3")
	waitFor(t, tm, 5*time.Second, "6.0.5", "web-lab")

	start := time.Now()
	w.Incus.Disconnect()
	// Match short, distinctive fragments: the renderer skips cells that did
	// not change, so longer strings can be split by cursor moves.
	waitFor(t, tm, 5*time.Second, "connection lost")
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("banner after %s", d)
	}

	// Docker keeps working during the Incus outage.
	w.Docker.SetState("postgres-dev", engine.StateRunning)

	w.Incus.Recover()
	waitFor(t, tm, 10*time.Second, "reconnected")

	_ = tm.Quit()
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	snap := fm.Snapshot()
	for _, id := range engine.IDs {
		if st := snap.Engine(id).Health.State; st != store.StateConnected {
			t.Errorf("%s: %v after recovery", id, st)
		}
	}
	if _, ok := snap.EnginePoint(engine.Docker); !ok {
		t.Error("the sampler kept sampling Docker during the Incus outage")
	}
	pg, _ := snap.Lookup(engine.MakeKey(engine.Docker, w.Docker.InstanceID("", "postgres-dev")))
	if pg.State != engine.StateRunning {
		t.Errorf("Docker event during the Incus outage lost: postgres-dev is %v", pg.State)
	}
}
