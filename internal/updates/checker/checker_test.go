package checker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/updates"
	"github.com/blokxz/viewDI/internal/updates/fakesrc"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var epoch = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

type rig struct {
	t      *testing.T
	clk    *clock.Fake
	world  *fake.World
	src    *fakesrc.Source
	c      *Checker
	cancel context.CancelFunc
	done   chan struct{}
}

func newRig(t *testing.T, scenario, cache string, edit func(*Options)) *rig {
	t.Helper()
	clk := clock.NewFake(epoch)
	w := fake.NewWorld(clk)
	fake.Scenarios()[scenario].Build(w)
	src := fakesrc.ForScenario(scenario)
	opts := Options{
		Config:   config.Default().Updates,
		Engines:  []engine.Engine{w.Docker, w.Incus},
		Packages: src, Upstream: src,
		CacheFile: cache, Clock: clk,
	}
	if edit != nil {
		edit(&opts)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &rig{t: t, clk: clk, world: w, src: src, c: New(opts), cancel: cancel, done: make(chan struct{})}
	go func() { defer close(r.done); r.c.Run(ctx) }()
	t.Cleanup(r.stop)
	return r
}

func (r *rig) stop() {
	r.cancel()
	<-r.done
}

// next returns the next report that is not a Running one, advancing the
// fake clock by step whenever the checker is idle (no report within a few
// real milliseconds), at most limit times.
func (r *rig) next(step time.Duration, limit int) updates.Report {
	r.t.Helper()
	for range limit {
		select {
		case rep := <-r.c.Reports():
			if rep.Running {
				continue
			}
			return rep
		case <-time.After(20 * time.Millisecond):
			r.clk.Advance(step)
		}
	}
	r.t.Fatal("no report")
	return updates.Report{}
}

func engineOf(rep updates.Report, id engine.ID) updates.EngineStatus {
	for _, e := range rep.Engines {
		if e.Engine == id {
			return e
		}
	}
	return updates.EngineStatus{}
}

func TestHomelabFindsUpdatesOnce(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "updates.json")
	r := newRig(t, "homelab", cache, nil)
	rep := r.next(10*time.Second, 50)
	if r.clk.Now().Before(epoch.Add(30 * time.Second)) {
		t.Fatalf("first check ran before the start delay: %v", r.clk.Now())
	}

	d := engineOf(rep, engine.Docker)
	if d.Installed != "29.0.1" || d.Available != "29.1.0" || d.Level != updates.LevelMinor || d.Source != "mock" || !strings.Contains(d.NotesURL, "docker-v29.1.0") || d.Hint == "" {
		t.Errorf("docker = %+v", d)
	}
	i := engineOf(rep, engine.Incus)
	if i.Available != "6.0.6" || i.Level != updates.LevelSecurity || i.Channel != "LTS" {
		t.Errorf("incus = %+v", i)
	}
	if len(rep.Images) != 3 {
		t.Fatalf("images = %+v", rep.Images)
	}
	for _, u := range rep.Images {
		if len(u.Instances) == 0 || len(u.Names) != len(u.Instances) || u.Available == "" {
			t.Errorf("image %+v", u)
		}
	}
	if rep.Failed != "" || len(rep.New) != 5 {
		t.Fatalf("failed=%q new=%+v", rep.Failed, rep.New)
	}

	// A manual check finds the same updates: nothing is new.
	r.c.CheckNow()
	rep = r.next(time.Second, 50)
	if len(rep.New) != 0 || len(rep.Images) != 3 {
		t.Fatalf("second run new=%+v images=%d", rep.New, len(rep.Images))
	}
	r.stop()

	// After a restart the cache is shown at once and nothing toasts again.
	r2 := newRig(t, "homelab", cache, nil)
	first := <-r2.c.Reports()
	if !first.FromCache || len(first.Images) != 3 || first.New != nil {
		t.Fatalf("startup report = %+v", first)
	}
	rep = r2.next(10*time.Second, 50)
	if len(rep.New) != 0 {
		t.Fatalf("re-toast after restart: %+v", rep.New)
	}
}

func TestNewVersionIsNewAgain(t *testing.T) {
	r := newRig(t, "homelab", "", nil)
	r.next(10*time.Second, 50)
	r.src.SetPackage(engine.Docker, updates.Package{Manager: "mock", Installed: "29.0.1", Candidate: "29.2.0"})
	r.c.CheckNow()
	rep := r.next(time.Second, 50)
	if len(rep.New) != 1 || rep.New[0].Key != "engine/docker" || rep.New[0].Version != "29.2.0" {
		t.Fatalf("new = %+v", rep.New)
	}
}

func TestOfflineFailsOnceAndKeepsScheduling(t *testing.T) {
	r := newRig(t, "offline", "", nil)
	rep := r.next(10*time.Second, 50)
	if rep.Failed == "" || len(rep.New) != 0 || len(rep.Images) != 0 {
		t.Fatalf("offline report = %+v", rep)
	}
	if e := engineOf(rep, engine.Incus); e.Err == "" {
		t.Errorf("incus has no error: %+v", e)
	}
	calls := r.src.Calls()
	// The next image check runs on schedule (6 h), engines later (24 h).
	rep = r.next(time.Hour, 10)
	if rep.Failed == "" || r.src.Calls() != calls {
		t.Fatalf("image run: failed=%q calls %d→%d", rep.Failed, calls, r.src.Calls())
	}
	// The network comes back: the next engine check finds the updates.
	r.src.Fail(nil)
	*r.src = *fakesrc.ForScenario("homelab")
	r.world.Docker.FailImageChecks(nil)
	r.world.Incus.FailImageChecks(nil)
	r.c.CheckNow()
	rep = r.next(time.Second, 50)
	if rep.Failed != "" || len(rep.New) != 5 {
		t.Fatalf("recovered: failed=%q new=%d", rep.Failed, len(rep.New))
	}
}

func TestHangingImageSourceTimesOut(t *testing.T) {
	r := newRig(t, "chaos", "", nil)
	rep := r.next(10*time.Second, 100)
	if rep.ImageErrors == 0 || rep.ImageErrors != rep.ImagesChecked || !strings.Contains(rep.Failed, "image checks failed") {
		t.Fatalf("report = %+v", rep)
	}
	if engineOf(rep, engine.Docker).Available == "" {
		t.Fatal("engine results lost to the hanging image source")
	}
}

func TestFailedCheckKeepsLastResults(t *testing.T) {
	r := newRig(t, "homelab", "", nil)
	r.next(10*time.Second, 50)
	r.src.Fail(context.DeadlineExceeded)
	r.world.Docker.FailImageChecks(context.DeadlineExceeded)
	r.c.CheckNow()
	rep := r.next(time.Second, 50)
	if rep.Failed == "" || len(rep.Images) != 3 || engineOf(rep, engine.Incus).Available != "6.0.6" {
		t.Fatalf("report = %+v", rep)
	}
}

func TestIgnoreImages(t *testing.T) {
	r := newRig(t, "homelab", "", func(o *Options) {
		o.Config.IgnoreImages = []string{"debian/*", "nginx:*"}
	})
	rep := r.next(10*time.Second, 50)
	if len(rep.Images) != 1 {
		t.Fatalf("images = %+v", rep.Images)
	}
}

func TestNoStartupCheck(t *testing.T) {
	r := newRig(t, "homelab", "", func(o *Options) { o.Config.CheckOnStartup = false })
	start := r.clk.Now()
	r.next(time.Hour, 10)
	if got := r.clk.Now().Sub(start); got < 6*time.Hour {
		t.Fatalf("first check after %v, want the image interval", got)
	}
}

func TestCorruptCacheDropped(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "updates.json")
	if err := os.WriteFile(cache, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newRig(t, "homelab", cache, nil)
	rep := r.next(10*time.Second, 50)
	if rep.FromCache || len(rep.New) != 5 {
		t.Fatalf("report = %+v", rep)
	}
	b, err := os.ReadFile(cache)
	if err != nil || !strings.Contains(string(b), `"version": 1`) {
		t.Fatalf("cache not rewritten: %s %v", b, err)
	}
}

// failing sources prove that a disabled checker makes no call at all.
type failing struct{ t *testing.T }

func (f failing) Lookup(context.Context, engine.ID) (updates.Package, error) {
	f.t.Error("package manager used while disabled")
	return updates.Package{}, nil
}

func (f failing) Releases(context.Context, engine.ID) ([]updates.Release, error) {
	f.t.Error("network used while disabled")
	return nil, nil
}

func TestDisabledDoesNothing(t *testing.T) {
	clk := clock.NewFake(epoch)
	cfg := config.Default().Updates
	cfg.Enabled = false
	c := New(Options{Config: cfg, Packages: failing{t}, Upstream: failing{t}, Clock: clk, CacheFile: filepath.Join(t.TempDir(), "u.json")})
	done := make(chan struct{})
	go func() { defer close(done); c.Run(context.Background()) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	c.CheckNow()
	clk.Advance(48 * time.Hour)
	select {
	case r := <-c.Reports():
		t.Fatalf("report while disabled: %+v", r)
	default:
	}
}

func TestFromReleases(t *testing.T) {
	rels := []updates.Release{
		{Version: "6.18", URL: "u618"},
		{Version: "6.0.7"},
		{Version: "6.0.6", Security: true},
		{Version: "6.0.5"},
	}
	cases := []struct {
		installed, override, want, channel string
		level                              updates.Level
	}{
		{"6.0.5", "", "6.0.7", "LTS", updates.LevelSecurity},
		{"6.0.6", "", "6.0.7", "LTS", updates.LevelPatch},
		{"6.17", "", "6.18", "feature", updates.LevelMinor},
		{"6.0.5", "stable", "6.18", "feature", updates.LevelSecurity},
		{"6.18", "", "", "feature", updates.LevelPatch},
	}
	for _, c := range cases {
		st := fromReleases(updates.EngineStatus{Engine: engine.Incus, Installed: c.installed}, rels, c.override)
		if st.Available != c.want || st.Channel != c.channel || (c.want != "" && st.Level != c.level) {
			t.Errorf("%s/%s: %+v", c.installed, c.override, st)
		}
	}
}

func TestUpstreamVersion(t *testing.T) {
	for in, want := range map[string]string{
		"5:29.1.2-1~debian.12~bookworm": "29.1.2",
		"1:6.18-1":                      "6.18",
		"6.0.6-1.1":                     "6.0.6",
		"weird":                         "weird",
	} {
		if got := upstreamVersion(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestAutoFallsBackToUpstream(t *testing.T) {
	r := newRig(t, "homelab", "", func(o *Options) { o.Packages = notManaged{} })
	rep := r.next(10*time.Second, 50)
	d := engineOf(rep, engine.Docker)
	if d.Source != "github" || d.Available != "29.1.0" || d.Hint != "" {
		t.Fatalf("docker = %+v", d)
	}
}

type notManaged struct{}

func (notManaged) Lookup(context.Context, engine.ID) (updates.Package, error) {
	return updates.Package{}, updates.ErrNotManaged
}

func imageOf(rep updates.Report, ref string) (updates.ImageUpdate, bool) {
	for _, u := range rep.Images {
		if u.Check.Ref == ref {
			return u, true
		}
	}
	return updates.ImageUpdate{}, false
}

func TestPulledThenRecreated(t *testing.T) {
	r := newRig(t, "homelab", "", nil)
	first := r.next(10*time.Second, 50)
	before, ok := imageOf(first, "nginx:1.27")
	if !ok || before.Pulled {
		t.Fatalf("nginx before the pull: %+v (%v)", before, ok)
	}
	ctx := context.Background()
	if _, err := r.world.Docker.PullImage(ctx, before.Check, nil); err != nil {
		t.Fatal(err)
	}

	// An image-only check: the engines keep their time, the image is
	// pulled but its container still runs the old build. Nothing is new.
	r.clk.Advance(time.Minute)
	r.c.CheckImagesNow()
	rep := r.next(time.Second, 50)
	if !rep.EnginesAt.Equal(first.EnginesAt) || !rep.ImagesAt.After(first.ImagesAt) {
		t.Errorf("engines at %v → %v, images at %v → %v", first.EnginesAt, rep.EnginesAt, first.ImagesAt, rep.ImagesAt)
	}
	pulled, ok := imageOf(rep, "nginx:1.27")
	if !ok || !pulled.Pulled || pulled.Available != before.Available || len(pulled.Names) != 1 || pulled.Names[0] != "nginx-proxy" {
		t.Fatalf("nginx after the pull: %+v (%v)", pulled, ok)
	}
	if len(rep.New) != 0 {
		t.Errorf("a pull toasts again: %+v", rep.New)
	}

	px, _ := r.world.Docker.Instance("nginx-proxy")
	if _, err := r.world.Docker.Recreate(ctx, px.ID, engine.RecreateOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	r.c.CheckImagesNow()
	rep = r.next(time.Second, 50)
	if u, ok := imageOf(rep, "nginx:1.27"); ok {
		t.Errorf("nginx still listed after the recreate: %+v", u)
	}
	if len(rep.Images) != len(first.Images)-1 {
		t.Errorf("%d images, want %d", len(rep.Images), len(first.Images)-1)
	}
}

func TestCheckNowDuringImageCheckStillRuns(t *testing.T) {
	r := newRig(t, "homelab", "", nil)
	first := r.next(10*time.Second, 50)
	r.clk.Advance(time.Minute)
	r.c.CheckImagesNow()
	r.c.CheckNow()
	got := 0
	for got < 2 {
		rep := r.next(time.Second, 50)
		got++
		if rep.EnginesAt.After(first.EnginesAt) {
			return
		}
	}
	t.Fatal("a U pressed during an image-only check was dropped")
}

// TestCheckNowRightAfterAReportRuns is the regression test for a request
// lost in a race: the checker used to drop the requests made during a
// check after handing the report over, so a U pressed as soon as the
// report arrived could be dropped too (seen in CI as "no report").
func TestCheckNowRightAfterAReportRuns(t *testing.T) {
	r := newRig(t, "homelab", "", nil)
	r.next(10*time.Second, 50)
	for i := range 200 {
		r.c.CheckNow()
		for {
			select {
			case rep := <-r.c.Reports():
				if rep.Running {
					continue
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("request %d: no report", i)
			}
			break
		}
	}
}
