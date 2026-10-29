package app

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/views/table"
)

// fixedNow pins the render clock for engine tests and golden frames.
var fixedNow = time.Date(2026, 10, 29, 14, 3, 30, 0, time.UTC)

// stubSource records the user's connection requests. Its change channel is
// closed, so the pump returns at once when the driver runs it.
type stubSource struct {
	ch         chan store.Change
	reconnects int
	projects   []string
}

func newStubSource() *stubSource {
	s := &stubSource{ch: make(chan store.Change)}
	close(s.ch)
	return s
}

func (s *stubSource) Changes() <-chan store.Change { return s.ch }
func (s *stubSource) Reconnect()                   { s.reconnects++ }
func (s *stubSource) SetProject(p string)          { s.projects = append(s.projects, p) }

// homelab returns the changes a supervisor would emit for the homelab
// scenario once both engines are connected.
func homelab(t testing.TB, mutate func(*fake.World)) []store.Change {
	t.Helper()
	w := fake.NewWorld(clock.NewFake(fixedNow))
	fake.Homelab(w)
	if mutate != nil {
		mutate(w)
	}
	ctx := context.Background()
	var out []store.Change
	for _, e := range w.Engines() {
		info, err := e.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		list, _ := e.ListInstances(ctx, engine.ListOptions{})
		nets, _ := e.Networks(ctx, engine.ListOptions{})
		vols, _ := e.Volumes(ctx, engine.ListOptions{})
		out = append(out,
			store.Synced{Engine: e.ID(), At: fixedNow.Add(-20 * time.Second), Info: info, Instances: list, Networks: nets, Volumes: vols},
			store.StatusChanged{Engine: e.ID(), Health: store.Health{State: store.StateConnected, Since: fixedNow}},
		)
	}
	return out
}

func withEngines(src *stubSource) func(*Options) {
	return func(o *Options) {
		o.Now = func() time.Time { return fixedNow }
		o.Tick = func(time.Duration) tea.Cmd { return nil }
		if src != nil {
			o.Source = src
		}
	}
}

func (d *driver) screen() string { return ansi.Strip(d.m.Render()) }

func (d *driver) table() *table.View { return d.m.ActiveViewModel().(*table.View) }

func TestEngineDataInTableAndHeader(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("3")
	out := d.screen()
	for _, want := range []string{"Docker 29.0.1", "Incus 6.0.5", "web-api", "● running", "▲ unhealthy", "■ exited 1", "⏸ paused", "dev-arch [lab]", "15 of 15 shown", "2d"} {
		if !strings.Contains(out, want) {
			t.Errorf("screen lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Coming in Stage 1") {
		t.Error("table placeholder still shown")
	}
}

func TestTableSelectionSurvivesRefresh(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("3", "down", "down", "down")
	sel, ok := d.table().Selected()
	if !ok {
		t.Fatal("nothing selected")
	}
	// A new instance sorting before the cursor, and the old ones refreshed.
	d.send(ChangesMsg{Changes: homelab(t, func(w *fake.World) {
		w.Docker.Add(engine.Instance{Name: "aaa-first", Image: "busybox", State: engine.StateRunning})
	})})
	if got, _ := d.table().Selected(); got.Key() != sel.Key() {
		t.Fatalf("cursor moved from %s to %s", sel.Name, got.Name)
	}
	// The selected instance disappears: the cursor stays in place.
	d.send(ChangesMsg{Changes: []store.Change{store.InstanceRemoved{Engine: sel.Engine, ID: sel.ID}}})
	if got, ok := d.table().Selected(); !ok || got.Key() == sel.Key() {
		t.Fatalf("selection after removal: %v %v", got.Name, ok)
	}
}

func TestFilterPromptCapturesReservedKeys(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("3", "/", "w", "e", "b", "q", "1", "backspace", "backspace")
	if d.quitRequested() || d.m.ActiveView() != 3 {
		t.Fatal("the filter prompt must capture q and 1")
	}
	if f := d.table().Filter(); f != "web" {
		t.Fatalf("filter %q", f)
	}
	for _, in := range d.table().Rows() {
		if !strings.Contains(in.Name, "w") {
			t.Fatalf("row %s does not match", in.Name)
		}
	}
	if !strings.Contains(d.screen(), "keep filter") {
		t.Error("status bar must show the input hints while typing")
	}
	d.press("enter")
	if d.table().Inputting() || d.table().Filter() != "web" {
		t.Fatal("enter keeps the filter and leaves the prompt")
	}
	d.press("4", "3") // keys work again
	if d.m.ActiveView() != 3 {
		t.Fatal("view keys after the prompt closed")
	}
	d.press("esc")
	if d.table().Filter() != "" {
		t.Fatal("esc clears the filter")
	}
	// The engine filter and the fuzzy match.
	d.press("e")
	for _, in := range d.table().Rows() {
		if in.Engine != engine.Docker {
			t.Fatalf("engine filter let %s through", in.Name)
		}
	}
	if d.saved[len(d.saved)-1].Table.Engine != "docker" {
		t.Fatalf("engine filter not persisted: %+v", d.saved)
	}
	d.press("e", "e", "/", "w", "p", "i", "enter")
	if rows := d.table().Rows(); len(rows) != 1 || rows[0].Name != "web-api" {
		t.Fatalf("fuzzy wpi: %v", rows)
	}
}

func TestSortPersists(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("3", "o", "o", "o", "O")
	col, rev := d.table().SortColumn()
	if col != table.ColState || !rev {
		t.Fatalf("sort %v %v", col, rev)
	}
	last := d.saved[len(d.saved)-1].Table
	if last.Sort != "state" || !last.Reverse {
		t.Fatalf("persisted %+v", last)
	}
	rows := d.table().Rows()
	if rows[len(rows)-1].State != engine.StateRunning {
		t.Fatalf("reverse state sort ends with %v", rows[len(rows)-1].State)
	}
}

func TestDegradedBannerCountdownAndRecoveryToast(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.send(ChangesMsg{Changes: []store.Change{store.StatusChanged{Engine: engine.Incus, Health: store.Health{
		State: store.StateDegraded, Since: fixedNow, RetryAt: fixedNow.Add(4 * time.Second), Err: "connection lost",
	}}}})
	d.press("3")
	out := d.screen()
	for _, want := range []string{"Incus: connection lost — showing data from 14:03:10 · retry in 4s · ctrl+r now", "Incus reconnecting 4s", "incus stale"} {
		if !strings.Contains(out, want) {
			t.Errorf("screen lacks %q:\n%s", want, out)
		}
	}
	d.send(ClockTickMsg(fixedNow.Add(3 * time.Second)))
	if !strings.Contains(d.screen(), "retry in 1s") {
		t.Error("countdown must follow the render clock")
	}
	d.send(ChangesMsg{Changes: []store.Change{store.StatusChanged{Engine: engine.Incus, Health: store.Health{State: store.StateConnected}}}})
	if !strings.Contains(d.lastToast(), "Incus reconnected") || strings.Contains(d.screen(), "stale") {
		t.Fatalf("recovery: toast %q", d.lastToast())
	}
}

func TestUnavailableCardWithHint(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: append(homelab(t, nil), store.StatusChanged{Engine: engine.Docker, Health: store.Health{
		State: store.StateUnavailable, RetryAt: fixedNow.Add(52 * time.Second), Err: "permission denied", Class: engine.ClassPermission,
		Hint: "Permission denied on /var/run/docker.sock. Join the docker group.",
	}})})
	d.press("3")
	out := d.screen()
	for _, want := range []string{"✖ Docker unavailable — permission denied", "Join the docker group", "retrying in 52s", "Docker unavailable"} {
		if !strings.Contains(out, want) {
			t.Errorf("screen lacks %q:\n%s", want, out)
		}
	}
}

func TestProjectPickerAndReconnect(t *testing.T) {
	src := newStubSource()
	d := newDriver(t, withEngines(src))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("P")
	if d.m.ModalDepth() != 1 || !strings.Contains(d.screen(), "Incus project") {
		t.Fatalf("P must open the project picker:\n%s", d.screen())
	}
	d.press("down", "down", "enter")
	if len(src.projects) != 1 || src.projects[0] != "lab" || d.m.State().IncusProject != "lab" {
		t.Fatalf("projects %v state %+v", src.projects, d.m.State())
	}
	if d.saved[len(d.saved)-1].IncusProject != "lab" {
		t.Fatal("project not persisted")
	}
	d.press("ctrl+r")
	if src.reconnects != 1 {
		t.Fatalf("reconnects %d", src.reconnects)
	}
}

func TestInstanceActionsNameTheSelection(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, func(w *fake.World) {
		w.Incus.Configure(func(i *engine.Info) {
			i.Capabilities.Restricted, i.Capabilities.Reason = true, "restricted incus-user socket"
		})
	})})
	d.press("3")
	first, _ := d.table().Selected()
	d.press("s")
	if want := "start " + first.Name + ": lifecycle actions arrive in Stage 4"; d.lastToast() != want {
		t.Fatalf("toast %q, want %q", d.lastToast(), want)
	}
	d.press("enter")
	if !strings.Contains(d.lastToast(), "Detail view for "+first.Name) {
		t.Fatalf("toast %q", d.lastToast())
	}
	d.press("e", "e") // incus only
	d.press("x")
	if !strings.Contains(d.lastToast(), "restricted incus-user socket") {
		t.Fatalf("restricted engine: toast %q", d.lastToast())
	}
	if !strings.Contains(d.screen(), "6.0.5 (restricted)") {
		t.Error("header must flag the restricted connection")
	}
}

func TestMarkRows(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("3", "space", "space")
	if n := len(d.table().Marked()); n != 2 {
		t.Fatalf("marked %d", n)
	}
	if !strings.Contains(d.screen(), "2 marked") {
		t.Error("footer must count marks")
	}
}
