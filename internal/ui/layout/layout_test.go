package layout

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestModeFor(t *testing.T) {
	tests := []struct {
		w, h int
		want Mode
	}{
		{79, 40, TooSmall},
		{120, 23, TooSmall},
		{80, 24, Stacked},
		{119, 40, Stacked},
		{120, 24, Wide},
		{300, 80, Wide},
	}
	for _, tt := range tests {
		if got := ModeFor(tt.w, tt.h); got != tt.want {
			t.Errorf("ModeFor(%d,%d) = %v, want %v", tt.w, tt.h, got, tt.want)
		}
	}
}

func TestFit(t *testing.T) {
	out := Fit("\x1b[1mhello world\x1b[0m\nhi\nextra\nlines", 8, 3)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 8 {
			t.Errorf("line %d width %d: %q", i, w, l)
		}
	}
	if !strings.HasSuffix(ansi.Strip(lines[0]), "…") {
		t.Errorf("long line not truncated with ellipsis: %q", lines[0])
	}
	if Fit("x", 0, 5) != "" {
		t.Error("zero width must render nothing")
	}
}

func TestSplit(t *testing.T) {
	got := Split(10, 3)
	if got[0]+got[1]+got[2] != 10 || got[0] != 4 || got[2] != 3 {
		t.Fatalf("Split(10,3) = %v", got)
	}
}
