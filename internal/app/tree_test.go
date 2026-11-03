package app

import (
	"strings"
	"testing"

	machinesview "github.com/blokxz/viewDI/internal/ui/views/machines"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/jobs"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/store"
	treeview "github.com/blokxz/viewDI/internal/ui/views/tree"
)

func (d *driver) tree() *treeview.View {
	return d.m.ActiveViewModel().(*machinesview.View).Tree()
}

// treeTo moves the Tree view's cursor down until the inspector names want.
func (d *driver) treeTo(want string) {
	d.t.Helper()
	for range 60 {
		if in, ok := d.tree().Selected(); ok && in.Name == want {
			return
		}
		d.press("down")
	}
	d.t.Fatalf("no %q in the tree:\n%s", want, d.screen())
}

func TestTreeActionsAndDetail(t *testing.T) {
	jr := &stubJobs{}
	d := newDriver(t, withEngines(nil), withJobs(jr))
	d.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.pressTree()
	d.treeTo("nginx-proxy")
	d.press("right", "down") // a leaf: actions apply to its machine
	d.press("x")
	if got := jr.titles(); len(got) != 1 || got[0] != "stop nginx-proxy" {
		t.Fatalf("submitted %v", got)
	}
	d.send(JobsMsg{Jobs: []jobs.Job{jr.job(1, jobs.Running, "")}})
	if !strings.Contains(d.screen(), "nginx-proxy  … stopping") {
		t.Fatalf("no pending mark in the tree:\n%s", d.screen())
	}
	d.press("enter")
	if d.m.detail == nil || !strings.Contains(d.screen(), "nginx-proxy") {
		t.Fatalf("enter did not open the detail:\n%s", d.screen())
	}
	d.press("esc")
	if d.m.detail != nil || d.m.ActiveView() != 2 {
		t.Fatal("esc did not return to the tree")
	}
	d.treeTo("redis-cache")
	d.press("d")
	if d.m.ModalDepth() != 1 || !strings.Contains(d.screen(), "Delete") {
		t.Fatalf("delete did not open the dialog:\n%s", d.screen())
	}
}

func TestTreeActionsNeedAMachine(t *testing.T) {
	jr := &stubJobs{}
	d := newDriver(t, withEngines(nil), withJobs(jr))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.pressTree("down", "down", "x") // a network
	if len(jr.reqs) != 0 || d.m.ModalDepth() != 0 {
		t.Fatalf("an action on a network: %v", jr.titles())
	}
	d.press("enter")
	if d.m.detail != nil {
		t.Fatal("enter on a network opened a detail")
	}
}

func TestTreeStateIsSaved(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.pressTree("down", "down", "space", "g")
	last := d.saved[len(d.saved)-1].Tree
	if last.Grouping != "project" || len(last.Toggled["network"]) != 1 || !strings.HasPrefix(last.Toggled["network"][0], "docker/net:") {
		t.Fatalf("saved tree state %+v", last)
	}
	// A new session starts where this one left off.
	d2 := newDriver(t, withEngines(nil), func(o *Options) { o.State = config.State{Tree: last} })
	d2.send(ChangesMsg{Changes: homelab(t, nil)})
	d2.pressTree()
	if d2.tree().Grouping() != treeview.ByProject {
		t.Fatalf("grouping %v", d2.tree().Grouping())
	}
}

// The cursor and the expansion survive live changes and a reconnect.
func TestTreeSurvivesStateChangesAndReconnect(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.pressTree()
	d.treeTo("web-db")
	cursor := d.tree().Cursor()
	d.press("right") // open it
	d.send(ChangesMsg{Changes: homelab(t, func(w *fake.World) { w.Docker.SetState("web-db", engine.StateStopped) })})
	if d.tree().Cursor() != cursor || !strings.Contains(d.screen(), "web-db  stopped") {
		t.Fatalf("a state change moved the cursor to %q:\n%s", d.tree().Cursor(), d.screen())
	}
	d.send(ChangesMsg{Changes: []store.Change{store.StatusChanged{Engine: engine.Docker, Health: store.Health{State: store.StateUnavailable, Err: "connection refused", Since: fixedNow}}}})
	if d.tree().Cursor() != cursor || !strings.Contains(d.screen(), "✖ unavailable") {
		t.Fatalf("an outage moved the cursor to %q:\n%s", d.tree().Cursor(), d.screen())
	}
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.send(ChangesMsg{Changes: []store.Change{store.StatusChanged{Engine: engine.Docker, Health: store.Health{State: store.StateConnected, Since: fixedNow}}}})
	if d.tree().Cursor() != cursor || !strings.Contains(d.screen(), "/var/lib/postgresql/data") {
		t.Fatalf("after the reconnect: cursor %q, leaves shown:\n%s", d.tree().Cursor(), d.screen())
	}
}
