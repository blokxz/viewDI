package picker

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
)

func TestPicker(t *testing.T) {
	items := []Item{{ID: "", Label: "all projects"}, {ID: "default", Label: "default"}, {ID: "lab", Label: "lab", Detail: "2 instances"}}
	m := New(keys.MustDefault(), "project", "Incus project", items, "default")
	if m.Cursor() != "default" {
		t.Fatalf("cursor %q", m.Cursor())
	}
	var mm modal.Modal = m
	mm, _ = mm.Update(uitest.Key("down"))
	mm, _ = mm.Update(uitest.Key("down")) // wraps
	if m.Cursor() != "" {
		t.Fatalf("cursor %q", m.Cursor())
	}
	mm, _ = mm.Update(uitest.Key("up"))
	_, cmd := mm.Update(uitest.Key("enter"))
	msgs := uitest.Collect(cmd)
	if len(msgs) != 2 || msgs[1] != (SelectedMsg{Kind: "project", ID: "lab"}) {
		t.Fatalf("msgs %v", msgs)
	}
	if _, ok := msgs[0].(modal.CloseMsg); !ok {
		t.Fatal("enter must close the picker first")
	}
	_, cmd = mm.Update(uitest.Key("esc"))
	if msgs := uitest.Collect(cmd); len(msgs) != 1 {
		t.Fatalf("esc: %v", msgs)
	}
	out := ansi.Strip(m.View(theme.NewStyles(theme.Fallback(), 1), 80, 24))
	for _, want := range []string{"Incus project", "›lab", "2 instances"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
}
