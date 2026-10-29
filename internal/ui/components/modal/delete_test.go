package modal

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
)

func press(t *testing.T, m Modal, ks ...string) (Modal, []tea.Msg) {
	t.Helper()
	var msgs []tea.Msg
	for _, k := range ks {
		var cmd tea.Cmd
		m, cmd = m.Update(uitest.Key(k))
		msgs = append(msgs, uitest.Collect(cmd)...)
	}
	return m, msgs
}

func confirmed(msgs []tea.Msg) (DeleteConfirmedMsg, bool) {
	for _, m := range msgs {
		if d, ok := m.(DeleteConfirmedMsg); ok {
			return d, true
		}
	}
	return DeleteConfirmedMsg{}, false
}

var (
	running = engine.Instance{Engine: engine.Docker, ID: "1", Name: "web", State: engine.StateRunning}
	stopped = engine.Instance{Engine: engine.Docker, ID: "2", Name: "old", State: engine.StateStopped}
	withVol = engine.Instance{
		Engine: engine.Incus, ID: "default/db", Name: "db", State: engine.StateStopped,
		Mounts: []engine.Mount{{Type: "disk", Name: "data"}},
	}
)

func TestNeedsTyping(t *testing.T) {
	if !NeedsTyping(running) || NeedsTyping(stopped) || !NeedsTyping(withVol) {
		t.Fatal("typing rules")
	}
}

func TestDeleteStoppedFlow(t *testing.T) {
	km := keys.MustDefault()
	d := NewDelete(km, []engine.Instance{stopped})
	if !d.Ready() {
		t.Fatal("a stopped container without volumes needs no typing")
	}
	// Focus starts on Cancel: enter cancels.
	_, msgs := press(t, d, "enter")
	if _, ok := confirmed(msgs); ok {
		t.Fatal("enter on the default focus confirmed")
	}
	d = NewDelete(km, []engine.Instance{stopped})
	_, msgs = press(t, d, "tab", "enter") // Delete button
	got, ok := confirmed(msgs)
	if !ok || got.Options != (engine.DeleteOptions{}) || len(got.Targets) != 1 {
		t.Fatalf("confirm %+v %v", got, ok)
	}
}

func TestDeleteTypedNameAndOptions(t *testing.T) {
	km := keys.MustDefault()
	d := NewDelete(km, []engine.Instance{running})
	m, msgs := press(t, d, "w", "e", "x", "enter")
	if _, ok := confirmed(msgs); ok || d.Ready() {
		t.Fatal("wrong name accepted")
	}
	m, _ = press(t, m, "backspace", "b")
	if !d.Ready() {
		t.Fatal("right name not accepted")
	}
	_, msgs = press(t, m, "tab", "space", "tab", "tab", "space", "space", "tab", "tab", "enter")
	got, ok := confirmed(msgs)
	if !ok || !got.Options.Force || got.Options.Volumes || got.Options.Stop {
		t.Fatalf("options %+v (confirmed %v)", got.Options, ok)
	}
}

func TestDeleteMixedEnginesNeedsCount(t *testing.T) {
	d := NewDelete(keys.MustDefault(), []engine.Instance{running, withVol})
	view := ansi.Strip(d.View(theme.NewStyles(theme.Fallback(), 1), 100, 40))
	for _, want := range []string{
		"Delete 2 instances?", "Type the number of instances 2", "Force: kill running containers",
		"anonymous volumes", "Stop running Incus instances first", "Delete (type to enable)",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	m, _ := press(t, d, "2")
	_, msgs := press(t, m, "enter")
	if _, ok := confirmed(msgs); !ok {
		t.Fatal("enter in the filled field must confirm")
	}
}
