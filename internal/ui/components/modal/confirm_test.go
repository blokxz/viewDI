package modal

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
)

type deleted struct{}

func TestConfirmFlows(t *testing.T) {
	km := keys.MustDefault()
	tests := []struct {
		name        string
		keys        []string
		wantConfirm bool
		wantClose   bool
	}{
		{"y confirms", []string{"y"}, true, true},
		{"enter on default focus cancels", []string{"enter"}, false, true},
		{"tab then enter confirms", []string{"tab", "enter"}, true, true},
		{"esc cancels", []string{"esc"}, false, true},
		{"n cancels", []string{"n"}, false, true},
		{"other keys ignored", []string{"q", "d"}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m Modal = NewConfirm(km, "Delete web-api?", "body", "Delete", true, deleted{})
			var cmd tea.Cmd
			for _, k := range tt.keys {
				m, cmd = m.Update(uitest.Key(k))
			}
			closed, confirmed := false, false
			for _, msg := range uitest.Collect(cmd) {
				switch msg.(type) {
				case CloseMsg:
					closed = true
				case deleted:
					confirmed = true
				}
			}
			if closed != tt.wantClose || confirmed != tt.wantConfirm {
				t.Fatalf("closed=%v confirmed=%v", closed, confirmed)
			}
		})
	}
}

func TestConfirmRenders(t *testing.T) {
	c := NewConfirm(keys.MustDefault(), "Title", "Body", "OK", false, nil)
	if c.View(theme.NewStyles(theme.Fallback(), 1), 80, 24) == "" {
		t.Fatal("empty view")
	}
}
