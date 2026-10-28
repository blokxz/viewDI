package debugoverlay

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/ui/theme"
)

func TestRoll(t *testing.T) {
	s := New()
	t0 := time.Unix(1000, 0)
	s.Roll(t0)
	for i := 1; i <= 100; i++ {
		s.RecordFrame(time.Duration(i) * time.Millisecond)
	}
	for range 30 {
		s.RecordMsg(tea.KeyPressMsg{})
	}
	s.RecordMsg(tea.WindowSizeMsg{})
	s.Roll(t0.Add(2 * time.Second))
	snap := s.Snapshot()
	if snap.FPS != 50 || snap.MsgsPerSec != 15.5 {
		t.Fatalf("fps=%v msgs=%v", snap.FPS, snap.MsgsPerSec)
	}
	if snap.FrameP50 != 50*time.Millisecond || snap.FrameP99 != 99*time.Millisecond {
		t.Fatalf("p50=%v p99=%v", snap.FrameP50, snap.FrameP99)
	}
	if len(snap.TopMsgs) != 2 || snap.TopMsgs[0] != "KeyPressMsg 15/s" {
		t.Fatalf("top = %v", snap.TopMsgs)
	}
	if snap.Goroutines < 1 {
		t.Fatal("goroutines not sampled")
	}
	if View(theme.NewStyles(theme.Fallback(), 1), snap, "wide", 120, 40) == "" {
		t.Fatal("empty view")
	}
}

func TestFrameRingWraps(t *testing.T) {
	s := New()
	for range frameWindow + 10 {
		s.RecordFrame(time.Millisecond)
	}
	if len(s.frames) != frameWindow {
		t.Fatalf("ring grew to %d", len(s.frames))
	}
}
