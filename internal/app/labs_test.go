package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	machinesview "github.com/blokxz/viewDI/internal/ui/views/machines"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/ui/uitest"
	treeview "github.com/blokxz/viewDI/internal/ui/views/tree"
)

// withLabs starts on the Tree view in the lab grouping, with labs saved
// into *saved.
func withLabs(ls config.Labs, saved *[]config.Labs) func(*Options) {
	return func(o *Options) {
		o.Labs = ls
		o.State.View = config.ViewMachines
		o.State.Tree.Grouping = "lab"
		o.SaveLabs = func(ls config.Labs) error {
			*saved = append(*saved, ls)
			return nil
		}
	}
}

func lastLabs(t *testing.T, saved []config.Labs) config.Labs {
	t.Helper()
	if len(saved) == 0 {
		t.Fatal("labs never saved")
	}
	return saved[len(saved)-1]
}

// Labs only edit labs.json: --read-only, which blocks every daemon
// mutation, lets the whole lab menu through.
func TestLabsWorkReadOnly(t *testing.T) {
	var saved []config.Labs
	d := newDriver(t, withEngines(nil), withLabs(config.Labs{}, &saved), func(o *Options) { o.ReadOnly = true })
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	if d.tree().Grouping() != treeview.ByLab {
		t.Fatalf("grouping %v", d.tree().Grouping())
	}
	d.press("right", "right") // to "(not in a lab)", then unfold it
	d.treeTo("web-api")
	d.press("L")
	if d.m.ModalDepth() != 1 || !strings.Contains(d.screen(), "New lab with web-api") {
		t.Fatalf("no lab menu:\n%s", d.screen())
	}
	d.press("enter")
	d.typeText("edge")
	d.press("enter")
	ls := lastLabs(t, saved)
	if len(ls.Labs) != 1 || ls.Labs[0].Name != "edge" || !ls.Labs[0].HasMember(config.LabMember{Engine: "docker", Name: "web-api"}) {
		t.Fatalf("saved %+v", ls)
	}
	if !strings.Contains(d.screen(), "◈ lab edge") {
		t.Fatalf("no lab in the tree:\n%s", d.screen())
	}

	// Delete goes through the confirm modal.
	d.press("L")
	if !strings.Contains(d.screen(), "Delete lab edge") {
		t.Fatalf("cursor not on the new lab:\n%s", d.screen())
	}
	d.press("up", "enter") // the last entry
	if d.m.ModalDepth() != 1 || !strings.Contains(d.screen(), "Delete lab edge?") {
		t.Fatalf("no confirm:\n%s", d.screen())
	}
	d.press("y")
	if ls := lastLabs(t, saved); len(ls.Labs) != 0 {
		t.Fatalf("lab not deleted: %+v", ls)
	}
}

func TestLabsSaveFailureIsReported(t *testing.T) {
	var saved []config.Labs
	d := newDriver(t, withEngines(nil), withLabs(config.Labs{}, &saved))
	d.send(LabsSavedMsg{Err: errors.New("disk full")})
	if !strings.Contains(d.lastToast(), "Cannot save labs: disk full") {
		t.Fatalf("toast %q", d.lastToast())
	}
}

// The link animation ticks only while the Tree view is on screen (active,
// no detail, terminal focused), in the lab grouping, with an active link.
func TestLabAnimationTicksOnlyWhileVisible(t *testing.T) {
	var ls config.Labs
	id, _ := ls.Add("web")
	_, _ = ls.Link(id, config.LabMember{Engine: "docker", Name: "web-api"}, config.LabMember{Engine: "docker", Name: "nginx-proxy"})
	var saved []config.Labs
	d := newDriver(t, withEngines(nil), withLabs(ls, &saved), func(o *Options) {
		o.State.View = config.ViewHome
		o.Animation = 125 * time.Millisecond
	})
	// The tick is a timer: these updates drop their commands.
	send := func(msgs ...tea.Msg) {
		for _, msg := range msgs {
			model, _ := d.m.Update(msg)
			d.m = model.(Model)
		}
	}
	key := func(k string) tea.Msg { return uitest.Key(k) }
	tv := d.m.views[viewMachines].(*machinesview.View).Tree()
	send(ChangesMsg{Changes: homelab(t, nil)})
	steps := []struct {
		name string
		msg  tea.Msg
		want bool
	}{
		{"admin view", nil, false},
		{"tree view", key("2"), true},
		{"other view", key("1"), false},
		{"back", key("2"), true},
		{"terminal blurred", tea.BlurMsg{}, false},
		{"terminal focused", tea.FocusMsg{}, true},
		{"network grouping", key("g"), false},
	}
	for _, st := range steps {
		if st.msg != nil {
			send(st.msg)
		}
		if got := tv.Animating(); got != st.want {
			t.Fatalf("%s: animating %v, want %v", st.name, got, st.want)
		}
	}
}
