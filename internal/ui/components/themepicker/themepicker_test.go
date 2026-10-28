package themepicker

import (
	"testing"

	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
	"github.com/blokxz/viewDI/themes"
)

func TestPreviewSelectCancel(t *testing.T) {
	reg, _ := theme.LoadRegistry(themes.FS, "")
	all := reg.Themes()
	p := New(keys.MustDefault(), all, "nord")
	if p.Cursor() != "nord" {
		t.Fatalf("cursor = %s", p.Cursor())
	}

	_, cmd := p.Update(uitest.Key("down"))
	msgs := uitest.Collect(cmd)
	if len(msgs) != 1 || msgs[0].(PreviewMsg).ID == "nord" {
		t.Fatalf("down must preview the next theme: %v", msgs)
	}
	next := p.Cursor()

	_, cmd = p.Update(uitest.Key("esc"))
	msgs = uitest.Collect(cmd)
	if len(msgs) != 2 || msgs[0] != (modal.CloseMsg{}) || msgs[1] != (PreviewMsg{ID: "nord"}) {
		t.Fatalf("esc must close and restore the original: %v", msgs)
	}

	p = New(keys.MustDefault(), all, "nord")
	p.Update(uitest.Key("j"))
	_, cmd = p.Update(uitest.Key("enter"))
	msgs = uitest.Collect(cmd)
	if len(msgs) != 2 || msgs[1] != (SelectedMsg{ID: next}) {
		t.Fatalf("enter must select %s: %v", next, msgs)
	}
}

func TestWrapAround(t *testing.T) {
	reg, _ := theme.LoadRegistry(themes.FS, "")
	all := reg.Themes()
	p := New(keys.MustDefault(), all, all[0].ID)
	p.Update(uitest.Key("up"))
	if p.Cursor() != all[len(all)-1].ID {
		t.Fatalf("up from first must wrap to last, got %s", p.Cursor())
	}
	if p.View(theme.NewStyles(all[0], 1), 80, 24) == "" {
		t.Fatal("empty view")
	}
}
