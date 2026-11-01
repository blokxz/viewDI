package tree

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/components/form"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/picker"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
	"github.com/blokxz/viewDI/internal/ui/views"
)

var (
	mK3s    = config.LabMember{Engine: "incus", Project: "lab", Name: "k3s"}
	mWebLab = config.LabMember{Engine: "incus", Project: "default", Name: "web-lab"}
	mAPI    = config.LabMember{Engine: "docker", Name: "web-api"}
	mGhost  = config.LabMember{Engine: "docker", Name: "ghost"}
	mOld    = config.LabMember{Engine: "docker", Name: "old"}

	// testLabs: "perimeter" with three running machines, a stopped one
	// and a missing one; "k8s" shares k3s with it.
	testLabs = config.Labs{Version: 1, Labs: []config.Lab{
		{ID: 1, Name: "perimeter", Members: []config.LabMember{mWebLab, mAPI, mK3s, mGhost, mOld}, Links: []config.LabLink{
			{A: mK3s, B: mWebLab}, {A: mWebLab, B: mAPI}, {A: mK3s, B: mAPI}, {A: mAPI, B: mGhost}, {A: mOld, B: mK3s},
		}},
		{ID: 2, Name: "k8s", Members: []config.LabMember{mK3s}},
	}}
)

// labView is a view in the lab grouping.
func labView(t *testing.T, opts Options) *View {
	t.Helper()
	if opts.Labs.Labs == nil {
		opts.Labs = testLabs
	}
	opts.State = config.TreeState{Grouping: "lab"}
	return newView(t, opts)
}

func TestLabGrouping(t *testing.T) {
	v := labView(t, Options{})
	want := []string{
		"viewDI",
		" ◈ lab perimeter  5 links · 3 active",
		"  ? ghost  missing",
		"  ● k3s  vm  [lab]",
		"  ■ old  stopped",
		"  ● web-api",
		"  ● web-lab",
		" ◈ lab k8s  1 machine",
		"  ● k3s  vm  [lab]",
		" ◇ (not in a lab)  ▸ 2",
	}
	if got := labels(v); !slices.Equal(got, want) {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// A missing member selects nothing; a member selects its machine.
	v.t().Select("lab:1/missing:docker//ghost")
	if _, ok := v.Selected(); ok {
		t.Fatal("a missing member selected a machine")
	}
	press(v, "down")
	if in, ok := v.Selected(); !ok || in.Name != "k3s" || v.breadcrumb() != "lab perimeter › k3s" {
		t.Fatalf("selected %q, breadcrumb %q", in.Name, v.breadcrumb())
	}
	// Grouping away keeps the machine.
	press(v, "g")
	if in, ok := v.Selected(); !ok || in.Name != "k3s" || v.Grouping() != ByNetwork {
		t.Fatalf("after g: %q", v.Cursor())
	}
}

func TestLanesNeverOverlap(t *testing.T) {
	// A lab where every machine links to every other.
	var ls config.Labs
	id, _ := ls.Add("mesh")
	ms := []config.LabMember{mK3s, mWebLab, mAPI, mGhost, mOld}
	for i := range ms {
		for j := i + 1; j < len(ms); j++ {
			ls.Link(id, ms[i], ms[j]) //nolint:errcheck // a fresh lab
		}
	}
	for _, labs := range []config.Labs{testLabs, ls} {
		v := labView(t, Options{Labs: labs})
		lanes, ncols := v.lanes()
		if len(lanes) == 0 || ncols == 0 {
			t.Fatal("no lanes")
		}
		for i, a := range lanes {
			for _, b := range lanes[i+1:] {
				if a.col == b.col && a.y1 <= b.y2 && b.y1 <= a.y2 {
					t.Fatalf("lanes %+v and %+v share a column", a, b)
				}
			}
		}
	}
}

func TestLaneStates(t *testing.T) {
	v := labView(t, Options{})
	v.t().Select("lab:1/incus/lab/k3s") // the cursor on k3s makes its lanes hot
	lanes, _ := v.lanes()
	active, hot := 0, 0
	for _, l := range lanes {
		if l.active {
			active++
		}
		if l.hot {
			hot++
		}
	}
	// k3s↔web-lab, web-lab↔web-api, k3s↔web-api run; web-api↔ghost and
	// old↔k3s do not. Three links touch k3s.
	if len(lanes) != 5 || active != 3 || hot != 3 {
		t.Fatalf("lanes %+v", lanes)
	}
	// Collapsing the lab hides its lanes.
	v.t().Select(labNodeID(1))
	press(v, "left")
	if lanes, _ := v.lanes(); len(lanes) != 0 {
		t.Fatalf("lanes of a folded lab %+v", lanes)
	}
}

func renderAt(v *View, w, h int) string {
	s := theme.NewStyles(theme.Fallback(), 1)
	v.Update(views.ResizeMsg{Width: w, Height: h})
	return v.Render(views.Context{Styles: s, Width: w, Height: h, Mode: layout.ModeFor(w, h+4), Now: now})
}

func TestLaneRendering(t *testing.T) {
	v := labView(t, Options{})
	out := renderAt(v, 120, 30)
	plain := ansi.Strip(out)
	for _, g := range []string{"╮", "╯", "│", "┬"} {
		if !strings.Contains(plain, g) {
			t.Fatalf("no %q in the lanes:\n%s", g, plain)
		}
	}
	if strings.Contains(plain, "●──") && strings.Contains(plain, "─●") {
		t.Fatal("dots drawn with the animation off")
	}
	// Narrow screens keep the lanes too, inside the tree panel.
	if plain := ansi.Strip(renderAt(v, 80, 24)); !strings.Contains(plain, "╮") {
		t.Fatalf("no lanes at 80 columns:\n%s", plain)
	}
	// ASCII icons draw ASCII lanes and an ASCII dot.
	va := labView(t, Options{ASCII: true, Animation: 100 * time.Millisecond})
	va.Update(frameMsg{gen: 0, at: now})
	plain = ansi.Strip(renderAt(va, 120, 30))
	for _, r := range plain {
		if r > 0x7e && r != '\n' {
			t.Fatalf("non-ASCII %q in:\n%s", r, plain)
		}
	}
}

func TestAnimationTicksOnlyWhenALaneIsActive(t *testing.T) {
	v := labView(t, Options{Animation: 100 * time.Millisecond})
	renderAt(v, 120, 30)
	if cmd := v.SetVisible(false); cmd != nil || v.Animating() {
		t.Fatal("ticking while hidden")
	}
	if cmd := v.SetVisible(true); cmd == nil || !v.Animating() {
		t.Fatal("no tick while visible with active lanes")
	}
	if cmd := v.SetVisible(true); cmd != nil {
		t.Fatal("a second tick chain")
	}
	gen := v.gen
	// Frames move the dots.
	dots := map[string]bool{}
	for i := range 6 {
		if cmd := v.onFrame(frameMsg{gen: gen, at: now.Add(time.Duration(i) * 100 * time.Millisecond)}); cmd == nil {
			t.Fatal("the chain stopped")
		}
		dots[dotCells(renderAt(v, 120, 30))] = true
	}
	if len(dots) < 3 {
		t.Fatalf("the dots did not move: %v", dots)
	}
	// A stale chain is dropped.
	if cmd := v.onFrame(frameMsg{gen: gen - 1, at: now}); cmd != nil {
		t.Fatal("a stale frame re-armed")
	}
	// Leaving the lab grouping stops it.
	press(v, "g")
	if cmd := v.onFrame(frameMsg{gen: gen, at: now}); cmd != nil || v.Animating() {
		t.Fatal("ticking outside the lab grouping")
	}
	// Every end stopped: no active lane, no tick.
	stopped := world()
	for i := range stopped {
		if sy, ok := stopped[i].(store.Synced); ok {
			for j := range sy.Instances {
				sy.Instances[j].State = engine.StateStopped
			}
		}
	}
	v2 := labView(t, Options{Animation: 100 * time.Millisecond})
	v2.Update(views.DataMsg{Snapshot: snapshotOf(stopped...)})
	if cmd := v2.SetVisible(true); cmd != nil {
		t.Fatal("ticking without an active lane")
	}
	// Animation off: never.
	v3 := labView(t, Options{})
	if cmd := v3.SetVisible(true); cmd != nil {
		t.Fatal("ticking with the animation off")
	}
}

// dotCells returns the positions of the dots on screen.
func dotCells(out string) string {
	var b strings.Builder
	for y, line := range strings.Split(ansi.Strip(out), "\n") {
		for x, r := range []rune(line) {
			if r == '●' {
				b.WriteString(string(rune('a'+y)) + string(rune('a'+x%26)))
			}
		}
	}
	return b.String()
}

func TestTierSpeeds(t *testing.T) {
	api, k3s := engine.MakeKey(engine.Docker, "id-web-api"), engine.MakeKey(engine.Incus, "lab/k3s")
	for _, c := range []struct {
		rate float64
		tier int
	}{{0, 0}, {20 << 10, 1}, {2 << 20, 2}} {
		v := labView(t, Options{})
		pts := map[engine.Key]metrics.Point{api: {Time: now, Has: engine.HasNet, NetRx: c.rate}}
		v.Update(views.DataMsg{Snapshot: snapshotOf(append(world(), store.MetricsSampled{At: now, Points: pts})...)})
		if active, tier := v.linkState(api, k3s); !active || tier != c.tier {
			t.Fatalf("rate %v: active %v tier %d, want %d", c.rate, active, tier, c.tier)
		}
	}
}

// openedModal returns the modal an OpenMsg among msgs pushes.
func openedModal(t *testing.T, msgs []tea.Msg) modal.Modal {
	t.Helper()
	for _, m := range msgs {
		if o, ok := m.(modal.OpenMsg); ok {
			return o.Modal
		}
	}
	t.Fatalf("no modal opened: %v", msgs)
	return nil
}

func menuText(t *testing.T, msgs []tea.Msg) string {
	t.Helper()
	m := openedModal(t, msgs)
	return ansi.Strip(m.View(theme.NewStyles(theme.Fallback(), 1), 120, 40))
}

// apply feeds a message to the view and returns the messages its command
// produces.
func apply(v *View, msg tea.Msg) []tea.Msg {
	_, cmd := v.Update(msg)
	return uitest.Collect(cmd)
}

func savedLabs(t *testing.T, msgs []tea.Msg) config.Labs {
	t.Helper()
	for _, m := range msgs {
		if c, ok := m.(LabsChangedMsg); ok {
			return c.Labs
		}
	}
	t.Fatalf("labs not saved: %v", msgs)
	return config.Labs{}
}

func toastText(msgs []tea.Msg) string {
	for _, m := range msgs {
		if s, ok := m.(toast.ShowMsg); ok {
			return s.Text
		}
	}
	return ""
}

func TestLabMenuFlows(t *testing.T) {
	v := newView(t, Options{}) // network grouping, no labs
	v.t().Select("docker/net:web_default/docker/id-web-db")
	text := menuText(t, press(v, "L"))
	if !strings.Contains(text, "New lab with web-db") || strings.Contains(text, "Link") || strings.Contains(text, "Delete") {
		t.Fatalf("menu without labs:\n%s", text)
	}
	if f, ok := openedModal(t, apply(v, picker.SelectedMsg{Kind: pickMenu, ID: menuNew})).(*form.Model); !ok || f.Kind() != formNew {
		t.Fatal("New lab did not open the form")
	}
	msgs := apply(v, form.SubmittedMsg{Kind: formNew, Values: form.Values{"name": " perimeter "}})
	ls := savedLabs(t, msgs)
	if len(ls.Labs) != 1 || ls.Labs[0].Name != "perimeter" || !ls.Labs[0].HasMember(config.LabMember{Engine: "docker", Name: "web-db"}) {
		t.Fatalf("labs %+v", ls)
	}
	if toastText(msgs) != "Lab perimeter created with web-db" {
		t.Fatalf("toast %q", toastText(msgs))
	}

	// web-db is in one lab: the menu offers links there, even outside the
	// lab grouping.
	text = menuText(t, press(v, "L"))
	if !strings.Contains(text, "Link web-db with") || !strings.Contains(text, "Remove web-db from perimeter") || strings.Contains(text, "Remove a link") {
		t.Fatalf("menu in a lab:\n%s", text)
	}
	text = menuText(t, apply(v, picker.SelectedMsg{Kind: pickMenu, ID: menuLink}))
	if !strings.Contains(text, "grafana") || !strings.Contains(text, "k3s") || strings.Contains(text, "  web-db  ") {
		t.Fatalf("link picker:\n%s", text)
	}
	msgs = apply(v, picker.SelectedMsg{Kind: pickLink, ID: mK3s.String()})
	ls = savedLabs(t, msgs)
	if l := ls.Labs[0]; len(l.Links) != 1 || !l.HasMember(mK3s) || toastText(msgs) != "Linked web-db ↔ k3s" {
		t.Fatalf("after link %+v %q", l, toastText(msgs))
	}
	if msgs := apply(v, picker.SelectedMsg{Kind: pickLink, ID: mK3s.String()}); !strings.Contains(toastText(msgs), "already linked") {
		t.Fatalf("a duplicate link: %v", msgs)
	}

	// Remove the link, then the machine.
	if text := menuText(t, press(v, "L")); !strings.Contains(text, "Remove a link") {
		t.Fatalf("menu:\n%s", text)
	}
	apply(v, picker.SelectedMsg{Kind: pickMenu, ID: menuUnlink})
	ls = savedLabs(t, apply(v, picker.SelectedMsg{Kind: pickUnlink, ID: mK3s.String()}))
	if len(ls.Labs[0].Links) != 0 {
		t.Fatalf("after unlink %+v", ls)
	}
	press(v, "L")
	ls = savedLabs(t, apply(v, picker.SelectedMsg{Kind: pickMenu, ID: menuRemove}))
	if ls.Labs[0].HasMember(config.LabMember{Engine: "docker", Name: "web-db"}) {
		t.Fatalf("after remove %+v", ls)
	}

	// Rename and delete from the lab node, in the lab grouping.
	press(v, "g", "g", "g", "g")
	if v.Grouping() != ByLab {
		t.Fatal(v.Grouping())
	}
	v.t().Select(labNodeID(1))
	text = menuText(t, press(v, "L"))
	if !strings.Contains(text, "Rename lab perimeter") || strings.Contains(text, "Link") {
		t.Fatalf("lab menu:\n%s", text)
	}
	f := openedModal(t, apply(v, picker.SelectedMsg{Kind: pickMenu, ID: menuRename})).(*form.Model)
	if f.Value("name") != "perimeter" {
		t.Fatalf("rename starts from %q", f.Value("name"))
	}
	ls = savedLabs(t, apply(v, form.SubmittedMsg{Kind: formRename, Context: 1, Values: form.Values{"name": "edge"}}))
	if ls.Labs[0].Name != "edge" || v.Cursor() != labNodeID(1) {
		t.Fatalf("after rename %+v, cursor %q", ls, v.Cursor())
	}
	press(v, "L")
	c := openedModal(t, apply(v, picker.SelectedMsg{Kind: pickMenu, ID: menuDelete}))
	if _, ok := c.(*modal.Confirm); !ok {
		t.Fatalf("delete opened %T", c)
	}
	ls = savedLabs(t, apply(v, labDeleteMsg{lab: 1}))
	if len(ls.Labs) != 0 {
		t.Fatalf("after delete %+v", ls)
	}
	// Other pickers' choices are not the view's.
	if msgs := apply(v, picker.SelectedMsg{Kind: "incus-project", ID: "x"}); len(msgs) != 0 {
		t.Fatalf("handled another picker: %v", msgs)
	}
}

func TestLabInspector(t *testing.T) {
	v := labView(t, Options{})
	v.t().Select(labNodeID(1))
	v.inspected = v.Cursor()
	out := ansi.Strip(renderAt(v, 120, 30))
	for _, want := range []string{"lab perimeter", "Machines 5", "2 incus · 3 docker", "Links    5  3 active", "? ghost · missing"} {
		if !strings.Contains(out, want) {
			t.Fatalf("no %q in:\n%s", want, out)
		}
	}
	v.t().Select("lab:1/docker/id-web-api")
	v.inspected = v.Cursor()
	out = ansi.Strip(renderAt(v, 120, 30))
	for _, want := range []string{"Labs     perimeter", "Links    ↔ ● web-lab", "↔ ? ghost · missing"} {
		if !strings.Contains(out, want) {
			t.Fatalf("no %q in:\n%s", want, out)
		}
	}
	v.t().Select("lab:1/missing:docker//ghost")
	v.inspected = v.Cursor()
	if out := ansi.Strip(renderAt(v, 120, 30)); !strings.Contains(out, "missing docker machine") {
		t.Fatalf("missing inspector:\n%s", out)
	}
}
