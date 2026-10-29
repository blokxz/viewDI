package gauge

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/theme"
)

func TestRender(t *testing.T) {
	s := theme.NewStyles(theme.Fallback(), 1)
	for frac, want := range map[float64]string{0: "░░░░░░░░░░", 0.01: "█░░░░░░░░░", 0.5: "█████░░░░░", 2: "██████████"} {
		if got := ansi.Strip(Render(s, lipgloss.NewStyle(), frac, 10)); got != want {
			t.Errorf("%v: %q", frac, got)
		}
	}
}
