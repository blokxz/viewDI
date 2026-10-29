// Package gauge renders a horizontal fill bar ("███████░░░░").
package gauge

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Render draws frac (0–1, clamped) as width cells: filled cells in fill,
// the rest in the theme's empty-gauge style.
func Render(s *theme.Styles, fill lipgloss.Style, frac float64, width int) string {
	if width <= 0 {
		return ""
	}
	frac = min(max(frac, 0), 1)
	n := int(frac*float64(width) + 0.5)
	if frac > 0 && n == 0 {
		n = 1 // any load is visible
	}
	return fill.Render(strings.Repeat("█", n)) + s.GaugeEmpty.Render(strings.Repeat("░", width-n))
}
