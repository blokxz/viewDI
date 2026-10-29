package logview

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

var styles = theme.NewStyles(theme.Fallback(), 1)

func lines(from, n int) []engine.LogLine {
	out := make([]engine.LogLine, n)
	t0 := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	for i := range out {
		k := from + i
		src := engine.Stdout
		text := fmt.Sprintf("line %d ok", k)
		if k%5 == 4 {
			src, text = engine.Stderr, fmt.Sprintf("line %d ERROR boom", k)
		}
		out[i] = engine.LogLine{Time: t0.Add(time.Duration(k) * time.Second), Source: src, Text: text}
	}
	return out
}

func plain(s string) []string { return strings.Split(ansi.Strip(s), "\n") }

func TestRingKeepsNewest(t *testing.T) {
	m := New(10)
	m.Append(lines(0, 25), 3)
	if m.Len() != 10 || m.Visible() != 10 || m.Dropped() != 3 {
		t.Fatalf("len %d visible %d dropped %d", m.Len(), m.Visible(), m.Dropped())
	}
	got := plain(m.Render(styles, 60, 10))
	if !strings.Contains(got[0], "line 15 ") || !strings.Contains(got[9], "line 24 ") {
		t.Fatalf("rows %q", got)
	}
	// A batch larger than the ring keeps its newest lines.
	m.Append(lines(100, 30), 0)
	if m.Dropped() != 3 || !strings.Contains(plain(m.Render(styles, 60, 10))[9], "line 129 ") {
		t.Fatalf("dropped %d", m.Dropped())
	}
}

func TestPauseCountsNewLines(t *testing.T) {
	m := New(100)
	m.Append(lines(0, 20), 0)
	m.TogglePause()
	m.Append(lines(20, 7), 0)
	if !m.Paused() || m.NewCount() != 7 {
		t.Fatalf("paused %v new %d", m.Paused(), m.NewCount())
	}
	if last := plain(m.Render(styles, 60, 5))[4]; !strings.Contains(last, "line 19 ") {
		t.Fatalf("paused view moved: %q", last)
	}
	if st := ansi.Strip(m.Status(styles)); !strings.Contains(st, "PAUSED (+7 new)") {
		t.Fatalf("status %q", st)
	}
	m.TogglePause()
	if last := plain(m.Render(styles, 60, 5))[4]; !strings.Contains(last, "line 26 ") || m.NewCount() != 0 {
		t.Fatalf("resumed view %q", last)
	}
}

func TestPausedViewSurvivesEviction(t *testing.T) {
	m := New(20)
	m.Append(lines(0, 20), 0)
	m.TogglePause()
	m.Append(lines(20, 15), 0) // the paused lines 0–14 fall out
	got := plain(m.Render(styles, 60, 5))
	if !strings.Contains(got[4], "line 19 ") {
		t.Fatalf("paused bottom %q", got[4])
	}
}

func TestFilterSourceAndMatches(t *testing.T) {
	m := New(100)
	m.Append(lines(0, 50), 0)
	m.SetFilter("error")
	if m.Visible() != 10 {
		t.Fatalf("filter error: %d visible", m.Visible())
	}
	m.Append(lines(50, 5), 0)
	if m.Visible() != 11 {
		t.Fatalf("incremental filter: %d visible", m.Visible())
	}
	out := m.Render(styles, 80, 3)
	if !strings.Contains(out, styles.Match.Render("ERROR")) {
		t.Fatal("match not highlighted")
	}
	if !m.NextMatch(false) || m.Following() {
		t.Fatal("ctrl+p did not stop following")
	}
	m.SetFilter("ERROR")
	if m.Visible() != 11 {
		t.Fatalf("smart case: %d visible", m.Visible())
	}
	m.SetFilter("(")
	if _, e := m.Filter(); e == "" || m.Visible() != 55 {
		t.Fatalf("invalid regex: err %q visible %d", e, m.Visible())
	}
	m.SetFilter("")
	m.CycleSource() // stdout
	if m.Visible() != 44 {
		t.Fatalf("stdout only: %d", m.Visible())
	}
	m.CycleSource() // stderr
	if m.Visible() != 11 {
		t.Fatalf("stderr only: %d", m.Visible())
	}
	m.CycleSource()
	m.CycleSource() // back to all
	if m.Source() != 0 || m.Visible() != 55 {
		t.Fatalf("source %v visible %d", m.Source(), m.Visible())
	}
}

func TestScrollTopAndEnd(t *testing.T) {
	m := New(100)
	m.Append(lines(0, 50), 0)
	m.Render(styles, 60, 10)
	m.Top()
	if got := plain(m.Render(styles, 60, 10)); !strings.Contains(got[0], "line 0 ") {
		t.Fatalf("top %q", got[0])
	}
	m.Append(lines(50, 3), 0)
	if m.NewCount() != 3 {
		t.Fatalf("new while scrolled %d", m.NewCount())
	}
	m.Scroll(1000)
	if !m.Following() {
		t.Fatal("scrolling to the end did not follow again")
	}
	if first, second := m.G(), m.G(); first || !second {
		t.Fatal("gg sequence")
	}
}

func TestWrapAndTimestamps(t *testing.T) {
	m := New(10)
	m.Append([]engine.LogLine{{Source: engine.Stdout, Text: strings.Repeat("abcdefghij", 6)}}, 0)
	m.ToggleTimestamps()
	m.ToggleWrap()
	got := plain(m.Render(styles, 24, 4))
	if !strings.HasPrefix(got[0], "out ") || !strings.HasPrefix(got[1], "    ") || !strings.Contains(got[2], "ghij") {
		t.Fatalf("wrapped %q", got)
	}
	for _, r := range got {
		if ansi.StringWidth(r) != 24 {
			t.Fatalf("row %q not 24 cells", r)
		}
	}
}

func TestFilterPrompt(t *testing.T) {
	m := New(10)
	m.StartFilter()
	for _, k := range []string{"e", "r", "r"} {
		m.Input(k, k, false, false, false)
	}
	m.Input("backspace", "", false, false, false)
	if f, _ := m.Filter(); f != "er" || !m.Typing() {
		t.Fatalf("filter %q", f)
	}
	if m.Input("tab", "", false, false, false) {
		t.Fatal("tab swallowed")
	}
	m.Input("enter", "", true, false, false)
	if m.Typing() {
		t.Fatal("still typing")
	}
}

func BenchmarkFlood(b *testing.B) {
	m := New(DefaultCapacity)
	m.SetFilter("error|warn")
	batch := lines(0, 100) // 1 000 lines/s at 10 batches/s
	for b.Loop() {
		m.Append(batch, 0)
		_ = m.Render(styles, 160, 40)
	}
}
