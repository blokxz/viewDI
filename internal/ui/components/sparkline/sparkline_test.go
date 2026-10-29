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

func TestTrend(t *testing.T) {
	if got := Trend([]float64{100, 100, 101, 100}, 4); got != "▄▄▄▄" {
		t.Fatalf("steady: %q", got)
	}
	if r := []rune(Trend([]float64{1000, 1100, 1200}, 3)); r[0] >= r[1] || r[1] >= r[2] {
		t.Fatalf("rising: %q", string(r))
	}
	if got := Trend(nil, 2); got != "  " {
		t.Fatalf("empty: %q", got)
	}
}

func TestChart(t *testing.T) {
	rows := Chart([]float64{0, 5, 10}, 4, 2, 10)
	if len(rows) != 2 {
		t.Fatalf("%d rows", len(rows))
	}
	if rows[0] != "   █" || rows[1] != "  ██" {
		t.Fatalf("rows %q", rows)
	}
	for _, r := range Chart([]float64{1, 2, 3}, 5, 3, 0) {
		if utf8.RuneCountInString(r) != 5 {
			t.Fatalf("row %q not 5 cells", r)
		}
	}
	if got := Chart(nil, 3, 1, 0); got[0] != "   " {
		t.Fatalf("empty chart %q", got)
	}
}
