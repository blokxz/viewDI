// Package panel renders a bordered box with its title embedded in the top
// border, sized to exactly width×height cells.
package panel

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Render draws body inside a rounded border. The focused panel uses the
// border_focus token. Bodies larger than the inner area are truncated.
func Render(s *theme.Styles, title, body string, width, height int, focused bool) string {
	frame := s.Panel
	if focused {
		frame = s.PanelFocused
	}
	return RenderFrame(s, frame, title, body, width, height)
}

// RenderFrame is Render with an explicit frame style (e.g. s.PanelError).
func RenderFrame(s *theme.Styles, frame lipgloss.Style, title, body string, width, height int) string {
	if width < 4 || height < 2 {
		return layout.Fit("", width, height)
	}
	border := lipgloss.RoundedBorder()
	edge := lipgloss.NewStyle().Foreground(frame.GetBorderTopForeground())

	innerW := width - 2
	label := ""
	if title != "" && innerW >= 5 { // room for "─ x ─"
		label = " " + ansi.Truncate(title, innerW-3, "…") + " "
	}
	fill := innerW - 1 - ansi.StringWidth(label)
	if fill < 0 {
		fill = 0
	}
	top := edge.Render(border.TopLeft+border.Top) + s.PanelTitle.Render(label) +
		edge.Render(strings.Repeat(border.Top, fill)+border.TopRight)

	var b strings.Builder
	b.WriteString(top)
	if height > 2 {
		content := layout.Fit(body, innerW-2, height-2) // 1-cell horizontal padding
		for _, line := range strings.Split(content, "\n") {
			b.WriteByte('\n')
			b.WriteString(edge.Render(border.Left) + " " + line + " " + edge.Render(border.Right))
		}
	}
	b.WriteByte('\n')
	b.WriteString(edge.Render(border.BottomLeft + strings.Repeat(border.Bottom, innerW) + border.BottomRight))
	return b.String()
}
