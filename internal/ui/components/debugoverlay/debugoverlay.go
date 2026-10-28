// Package debugoverlay collects render and message statistics and renders the
// ctrl+d overlay (only available with --debug).
package debugoverlay

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/ui/theme"
)

// TickMsg rolls the per-second counters while the overlay is visible.
type TickMsg time.Time

// Tick schedules the next roll.
func Tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return TickMsg(t) })
}

const frameWindow = 240

// Stats is shared between Update (messages) and View (frames). View has a
// value receiver in Bubble Tea, so Stats lives behind a pointer with a mutex.
type Stats struct {
	mu       sync.Mutex
	frames   []time.Duration // ring of recent frame render times
	next     int
	frameCnt int
	msgCnt   int
	msgTypes map[string]int
	snap     Snapshot
	lastRoll time.Time
}

// Snapshot is the last rolled figures.
type Snapshot struct {
	FPS        float64
	FrameP50   time.Duration
	FrameP99   time.Duration
	MsgsPerSec float64
	TopMsgs    []string // "type n/s", busiest first
	Goroutines int
	HeapMiB    float64
}

// New returns empty stats.
func New() *Stats {
	return &Stats{frames: make([]time.Duration, 0, frameWindow), msgTypes: map[string]int{}}
}

// RecordFrame records one View() duration.
func (s *Stats) RecordFrame(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frames) < frameWindow {
		s.frames = append(s.frames, d)
	} else {
		s.frames[s.next] = d
		s.next = (s.next + 1) % frameWindow
	}
	s.frameCnt++
}

// RecordMsg records one message processed by Update.
func (s *Stats) RecordMsg(msg tea.Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgCnt++
	s.msgTypes[fmt.Sprintf("%T", msg)]++
}

// Roll computes a new snapshot from the counters since the previous roll.
func (s *Stats) Roll(now time.Time) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	s.mu.Lock()
	defer s.mu.Unlock()
	elapsed := time.Second
	if !s.lastRoll.IsZero() && now.After(s.lastRoll) {
		elapsed = now.Sub(s.lastRoll)
	}
	s.lastRoll = now
	sec := elapsed.Seconds()

	sorted := slices.Clone(s.frames)
	slices.Sort(sorted)
	type kv struct {
		k string
		v int
	}
	var types []kv
	for k, v := range s.msgTypes {
		types = append(types, kv{k, v})
	}
	slices.SortFunc(types, func(a, b kv) int {
		if a.v != b.v {
			return b.v - a.v
		}
		return strings.Compare(a.k, b.k)
	})
	var top []string
	for _, t := range types[:min(3, len(types))] {
		top = append(top, fmt.Sprintf("%s %.0f/s", strings.TrimPrefix(t.k, "tea."), float64(t.v)/sec))
	}

	s.snap = Snapshot{
		FPS:        float64(s.frameCnt) / sec,
		FrameP50:   percentile(sorted, 50),
		FrameP99:   percentile(sorted, 99),
		MsgsPerSec: float64(s.msgCnt) / sec,
		TopMsgs:    top,
		Goroutines: runtime.NumGoroutine(),
		HeapMiB:    float64(mem.HeapAlloc) / (1 << 20),
	}
	s.frameCnt, s.msgCnt = 0, 0
	clear(s.msgTypes)
}

// Snapshot returns the last rolled figures.
func (s *Stats) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := (len(sorted) - 1) * p / 100
	return sorted[i]
}

// View renders the overlay box.
func View(st *theme.Styles, snap Snapshot, layout string, width, height int) string {
	lines := []string{
		st.PanelTitle.Render("debug"),
		fmt.Sprintf("fps      %5.1f", snap.FPS),
		fmt.Sprintf("frame    p50 %s  p99 %s", fmtDur(snap.FrameP50), fmtDur(snap.FrameP99)),
		fmt.Sprintf("msgs/s   %5.1f", snap.MsgsPerSec),
	}
	for _, t := range snap.TopMsgs {
		lines = append(lines, "  "+st.Muted.Render(t))
	}
	lines = append(lines,
		fmt.Sprintf("gorout.  %d", snap.Goroutines),
		fmt.Sprintf("heap     %.1f MiB", snap.HeapMiB),
		fmt.Sprintf("screen   %dx%d %s", width, height, layout),
	)
	return st.Toast(theme.LevelInfo).Render(strings.Join(lines, "\n"))
}

func fmtDur(d time.Duration) string {
	return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000)
}
