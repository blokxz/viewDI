// Package header renders the top line: app title, per-engine status and the
// update badge slot.
package header

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Engine is one engine's status as shown in the header.
type Engine struct {
	ID    string // "docker" | "incus" — selects the engine color
	Label string // "Docker"
	State string // short state text, e.g. "connected", "—"
	Level theme.Level
	Known bool // false until the supervisor reports (Stage 1): hollow dot
}

// Data is everything the header shows.
type Data struct {
	Engines []Engine
	Updates int // update badge; hidden when 0
}

// Render draws the header on one line of exactly width cells.
func Render(s *theme.Styles, d Data, width int) string {
	left := " " + s.AppTitle.Render("viewDI")

	parts := make([]string, 0, len(d.Engines)+1)
	for _, e := range d.Engines {
		dot := "○"
		if e.Known {
			dot = "●"
		}
		name, ok := s.EngineName[e.ID]
		if !ok {
			name = s.Bold
		}
		parts = append(parts, s.LevelText(e.Level).Render(dot)+" "+name.Render(e.Label)+" "+s.Muted.Render(e.State))
	}
	if d.Updates > 0 {
		parts = append(parts, s.Badge.Render("⬆ "+strconv.Itoa(d.Updates)))
	}
	right := strings.Join(parts, "  ") + " "

	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		// Narrow terminal: keep the engine status, drop the title.
		return ansi.Truncate(strings.Repeat(" ", max(0, width-ansi.StringWidth(right)))+right, width, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}
