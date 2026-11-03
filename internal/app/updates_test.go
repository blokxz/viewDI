package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/supervisor"
	"github.com/blokxz/viewDI/internal/updates"
	"github.com/blokxz/viewDI/internal/updates/checker"
	"github.com/blokxz/viewDI/internal/updates/fakesrc"
)

// stubUpdater counts manual checks. Its report channel is closed, so the
// follow loop ends at once; tests send UpdatesMsg themselves.
type stubUpdater struct {
	mu          sync.Mutex
	checks      int
	imageChecks int
}

func (s *stubUpdater) Reports() <-chan updates.Report {
	ch := make(chan updates.Report)
	close(ch)
	return ch
}

func (s *stubUpdater) CheckNow() {
	s.mu.Lock()
	s.checks++
	s.mu.Unlock()
}

func (s *stubUpdater) CheckImagesNow() {
	s.mu.Lock()
	s.imageChecks++
	s.mu.Unlock()
}

func (s *stubUpdater) imageCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.imageChecks
}

func (s *stubUpdater) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checks
}

func withUpdater(u Updater) func(*Options) {
	return func(o *Options) { o.Updates = u }
}

// scenarioReports runs the real checker against a mock scenario's fake
// world and fake sources and returns its first n reports that are not
// "running" ones (a manual check between each).
func scenarioReports(t testing.TB, scenario string, n int) []updates.Report {
	t.Helper()
	clk := clock.NewFake(fixedNow)
	w := fake.NewWorld(clk)
	fake.Scenarios()[scenario].Build(w)
	src := fakesrc.ForScenario(scenario)
	c := checker.New(checker.Options{
		Config:   config.Default().Updates,
		Engines:  []engine.Engine{w.Docker, w.Incus},
		Packages: src, Upstream: src, Clock: clk,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	defer func() { cancel(); <-done }()

	var out []updates.Report
	for range 200 {
		select {
		case r := <-c.Reports():
			if r.Running {
				continue
			}
			if out = append(out, r); len(out) == n {
				return out
			}
			c.CheckNow()
		case <-time.After(20 * time.Millisecond):
			clk.Advance(checker.DefaultStartDelay)
		}
	}
	t.Fatalf("%s: got %d of %d reports", scenario, len(out), n)
	return nil
}

func homelabReport(t testing.TB) updates.Report { return scenarioReports(t, "homelab", 1)[0] }

func TestUpdatesOff(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("U")
	if got := d.lastToast(); !strings.Contains(got, "Update checks are off") {
		t.Fatalf("toast = %q", got)
	}
	d.press("5")
	if s := d.screen(); !strings.Contains(s, "viewDI makes no network calls") {
		t.Fatalf("view 6:\n%s", s)
	}
	if strings.Contains(d.screen(), "⬆") {
		t.Fatal("badge or marker with checks off")
	}
}

func TestUpdatesToastOnceAndMarkers(t *testing.T) {
	u := &stubUpdater{}
	d := newDriver(t, withEngines(nil), withUpdater(u))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	reps := scenarioReports(t, "homelab", 2)
	if len(reps[0].New) == 0 || len(reps[1].New) != 0 {
		t.Fatalf("new = %d then %d", len(reps[0].New), len(reps[1].New))
	}

	d.send(UpdatesMsg{Report: reps[0]})
	toast := d.lastToast()
	for _, want := range []string{"Docker 29.1.0", "Incus 6.0.6 (security)", "3 images", "6 for details"} {
		if !strings.Contains(toast, want) {
			t.Fatalf("toast %q lacks %q", toast, want)
		}
	}
	if n := d.m.store.Snapshot().Updates.Count(); n != 5 {
		t.Fatalf("badge count = %d", n)
	}
	d.pressTable()
	s := d.screen()
	if !strings.Contains(s, "⬆ 5") {
		t.Fatalf("no header badge:\n%s", s)
	}
	for _, name := range []string{"nginx-proxy", "web-api", "ci-runner"} {
		if !lineHas(s, name, "⬆") {
			t.Fatalf("no marker on %s:\n%s", name, s)
		}
	}
	if lineHas(s, "redis-cache", "⬆") {
		t.Fatal("marker on an up-to-date instance")
	}
	d.press("1")
	for _, want := range []string{"update available: Docker 29.1.0", "update available: Incus 6.0.6 (security)", "3 images have updates"} {
		if !strings.Contains(d.screen(), want) {
			t.Fatalf("admin lacks alert %q", want)
		}
	}

	// The next report repeats known updates: no new toast.
	d.press("U")
	if u.count() != 1 {
		t.Fatalf("CheckNow calls = %d", u.count())
	}
	before := len(d.m.Toasts())
	d.send(UpdatesMsg{Report: reps[1]})
	if len(d.m.Toasts()) != before {
		t.Fatalf("toast repeated: %q", d.lastToast())
	}
}

func TestUpdatesCacheReportDoesNotToast(t *testing.T) {
	d := newDriver(t, withEngines(nil), withUpdater(&stubUpdater{}))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	r := homelabReport(t)
	r.New, r.FromCache = nil, true
	d.send(UpdatesMsg{Report: r})
	if len(d.m.Toasts()) != 0 {
		t.Fatalf("toast from cache: %q", d.lastToast())
	}
	if n := d.m.store.Snapshot().Updates.Count(); n != 5 {
		t.Fatalf("count = %d", n)
	}
}

func TestUpdatesOfflineFailsOnce(t *testing.T) {
	d := newDriver(t, withEngines(nil), withUpdater(&stubUpdater{}))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	reps := scenarioReports(t, "offline", 2)
	for _, r := range reps {
		d.send(UpdatesMsg{Report: r})
	}
	if len(d.m.Toasts()) != 0 {
		t.Fatalf("failed check toasted: %q", d.lastToast())
	}
	d.press("5")
	s := d.screen()
	if strings.Count(s, "last check failed") != 1 {
		t.Fatalf("want one failure line:\n%s", s)
	}
	if strings.Contains(s, "⬆ ") {
		t.Fatalf("badge with nothing found:\n%s", s)
	}
}

func TestUpdatesSnoozeAndIgnore(t *testing.T) {
	d := newDriver(t, withEngines(nil), withUpdater(&stubUpdater{}))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.send(UpdatesMsg{Report: homelabReport(t)})
	d.press("5", "o") // sort by name: Docker first among engines
	count := func() int { return d.m.store.Snapshot().Updates.Count() }

	d.press("z")
	if count() != 4 || !strings.Contains(d.lastToast(), "Snoozed for 7 days") {
		t.Fatalf("snooze: count %d, toast %q", count(), d.lastToast())
	}
	if len(d.saved) == 0 || len(d.saved[len(d.saved)-1].Updates.Snoozed) != 1 {
		t.Fatalf("snooze not saved: %+v", d.saved)
	}
	d.press("z")
	if count() != 5 || !strings.Contains(d.lastToast(), "No longer snoozed") {
		t.Fatalf("unsnooze: count %d, toast %q", count(), d.lastToast())
	}

	d.press("I")
	if count() != 4 || !strings.Contains(d.lastToast(), "Ignoring this version") {
		t.Fatalf("ignore: count %d, toast %q", count(), d.lastToast())
	}
	// Snoozes expire; ignored versions stay hidden.
	d.send(ClockTickMsg(fixedNow.Add(8 * 24 * time.Hour)))
	if count() != 4 {
		t.Fatalf("ignored update came back: %d", count())
	}
	d.press("I")
	if count() != 5 || !strings.Contains(d.lastToast(), "No longer ignored") {
		t.Fatalf("unignore: count %d, toast %q", count(), d.lastToast())
	}
	if d.press("z"); count() != 4 {
		t.Fatalf("snooze again: %d", count())
	}
	d.send(ClockTickMsg(fixedNow.Add(16 * 24 * time.Hour)))
	if count() != 5 {
		t.Fatalf("snooze did not expire: %d", count())
	}
}

func TestUpdatesHiddenAreNotToasted(t *testing.T) {
	r := homelabReport(t)
	var dec updates.Decisions
	for _, e := range r.Engines {
		if e.HasUpdate() {
			dec = dec.Ignore(e.Key(), e.Available)
		}
	}
	d := newDriver(t, withEngines(nil), withUpdater(&stubUpdater{}), func(o *Options) { o.State.Updates = dec })
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.send(UpdatesMsg{Report: r})
	if got := d.lastToast(); strings.Contains(got, "Docker") || strings.Contains(got, "Incus") || !strings.Contains(got, "3 images") {
		t.Fatalf("toast = %q", got)
	}
}

func TestUpdatesOpenNotes(t *testing.T) {
	var opened []string
	open := func(url string) error { opened = append(opened, url); return nil }
	d := newDriver(t, withEngines(nil), withUpdater(&stubUpdater{}), func(o *Options) { o.OpenURL = open })
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.send(UpdatesMsg{Report: homelabReport(t)})
	d.press("5", "o", "enter")
	if len(opened) != 1 || !strings.HasPrefix(opened[0], "https://github.com/") {
		t.Fatalf("opened %q", opened)
	}

	// Without a browser, the URL is shown instead.
	d2 := newDriver(t, withEngines(nil), withUpdater(&stubUpdater{}), func(o *Options) {
		o.OpenURL = func(string) error { return errors.New(`exec: "xdg-open": executable file not found in $PATH`) }
	})
	d2.send(ChangesMsg{Changes: homelab(t, nil)})
	d2.send(UpdatesMsg{Report: homelabReport(t)})
	d2.press("5", "o", "enter")
	if got := d2.lastToast(); !strings.Contains(got, "https://github.com/") {
		t.Fatalf("toast = %q", got)
	}
}

// lineHas reports whether the screen line naming name also contains s.
func lineHas(screen, name, s string) bool {
	for line := range strings.SplitSeq(screen, "\n") {
		if strings.Contains(line, name) && strings.Contains(line, s) {
			return true
		}
	}
	return false
}

// Chaos (F-series): the registry hangs on every image check. The checker
// times each one out, the UI keeps answering keys meanwhile, and the
// Updates view reports the failure instead of waiting forever.
func TestUpdatesImageSourceHangs(t *testing.T) {
	w := fake.NewWorld(nil)
	fake.Homelab(w)
	w.Docker.HangImageChecks(true)
	w.Incus.HangImageChecks(true)
	src := fakesrc.ForScenario("homelab")
	c := checker.New(checker.Options{
		Config:   config.Default().Updates,
		Engines:  []engine.Engine{w.Docker, w.Incus},
		Packages: src, Upstream: src,
		StartDelay: time.Millisecond, ImageTimeout: 100 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done }) // runs after the program's cleanup

	tm, _ := programWithStreams(t, w, supervisor.Options{}, func(o *Options) { o.Updates = c })
	waitFor(t, tm, 5*time.Second, "6.0.5")
	for _, step := range []struct{ key, want string }{
		{"5", "engines checked"},
		{"3", "grouping: app"},
	} {
		start := time.Now()
		tm.Type(step.key)
		waitFor(t, tm, time.Second, step.want)
		if d := time.Since(start); d > 500*time.Millisecond {
			t.Fatalf("key %s took %s while image checks hang", step.key, d)
		}
	}
	// One frame paints both lines; the output reader consumes what it reads.
	tm.Type("5")
	waitFor(t, tm, 5*time.Second, "no image could be checked", "last check failed")
}

// stubNotifier records desktop notifications.
type stubNotifier struct {
	mu   sync.Mutex
	sent []string // "summary|body|urgent"
}

func (s *stubNotifier) Notify(_ context.Context, summary, body string, urgent bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, fmt.Sprintf("%s|%s|%v", summary, body, urgent))
	return nil
}

func (s *stubNotifier) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
}

func TestDesktopNotificationsOnlyForTheDiff(t *testing.T) {
	n := &stubNotifier{}
	d := newDriver(t, withEngines(nil), withUpdater(&stubUpdater{}), func(o *Options) {
		o.Notifier, o.NotifyMinLevel = n, updates.LevelMinor
	})
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	reps := scenarioReports(t, "homelab", 2)
	d.send(UpdatesMsg{Report: reps[0]})
	got := n.all()
	want := "viewDI: 2 updates available|Docker 29.1.0\nIncus 6.0.6 (security)\nPress 6 in viewDI for details.|true"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("notifications %q, want %q (images are patch level: below minor)", got, want)
	}
	d.send(UpdatesMsg{Report: reps[1]})
	if len(n.all()) != 1 {
		t.Fatalf("a repeated report notified: %q", n.all())
	}
}

func TestDesktopNotificationsRespectLevelAndDecisions(t *testing.T) {
	n := &stubNotifier{}
	d := newDriver(t, withEngines(nil), withUpdater(&stubUpdater{}), func(o *Options) {
		o.Notifier, o.NotifyMinLevel = n, updates.LevelSecurity
		o.State = config.State{Updates: updates.Decisions{}.Ignore("engine/incus", "6.0.6")}
	})
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.send(UpdatesMsg{Report: homelabReport(t)})
	if got := n.all(); len(got) != 0 {
		t.Fatalf("notified %q: Docker is below security and Incus 6.0.6 is ignored", got)
	}
}
