package sparkline

import (
	"testing"
	"unicode/utf8"
)

func TestRender(t *testing.T) {
	if got := Render([]float64{0, 1, 2, 4, 8}, 5, 0); got != "▁▁▂▄█" {
		t.Fatalf("%q", got)
	}
	if got := Render([]float64{5}, 4, 10); got != "   ▄" {
		t.Fatalf("right-aligned: %q", got)
	}
	if got := Render(nil, 3, 0); got != "   " {
		t.Fatalf("empty: %q", got)
	}
	if got := Render([]float64{50}, 1, 10); got != "█" {
		t.Fatalf("clamped: %q", got)
	}
	long := make([]float64, 300)
	long[150] = 9 // a single spike survives downsampling
	got := Render(long, 30, 0)
	if utf8.RuneCountInString(got) != 30 || got[0:3] != "▁" {
		t.Fatalf("downsampled %q", got)
	}
	found := false
	for _, r := range got {
		found = found || r == '█'
	}
	if !found {
		t.Fatal("spike lost")
	}
}
