package help

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
)

func TestHelpShowsScopesAndCloses(t *testing.T) {
	km := keys.MustDefault()
	s := theme.NewStyles(theme.Fallback(), 1)
	m := New(km, s, 120, 60, keys.Global, keys.Instance, keys.Table)
	out := ansi.Strip(m.View(s, 120, 60))
	for _, want := range []string{"Global", "Instance actions", "Table", "theme picker", "pause / unpause"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q", want)
		}
	}
	for _, k := range []string{"esc", "?", "q"} {
		_, cmd := New(km, s, 80, 24, keys.Global).Update(uitest.Key(k))
		if msgs := uitest.Collect(cmd); len(msgs) != 1 || msgs[0] != (modal.CloseMsg{}) {
			t.Errorf("%q did not close help: %v", k, msgs)
		}
	}
}

func TestHelpScrollsInSmallTerminal(t *testing.T) {
	km := keys.MustDefault()
	s := theme.NewStyles(theme.Fallback(), 1)
	m := New(km, s, 80, 24, keys.Global, keys.Instance)
	first := m.View(s, 80, 24)
	if !strings.Contains(ansi.Strip(first), "scroll") {
		t.Fatal("small terminal must offer scrolling")
	}
	for range 100 {
		m.Update(uitest.Key("down"))
	}
	last := m.View(s, 80, 24)
	m.Update(uitest.Key("up"))
	if m.View(s, 80, 24) == last {
		t.Fatal("one up after over-scrolling must move immediately")
	}
	if first == last || !strings.Contains(ansi.Strip(last), "pause / unpause") {
		t.Fatal("scrolling to the end must reveal the last binding")
	}
}
