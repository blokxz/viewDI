// Package statusbar renders the bottom key-hint bar.
package statusbar

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Render draws as many hints as fit, then the pinned hints (always shown,
// e.g. "? help") and the indicators (e.g. MOCK, READ-ONLY) right-aligned, on
// one line of exactly width cells.
func Render(s *theme.Styles, hints, pinned []key.Binding, indicators []string, width int) string {
	var right strings.Builder
	if len(pinned) > 0 {
		right.WriteString(s.StatusBar.Render(" "))
	}
	for _, h := range pinned {
		if h.Enabled() {
			right.WriteString(s.KeyHint.Render(h.Help().Key) + s.KeyDesc.Render(" "+h.Help().Desc+"  "))
		}
	}
	for _, ind := range indicators {
		right.WriteString(s.Indicator.Render(ind))
	}
	rightStr := right.String()
	avail := width - ansi.StringWidth(rightStr)

	var left strings.Builder
	left.WriteString(s.StatusBar.Render(" "))
	used := 1
	sep := s.StatusBar.Render("  ")
	for i, h := range hints {
		if !h.Enabled() {
			continue
		}
		item := s.KeyHint.Render(h.Help().Key) + s.KeyDesc.Render(" "+h.Help().Desc)
		w := ansi.StringWidth(item)
		if i > 0 {
			w += 2
		}
		if used+w > avail {
			break
		}
		if i > 0 {
			left.WriteString(sep)
		}
		left.WriteString(item)
		used += w
	}
	if pad := avail - used; pad > 0 {
		left.WriteString(s.StatusBar.Render(strings.Repeat(" ", pad)))
	}
	return ansi.Truncate(left.String()+rightStr, width, "")
}
