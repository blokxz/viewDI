// Package tabbar renders a single-line tab strip, used for the main views and
// (from Stage 3) the detail view tabs.
package tabbar

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Tab is one entry. Key is the shortcut shown before the title ("1").
type Tab struct {
	Key   string
	Title string
}

// Render draws tabs with active highlighted and right right-aligned, on one
// line of exactly width cells. When space runs out, right is dropped first,
// then the titles (keys are always kept).
func Render(s *theme.Styles, tabs []Tab, active int, right string, width int) string {
	render := func(withTitles bool) string {
		var b strings.Builder
		b.WriteByte(' ')
		for i, t := range tabs {
			label := t.Key
			if withTitles {
				label = t.Key + " " + t.Title
			}
			if i == active {
				b.WriteString(s.TabActive.Render(label))
			} else {
				b.WriteString(s.TabInactive.Render(label))
			}
		}
		return b.String()
	}
	left := render(true)
	if ansi.StringWidth(left)+ansi.StringWidth(right)+1 > width {
		right = ""
	}
	if ansi.StringWidth(left) > width {
		left = render(false)
	}
	rightW := ansi.StringWidth(right)
	gap := width - ansi.StringWidth(left) - rightW
	if gap < 1 {
		return ansi.Truncate(left, width, "")
	}
	return left + strings.Repeat(" ", gap) + right
}
