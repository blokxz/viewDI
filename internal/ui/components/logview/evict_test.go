package logview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A paused view whose lines all left the ring still fills the screen.
func TestPausedViewAfterEvictionFillsScreen(t *testing.T) {
	m := New(1000)
	m.Append(lines(0, 1000), 0)
	m.Render(styles, 80, 20)
	m.TogglePause()
	for i := range 20 {
		m.Append(lines(1000+i*100, 100), 0)
	}
	m.SetFilter("ERROR")
	m.CycleSource()
	m.CycleSource()
	out := ansi.Strip(m.Render(styles, 80, 20))
	if strings.Count(out, "ERROR") < 10 {
		t.Fatalf("only %d matching rows:\n%s", strings.Count(out, "ERROR"), out)
	}
}
