package app

import (
	"bytes"
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/jobs"
	"github.com/blokxz/viewDI/internal/metrics"
	"github.com/blokxz/viewDI/internal/metrics/sampler"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/streams"
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
	tm, _ := programWithStreams(t, w, opts)
	return tm
}

// programOpt adjusts the program's Options.
type programOpt func(*Options)

// programWithStreams is program, also returning the Detail view's stream
// registry.
func programWithStreams(t *testing.T, w *fake.World, opts supervisor.Options, mods ...programOpt) (*teatest.TestModel, *streams.Registry) {
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
	str := streams.New(ctx, streams.Options{}, w.Engines()...)
	runner := jobs.New(ctx, jobs.Options{}, w.Engines()...)
	reg, _ := theme.LoadRegistry(themes.FS, "")
	th, _ := reg.Resolve()
	// Short toasts: their expiry timers must not outlive the test (goleak).
	o := Options{
		Themes: reg, Theme: th, Keys: keys.MustDefault(), Source: testSource{g, smp}, Streams: str, Jobs: runner,
		Context: ctx, ToastTTL: 50 * time.Millisecond,
	}
	for _, mod := range mods {
		mod(&o)
	}
	m := New(o)
	tm := teatest.NewTestModel(t, m,
		teatest.WithInitialTermSize(120, 40),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.Ascii)),
	)
	t.Cleanup(func() {
		_ = tm.Quit()
		tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
		cancel() // ends the pump, the supervisors, the sampler and the streams
		g.Wait()
		str.Wait()
		runner.Wait()
		<-sampled
	})
	return tm, str
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
	// Wait for the header to show the version again. Not for the
	// "reconnected" toast: with the 50 ms toast TTL used here, a slow runner
	// can expire it before a frame paints it (the toast itself is covered by
	// TestDegradedBannerCountdownAndRecoveryToast).
	waitFor(t, tm, 10*time.Second, "6.0.5")

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

// Stage 3: the Detail view shows live logs from the stream registry, and
// esc ends the subscription.
func TestDetailStreamsLiveLogs(t *testing.T) {
	w := fake.NewWorld(nil)
	fake.Homelab(w)
	w.Docker.SetLogProfile("nginx-proxy", fake.LogProfile{Rate: 20})
	tm, str := programWithStreams(t, w, supervisor.Options{})
	tm.Type("3")
	waitFor(t, tm, 5*time.Second, "nginx-proxy")
	tm.Type("/")
	tm.Type("nginx")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab}) // Logs
	waitFor(t, tm, 5*time.Second, "LIVE", "HTTP/1.1")
	if str.Active() != 1 {
		t.Fatalf("%d subscriptions, want 1", str.Active())
	}
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	waitFor(t, tm, 5*time.Second, "Instances")
	if !clock.Eventually(5*time.Second, func() bool { return str.Active() == 0 }) {
		t.Fatalf("%d subscriptions left after esc", str.Active())
	}
}

// Stage 3 acceptance: opening and closing the detail 100 times leaves no
// subscription (and, through goleak, no goroutine) behind.
func TestDetailOpenClose100(t *testing.T) {
	w := fake.NewWorld(nil)
	fake.Homelab(w)
	tm, str := programWithStreams(t, w, supervisor.Options{})
	tm.Type("3")
	waitFor(t, tm, 5*time.Second, "web-api")
	for range 100 {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
		tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	}
	tm.Type("2") // a marker that every key before it was processed
	waitFor(t, tm, 5*time.Second, "Top consumers")
	if !clock.Eventually(5*time.Second, func() bool { return str.Active() == 0 }) {
		t.Fatalf("%d subscriptions left after 100 open/close", str.Active())
	}
}

// Stage 3 acceptance (docs/04-development-plan.md): a log flooding at
// 1 000 lines/s keeps the UI responsive and the log buffer bounded. CI runs
// it for 3 s; VIEWDI_LONG=1 runs the full 60 s and reports the heap.
func TestLogFloodStaysResponsive(t *testing.T) {
	dur := 3 * time.Second
	if os.Getenv("VIEWDI_LONG") != "" {
		dur = 60 * time.Second
	}
	w := fake.NewWorld(nil)
	fake.Homelab(w)
	w.Docker.SetLogProfile("web-api", fake.LogProfile{Rate: 1000, ErrEvery: 50})
	tm, _ := programWithStreams(t, w, supervisor.Options{}, func(o *Options) { o.LogCapacity = 2000 })
	tm.Type("3")
	waitFor(t, tm, 5*time.Second, "web-api")
	tm.Type("/")
	tm.Type("web-api")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab}) // Logs
	waitFor(t, tm, 5*time.Second, "LIVE")

	var worst time.Duration
	end := time.Now().Add(dur)
	for time.Now().Before(end) {
		// Flip between the Inspect and Logs tabs: whole lines change, so
		// the renderer repaints the text waited for.
		for _, step := range []struct {
			key  tea.KeyPressMsg
			want string
		}{{tea.KeyPressMsg{Code: tea.KeyTab}, "path ."}, {tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "LIVE"}} {
			start := time.Now()
			tm.Send(step.key)
			waitFor(t, tm, 2*time.Second, step.want)
			worst = max(worst, time.Since(start))
		}
	}
	if worst > 500*time.Millisecond {
		t.Fatalf("worst key latency %s during the flood", worst)
	}
	_ = tm.Quit()
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	logs := fm.Detail().Logs()
	if logs.Len() != 2000 {
		t.Fatalf("log buffer holds %d lines, want the 2000 cap", logs.Len())
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("flood %s: worst key latency %s, %d lines kept, %d dropped upstream, heap %s",
		dur, worst, logs.Len(), logs.Dropped(), metrics.Bytes(ms.HeapAlloc))
}

// Stage 4 acceptance: the daemon dies in the middle of an operation → the
// job fails with a clear message, the row leaves its pending state, and the
// UI keeps working (the other engine too).
func TestDaemonDiesMidAction(t *testing.T) {
	w := fake.NewWorld(nil)
	fake.Homelab(w)
	w.Docker.SetActionDelay(time.Second)
	tm, _ := programWithStreams(t, w, supervisor.Options{})
	tm.Type("3")
	waitFor(t, tm, 5*time.Second, "redis-cache")
	tm.Type("/")
	tm.Type("redis")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.Type("x")
	waitFor(t, tm, 5*time.Second, "stopping")
	w.Docker.Disconnect()
	waitFor(t, tm, 5*time.Second, "failed")
	tm.Type("5")
	waitFor(t, tm, 5*time.Second, "stop redis-cache")
	w.Docker.Recover()

	_ = tm.Quit()
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	js := fm.Snapshot().Jobs()
	if len(js) != 1 || js[0].State != store.JobFailed || js[0].Err == "" {
		t.Fatalf("jobs %+v", js)
	}
	if fm.store.HasPending() {
		t.Fatal("the failed stop left a pending row")
	}
}

// A real action through the program: the row shows its pending state, the
// engine's event confirms it and the Jobs view lists it.
func TestStopThroughTheProgram(t *testing.T) {
	w := fake.NewWorld(nil)
	fake.Homelab(w)
	w.Docker.SetActionDelay(300 * time.Millisecond)
	tm, _ := programWithStreams(t, w, supervisor.Options{})
	tm.Type("3")
	waitFor(t, tm, 5*time.Second, "redis-cache")
	tm.Type("/")
	tm.Type("redis")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.Type("x")
	waitFor(t, tm, 5*time.Second, "stopping")
	waitFor(t, tm, 5*time.Second, "stopped")
	if in, _ := w.Docker.Instance("redis-cache"); in.State != engine.StateStopped {
		t.Fatalf("redis-cache is %v", in.State)
	}
	_ = tm.Quit()
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	if fm.store.HasPending() { // "stopped" replaces "stopping" only once confirmed
		t.Fatal("the stop was never confirmed")
	}
}

// Stage 4b: a create through the program pulls with progress, the new
// container appears in the Table and a toast names it.
func TestCreateThroughTheProgram(t *testing.T) {
	w := fake.NewWorld(nil)
	fake.Homelab(w)
	w.Docker.SetActionDelay(2 * time.Second)
	tm, _ := programWithStreams(t, w, supervisor.Options{}, func(o *Options) { o.Images = fakeImages{w} })
	tm.Type("3")
	waitFor(t, tm, 5*time.Second, "web-api")
	tm.Type("n")
	waitFor(t, tm, 5*time.Second, "Create on")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, 5*time.Second, "Create a Docker container", "local images")
	tm.Type("caddy:2")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab})
	tm.Send(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}) // pull
	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab})
	tm.Type("new-web")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, 5*time.Second, "Creating new-web")
	tm.Type("5")
	waitFor(t, tm, 5*time.Second, "create new-web", "pulling")
	waitFor(t, tm, 10*time.Second, "Created new-web")
	tm.Type("3")
	waitFor(t, tm, 5*time.Second, "new-web")
	in, ok := w.Docker.Instance("new-web")
	if !ok || in.State != engine.StateRunning || in.Image != "caddy:2" {
		t.Fatalf("new-web = %+v", in)
	}
}

// Stage 4b: the daemon dies in the middle of a create → the job fails with
// a clear message, nothing half-made is shown, and the UI keeps working.
func TestDaemonDiesMidCreate(t *testing.T) {
	w := fake.NewWorld(nil)
	fake.Homelab(w)
	w.Docker.SetActionDelay(4 * time.Second)
	tm, _ := programWithStreams(t, w, supervisor.Options{})
	tm.Type("3")
	waitFor(t, tm, 5*time.Second, "web-api", "dev-arch")
	tm.Type("n")
	waitFor(t, tm, 5*time.Second, "Create on")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, 5*time.Second, "Create a Docker container")
	tm.Type("caddy:2")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab})
	tm.Send(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyTab})
	tm.Type("half-made")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, 5*time.Second, "Creating half-made")
	tm.Type("5")
	waitFor(t, tm, 5*time.Second, "pulling")
	w.Docker.Disconnect()
	waitFor(t, tm, 10*time.Second, "failed")
	w.Docker.Recover()
	tm.Type("3")
	waitFor(t, tm, 5*time.Second, "Table")

	_ = tm.Quit()
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	js := fm.Snapshot().Jobs()
	if len(js) != 1 || js[0].State != store.JobFailed || js[0].Err == "" || js[0].Verb != "create" {
		t.Fatalf("jobs %+v", js)
	}
	if _, ok := w.Docker.Instance("half-made"); ok {
		t.Fatal("a failed create left an instance")
	}
}
