package app

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/engine"
)

// menuDriver shows the machines list with enter opening the action menu.
func menuDriver(t *testing.T, jr *stubJobs, mutates ...func(*Options)) *driver {
	t.Helper()
	d := newDriver(t, append([]func(*Options){withEngines(nil), withJobs(jr), func(o *Options) { o.EnterDetails = false }}, mutates...)...)
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	return d
}

func TestEnterOpensTheMenuAndArrowTheDetails(t *testing.T) {
	jr := &stubJobs{}
	d := menuDriver(t, jr)
	d.selectRow("redis-cache")
	d.press("enter")
	s := d.screen()
	if d.m.ModalDepth() != 1 || d.m.Detail() != nil {
		t.Fatalf("enter did not open the menu:\n%s", s)
	}
	for _, want := range []string{"redis-cache", "Read the logs", "Restart", "Stop", "Details", "Delete…"} {
		if !strings.Contains(s, want) {
			t.Errorf("menu lacks %q:\n%s", want, s)
		}
	}
	d.press("esc", "right")
	if d.m.Detail() == nil {
		t.Fatal("→ must open the details in the list")
	}
}

// menuTo moves the highlight of the open picker to the line labelled label.
func (d *driver) menuTo(label string) {
	d.t.Helper()
	for range 30 {
		if l := lineWith(d.screen(), label); strings.Contains(l, "›") {
			return
		}
		d.press("down")
	}
	d.t.Fatalf("no %q in the menu:\n%s", label, d.screen())
}

func lineWith(screen, s string) string {
	for _, l := range strings.Split(screen, "\n") {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}

func TestMenuRunsTheSameCodeAsTheKeys(t *testing.T) {
	jr := &stubJobs{}
	d := menuDriver(t, jr)
	d.selectRow("redis-cache")
	d.press("enter")
	d.menuTo("Restart")
	d.press("enter")
	if got := jr.titles(); len(got) != 1 || got[0] != "restart redis-cache" {
		t.Fatalf("submitted %v", got)
	}
	// Delete still asks before anything happens.
	d.selectRow("redis-cache")
	d.press("enter")
	d.menuTo("Delete")
	d.press("enter")
	if !strings.Contains(d.screen(), "Delete redis-cache") || len(jr.reqs) != 1 {
		t.Fatalf("delete from the menu skipped its confirmation:\n%s", d.screen())
	}
}

func TestEnterDetailsKeepsTheOldBehaviour(t *testing.T) {
	d := menuDriver(t, &stubJobs{}, func(o *Options) { o.EnterDetails = true })
	d.selectRow("redis-cache")
	d.press("enter")
	if d.m.Detail() == nil || d.m.ModalDepth() != 0 {
		t.Fatal("[ui] enter = details must open the Detail view")
	}
}

func TestMenuInReadOnlyOffersOnlyWhatChangesNothing(t *testing.T) {
	d := menuDriver(t, &stubJobs{}, func(o *Options) { o.ReadOnly = true })
	d.selectRow("web-api")
	d.press("enter")
	s := d.screen()
	for _, gone := range []string{"Restart", "Stop", "Open a shell", "Delete", "Pause"} {
		if strings.Contains(s, gone) {
			t.Errorf("read-only menu offers %q:\n%s", gone, s)
		}
	}
	for _, want := range []string{"Read the logs", "Details"} {
		if !strings.Contains(s, want) {
			t.Errorf("read-only menu lacks %q:\n%s", want, s)
		}
	}
}

func TestShellArgv(t *testing.T) {
	id := strings.Repeat("ab12", 16)
	for _, tc := range []struct {
		name string
		in   engine.Instance
		want []string
		bad  string
	}{
		{
			"docker",
			engine.Instance{Engine: engine.Docker, ID: id, Name: "web", State: engine.StateRunning},
			[]string{"docker", "exec", "-it", id, "sh", "-c", shellScript},
			"",
		},
		{
			"incus",
			engine.Instance{Engine: engine.Incus, ID: "default/web-lab", Name: "web-lab", State: engine.StateRunning},
			[]string{"incus", "exec", "web-lab", "--", "sh", "-c", shellScript},
			"",
		},
		{
			"incus project",
			engine.Instance{Engine: engine.Incus, Name: "web-lab", Project: "lab", State: engine.StateRunning},
			[]string{"incus", "exec", "web-lab", "--project", "lab", "--", "sh", "-c", shellScript},
			"",
		},
		{"stopped", engine.Instance{Engine: engine.Docker, ID: id, State: engine.StateStopped}, nil, "not running"},
		{"docker id as an option", engine.Instance{Engine: engine.Docker, ID: "--privileged", State: engine.StateRunning}, nil, "not safe"},
		{"incus name with a space", engine.Instance{Engine: engine.Incus, Name: "a b", State: engine.StateRunning}, nil, "not safe"},
		{"incus name as an option", engine.Instance{Engine: engine.Incus, Name: "-x", State: engine.StateRunning}, nil, "not safe"},
		{"incus project injection", engine.Instance{Engine: engine.Incus, Name: "ok", Project: "p;rm", State: engine.StateRunning}, nil, "not safe"},
		{"escape in a name", engine.Instance{Engine: engine.Incus, Name: "a\x1b[2J", State: engine.StateRunning}, nil, "not safe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := shellArgv(tc.in)
			if tc.bad != "" {
				if err == nil || !strings.Contains(err.Error(), tc.bad) {
					t.Fatalf("err %v, want %q", err, tc.bad)
				}
				return
			}
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("argv %q err %v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestShellHandsTheTerminalOverWithTheExactArgv(t *testing.T) {
	var ran [][]string
	d := menuDriver(t, &stubJobs{}, func(o *Options) {
		o.RunProcess = func(argv []string, done func(error) tea.Msg) tea.Cmd {
			ran = append(ran, argv)
			return func() tea.Msg { return done(nil) }
		}
	})
	d.selectRow("redis-cache")
	d.press("E")
	if len(ran) != 1 || ran[0][0] != "docker" || ran[0][1] != "exec" || ran[0][2] != "-it" || ran[0][4] != "sh" {
		t.Fatalf("ran %q", ran)
	}
	if sel, _ := d.table().Selected(); ran[0][3] != sel.ID {
		t.Fatalf("the ID in the argv is %q, the selected machine's is %q", ran[0][3], sel.ID)
	}
	// The shell ends: a toast only when it failed.
	d.send(shellDoneMsg{name: "redis-cache", err: nil})
	d.send(shellDoneMsg{name: "redis-cache", err: context.Canceled})
	if !strings.Contains(d.lastToast(), "ended") {
		t.Fatalf("toast %q", d.lastToast())
	}
}

func TestShellRefusals(t *testing.T) {
	var ran [][]string
	run := func(o *Options) {
		o.RunProcess = func(argv []string, done func(error) tea.Msg) tea.Cmd { ran = append(ran, argv); return nil }
	}
	d := menuDriver(t, &stubJobs{}, run, func(o *Options) { o.ReadOnly = true })
	d.selectRow("redis-cache")
	d.press("E")
	if len(ran) != 0 || !strings.Contains(d.lastToast(), "Read-only") {
		t.Fatalf("read-only ran %q, toast %q", ran, d.lastToast())
	}
	d2 := menuDriver(t, &stubJobs{}, run)
	d2.selectRow("old-backup") // stopped
	d2.press("E")
	if len(ran) != 0 || !strings.Contains(d2.lastToast(), "not running") {
		t.Fatalf("stopped ran %q, toast %q", ran, d2.lastToast())
	}
	d3 := menuDriver(t, &stubJobs{}, run, func(o *Options) { o.Mock = "homelab" })
	d3.selectRow("redis-cache")
	d3.press("E")
	if len(ran) != 0 || !strings.Contains(d3.lastToast(), "--mock: would run docker exec") {
		t.Fatalf("mock ran %q, toast %q", ran, d3.lastToast())
	}
}

// --- the command palette ---

func TestPaletteRunsAVerbOnAMachine(t *testing.T) {
	jr := &stubJobs{}
	d := menuDriver(t, jr)
	d.press("ctrl+k")
	if d.m.ModalDepth() != 1 || !strings.Contains(d.screen(), "Command palette") {
		t.Fatalf("no palette:\n%s", d.screen())
	}
	d.typeText("restart redis")
	if !strings.Contains(d.screen(), "Restart · redis-cache") {
		t.Fatalf("no result:\n%s", d.screen())
	}
	d.press("enter")
	if got := jr.titles(); len(got) != 1 || got[0] != "restart redis-cache" {
		t.Fatalf("submitted %v", got)
	}
	if rec := d.m.state.Palette.Recent; len(rec) != 1 || !strings.HasPrefix(rec[0], "a:restart:") {
		t.Fatalf("recent %v", rec)
	}
	// The next time it is the first suggestion with an empty query.
	d.press("ctrl+k")
	if !strings.Contains(lineWith(d.screen(), "Restart · redis-cache"), "›") {
		t.Fatalf("recent item not first:\n%s", d.screen())
	}
}

func TestPaletteGoesToViewsAndMachines(t *testing.T) {
	d := menuDriver(t, &stubJobs{})
	d.press("ctrl+k")
	d.typeText("go to activity")
	d.press("enter")
	if d.m.ActiveView() != 4 {
		t.Fatalf("view %d", d.m.ActiveView())
	}
	d.press("ctrl+k")
	d.typeText("web-api")
	d.press("enter") // a machine opens its action menu
	if d.m.ModalDepth() != 1 || !strings.Contains(d.screen(), "web-api · Docker") {
		t.Fatalf("no menu:\n%s", d.screen())
	}
}

func TestPaletteInReadOnlyOffersNoChange(t *testing.T) {
	d := menuDriver(t, &stubJobs{}, func(o *Options) { o.ReadOnly = true })
	d.press("ctrl+k")
	d.typeText("restart")
	if s := d.screen(); strings.Contains(s, "Restart ·") {
		t.Fatalf("read-only palette offers restart:\n%s", s)
	}
	d.press("ctrl+u")
	d.typeText("create")
	if s := d.screen(); strings.Contains(s, "Create a container") {
		t.Fatalf("read-only palette offers create:\n%s", s)
	}
}

func TestPaletteEscClosesAndOtherKeysAreCaptured(t *testing.T) {
	d := menuDriver(t, &stubJobs{})
	d.press("ctrl+k", "q", "1")
	if d.quitRequested() || d.m.ModalDepth() != 1 {
		t.Fatal("the palette must capture q and 1")
	}
	d.press("esc")
	if d.m.ModalDepth() != 0 {
		t.Fatal("esc must close")
	}
}

// --- raise a limit ---

type limitsRecorder struct {
	fakeImages
	got []engine.Limits
	ids []string
}

func (l *limitsRecorder) UpdateLimits(_ context.Context, _ engine.ID, id string, lim engine.Limits) error {
	l.ids = append(l.ids, id)
	l.got = append(l.got, lim)
	return nil
}

func TestRaiseALimit(t *testing.T) {
	rec := &limitsRecorder{fakeImages: fakeImages{}}
	d := menuDriver(t, &stubJobs{}, func(o *Options) { o.Images = rec })
	d.send(ChangesMsg{Changes: homelabMetrics(t, 5)})
	d.selectRow("k3s-node1")
	d.press("m")
	s := d.screen()
	if !strings.Contains(s, "Raise a limit · k3s-node1") || !strings.Contains(s, "Memory") {
		t.Fatalf("no form:\n%s", s)
	}
	d.press("ctrl+u") // clear the suggestion
	d.typeText("2GiB")
	d.press("tab", "enter") // to CPUs (kept), submit
	if d.m.ModalDepth() == 0 || !strings.Contains(d.screen(), "Change the limits of k3s-node1?") {
		t.Fatalf("no confirmation:\n%s", d.screen())
	}
	if len(rec.got) != 0 {
		t.Fatal("changed before the answer")
	}
	d.press("y")
	if len(rec.got) != 1 || rec.got[0].MemoryBytes != 2<<30 {
		t.Fatalf("limits %+v", rec.got)
	}
}

func TestRaiseALimitRefusals(t *testing.T) {
	rec := &limitsRecorder{}
	d := menuDriver(t, &stubJobs{}, func(o *Options) { o.Images = rec; o.ReadOnly = true })
	d.selectRow("redis-cache")
	d.press("m")
	if !strings.Contains(d.lastToast(), "Read-only") || d.m.ModalDepth() != 0 {
		t.Fatalf("read-only: toast %q", d.lastToast())
	}
	d2 := menuDriver(t, &stubJobs{}, func(o *Options) { o.Images = rec })
	d2.selectRow("old-backup")
	d2.press("m")
	if !strings.Contains(d2.lastToast(), "not running") {
		t.Fatalf("stopped: toast %q", d2.lastToast())
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]uint64{"512MiB": 512 << 20, "2gib": 2 << 30, "1.5g": 3 << 29, "2 GB": 2 << 30, "64m": 64 << 20} {
		got, err := parseSize(in)
		if err != nil || got != want {
			t.Errorf("%q = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "-1g", "0", "1k", "NaN", "9999999t", "1e400g", "\x1b[2J"} {
		if _, err := parseSize(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if got, err := parseSize("1073741824"); err != nil || got != 1<<30 {
		t.Errorf("bytes: %d %v", got, err)
	}
	for _, bad := range []string{"0", "-2", "x", "NaN", "2000"} {
		if _, err := parseCPUs(bad); err == nil {
			t.Errorf("cpus %q accepted", bad)
		}
	}
	if formatSize(2<<30) != "2GiB" || formatSize(1536<<20) != "1536MiB" {
		t.Errorf("formatSize: %s %s", formatSize(2<<30), formatSize(1536<<20))
	}
}
