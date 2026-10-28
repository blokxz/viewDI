package panel

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/theme"
)

func TestRenderExactSize(t *testing.T) {
	s := theme.NewStyles(theme.Fallback(), 1)
	for _, size := range [][2]int{{20, 5}, {4, 2}, {60, 3}, {10, 10}} {
		w, h := size[0], size[1]
		out := Render(s, "A rather long panel title", "line one\nline two that is far too long to fit", w, h, true)
		lines := strings.Split(out, "\n")
		if len(lines) != h {
			t.Fatalf("%dx%d: got %d lines", w, h, len(lines))
		}
		for i, l := range lines {
			if got := ansi.StringWidth(l); got != w {
				t.Errorf("%dx%d line %d width %d: %q", w, h, i, got, ansi.Strip(l))
			}
		}
	}
}
