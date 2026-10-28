// Package layout holds the responsive breakpoints and the size helpers shared
// by views and components.
package layout

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Mode is the responsive layout class for a terminal size.
type Mode int

// Layout modes (docs/04-development-plan.md, Stage 0 task 8).
const (
	TooSmall Mode = iota // below MinWidth×MinHeight: "terminal too small" screen
	Stacked              // MinWidth–WideWidth-1 columns: panes stacked vertically
	Wide                 // WideWidth+ columns: two-column layouts
)

// Breakpoints.
const (
	MinWidth  = 80
	MinHeight = 24
	WideWidth = 120
)

func (m Mode) String() string {
	switch m {
	case Wide:
		return "wide"
	case Stacked:
		return "stacked"
	default:
		return "too-small"
	}
}

// ModeFor classifies a terminal size.
func ModeFor(width, height int) Mode {
	switch {
	case width < MinWidth || height < MinHeight:
		return TooSmall
	case width < WideWidth:
		return Stacked
	default:
		return Wide
	}
}

// Fit forces s to exactly width×height cells: long lines are truncated
// (ANSI-aware), short lines and missing lines are padded with spaces.
func Fit(s string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	var b strings.Builder
	for i := 0; i < height; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		line := ""
		if i < len(lines) {
			line = ansi.Truncate(lines[i], width, "…")
		}
		b.WriteString(line)
		if pad := width - ansi.StringWidth(line); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
	}
	return b.String()
}

// Split divides total into n parts that differ by at most one cell.
func Split(total, n int) []int {
	if n <= 0 {
		return nil
	}
	out := make([]int, n)
	for i := range out {
		out[i] = total / n
		if i < total%n {
			out[i]++
		}
	}
	return out
}
