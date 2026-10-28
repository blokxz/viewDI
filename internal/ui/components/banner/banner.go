// Package banner renders the persistent full-width banner slot under the
// header (reconnect countdowns, config errors).
package banner

import (
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Banner is the banner slot content. The zero value means "no banner".
type Banner struct {
	Level theme.Level
	Text  string
}

// ShowMsg sets the banner; an empty Text clears it.
type ShowMsg Banner

// Visible reports whether the banner occupies a line.
func (b Banner) Visible() bool { return b.Text != "" }

// Render draws the banner on one line of exactly width cells.
func Render(s *theme.Styles, b Banner, width int) string {
	if !b.Visible() || width <= 0 {
		return ""
	}
	style := s.Banner(b.Level)
	text := ansi.Truncate(b.Text, max(0, width-style.GetHorizontalFrameSize()), "…")
	return style.Width(width).Render(text)
}
