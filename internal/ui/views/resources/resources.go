// Package resources is the Resources dashboard (2): per-engine CPU, RAM,
// network and disk with gauges and sparklines, and the top consumers.
package resources

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/components/gauge"
	"github.com/blokxz/viewDI/internal/ui/components/panel"
	"github.com/blokxz/viewDI/internal/ui/components/sparkline"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
)

// RefreshStepMsg asks the app for a faster or slower sampling interval.
type RefreshStepMsg struct{ Faster bool }

// Column is a top-consumers sort column.
type Column int

// Sort columns, in the order "o" cycles through them.
const (
	ByCPU Column = iota
	ByMem
	ByNet
	ByDisk
	numColumns
)

var columnNames = [numColumns]string{"cpu", "mem", "net", "disk"}

func (c Column) String() string { return columnNames[c] }

// View is the Resources dashboard.
type View struct {
	keys    *keys.Map
	snap    *store.Snapshot
	sort    Column
	reverse bool // ascending instead of descending
}

var _ views.View = (*View)(nil)

// New returns the view.
func New(km *keys.Map) *View { return &View{keys: km} }

// Title implements views.View.
func (v *View) Title() string { return "Resources" }

// Scope implements views.View.
func (v *View) Scope() keys.Scope { return keys.Resources }

// ShowsInstances implements views.View.
func (v *View) ShowsInstances() bool { return false }

// Panes implements views.View: the engine panels and the top consumers.
func (v *View) Panes() int { return 2 }

// Sort returns the top-consumers sort.
func (v *View) Sort() (Column, bool) { return v.sort, v.reverse }

// Update implements views.View.
func (v *View) Update(msg tea.Msg) (views.View, tea.Cmd) {
	if m, ok := msg.(views.DataMsg); ok {
		v.snap = m.Snapshot
	}
	return v, nil
}

// HandleKey implements views.View.
func (v *View) HandleKey(msg tea.KeyPressMsg, _ int) (views.View, tea.Cmd, bool) {
	switch v.keys.Action(msg, keys.Resources) {
	case "sort":
		v.sort = (v.sort + 1) % numColumns
	case "sort_reverse":
		v.reverse = !v.reverse
	case "rate_up":
		return v, func() tea.Msg { return RefreshStepMsg{Faster: true} }, true
	case "rate_down":
		return v, func() tea.Msg { return RefreshStepMsg{Faster: false} }, true
	default:
		return v, nil, false
	}
	return v, nil, true
}

// Hints implements views.View.
func (v *View) Hints(km *keys.Map) []key.Binding {
	return []key.Binding{
		km.Binding(keys.Resources, "sort"), km.Binding(keys.Resources, "sort_reverse"),
		km.Binding(keys.Resources, "rate_up"), km.Binding(keys.Resources, "rate_down"),
		km.Binding(keys.Global, keys.ActReconnect),
	}
}

// engineH is the height of an engine panel.
const engineH = 6

// Render implements views.View.
func (v *View) Render(ctx views.Context) string {
	s := ctx.Styles
	var top string
	h := ctx.Height
	if ctx.Mode == layout.Wide {
		var cols []string
		for i, w := range layout.Split(ctx.Width, len(engine.IDs)) {
			cols = append(cols, v.enginePanel(s, engine.IDs[i], w, ctx.Focus == 0))
		}
		top = lipgloss.JoinHorizontal(lipgloss.Top, cols...)
		h -= engineH
	} else {
		var rows []string
		for _, id := range engine.IDs {
			rows = append(rows, v.enginePanel(s, id, ctx.Width, ctx.Focus == 0))
		}
		top = strings.Join(rows, "\n")
		h -= engineH * len(engine.IDs)
	}
	if h < 3 {
		return layout.Fit(top, ctx.Width, ctx.Height)
	}
	return top + "\n" + v.topPanel(s, ctx.Width, h, ctx.Focus == 1)
}

func (v *View) enginePanel(s *theme.Styles, id engine.ID, width int, focused bool) string {
	ev := v.snap.Engine(id)
	title := strings.ToUpper(id.Label())
	if ev.Stale {
		title += " (stale)"
	}
	if h := v.snap.Sampling(id); h.Source != "" {
		title += " · " + h.Source
	}
	inner := width - 4
	p, ok := v.snap.EnginePoint(id)
	var body string
	switch {
	case ev.Health.State == store.StateUnavailable:
		body = s.LevelText(theme.LevelErr).Render("unavailable") + s.Muted.Render(" — "+ev.Health.Err)
	case !ok:
		msg := "waiting for samples…"
		if h := v.snap.Sampling(id); h.Failing > 0 {
			msg = "sampling failing: " + h.Err
		} else if ev.Health.State != store.StateConnected {
			msg = "not connected"
		}
		body = s.Muted.Render(msg)
	default:
		body = v.engineLines(s, id, ev, p, inner)
	}
	if ev.Stale {
		body = s.Muted.Render(ansi.Strip(body))
	}
	return panel.Render(s, title, body, width, engineH, focused)
}

// engineLines draws CPU, RAM, NET and DISK: label, gauge or rates, and a
// sparkline in the remaining width.
func (v *View) engineLines(s *theme.Styles, id engine.ID, ev store.EngineView, p metrics.Point, inner int) string {
	series := v.snap.EngineSeries(id, store.History)
	pick := func(f func(metrics.Point) float64) []float64 {
		out := make([]float64, len(series))
		for i, q := range series {
			out[i] = f(q)
		}
		return out
	}
	gw := max(6, inner/4)
	// Sparklines show the trend, scaled to the series' own range.
	spark := func(vals []float64, used float64, st lipgloss.Style) string {
		w := inner - int(used)
		if w < 4 {
			return ""
		}
		return " " + st.Render(sparkline.Trend(vals, w-1))
	}
	ncpu := float64(max(1, ev.Info.NCPU))
	cpuFrac := p.CPU / (ncpu * 100)
	cpuText := fmt.Sprintf(" %3.0f%%", cpuFrac*100)
	cpuLine := s.Bold.Render("CPU  ") + gauge.Render(s, s.ChartCPU, cpuFrac, gw) + s.Text.Render(cpuText)
	cpuLine += spark(pick(func(q metrics.Point) float64 { return q.CPU }), float64(5+gw+len(cpuText)), s.ChartCPU)

	memTotal := ev.Info.MemTotal
	memFrac := 0.0
	memText := " " + metrics.Bytes(p.MemUsed)
	if memTotal > 0 {
		memFrac = float64(p.MemUsed) / float64(memTotal)
		memText += "/" + metrics.Bytes(memTotal)
	}
	memLine := s.Bold.Render("RAM  ") + gauge.Render(s, s.ChartMem, memFrac, gw) + s.Text.Render(memText)
	memLine += spark(pick(func(q metrics.Point) float64 { return float64(q.MemUsed) }), float64(5+gw+ansi.StringWidth(memText)), s.ChartMem)

	netText := fmt.Sprintf("↓ %-9s ↑ %-9s", metrics.BytesRate(p.NetRx), metrics.BytesRate(p.NetTx))
	netLine := s.Bold.Render("NET  ") + s.ChartNetRx.Render(netText)
	netLine += spark(pick(func(q metrics.Point) float64 { return q.NetRx + q.NetTx }), float64(5+ansi.StringWidth(netText)), s.ChartNetRx)

	blkText := fmt.Sprintf("r %-9s w %-9s", metrics.BytesRate(p.BlkRead), metrics.BytesRate(p.BlkWrite))
	blkLine := s.Bold.Render("DISK ") + s.ChartDisk.Render(blkText)
	blkLine += spark(pick(func(q metrics.Point) float64 { return q.BlkRead + q.BlkWrite }), float64(5+ansi.StringWidth(blkText)), s.ChartDisk)

	return strings.Join([]string{cpuLine, memLine, netLine, blkLine}, "\n")
}

// consumer is one top-consumers row.
type consumer struct {
	in engine.Instance
	p  metrics.Point
}

func (v *View) consumers() []consumer {
	if v.snap == nil {
		return nil
	}
	pts := v.snap.Points()
	out := make([]consumer, 0, len(pts))
	for _, in := range v.snap.Instances {
		if p, ok := pts[in.Key()]; ok {
			out = append(out, consumer{in, p})
		}
	}
	key := func(c consumer) float64 {
		switch v.sort {
		case ByMem:
			return float64(c.p.MemUsed)
		case ByNet:
			return c.p.NetRx + c.p.NetTx
		case ByDisk:
			return c.p.BlkRead + c.p.BlkWrite
		}
		return c.p.CPU
	}
	slices.SortStableFunc(out, func(a, b consumer) int {
		c := cmp.Compare(key(b), key(a))
		if v.reverse {
			c = -c
		}
		return cmp.Or(c, cmp.Compare(a.in.Key(), b.in.Key()))
	})
	return out
}

func (v *View) topPanel(s *theme.Styles, width, height int, focused bool) string {
	inner := width - 4
	rows := v.consumers()
	arrow := "↓"
	if v.reverse {
		arrow = "↑"
	}
	showBlk := inner >= 100 // narrow terminals drop the disk column
	nameW := inner - 5 - 1 - 7 - 1 - 12 - 1 - 21 - 1
	if showBlk {
		nameW -= 21 + 1
	}
	nameW = max(10, nameW)
	hdr := func(c Column, label string, w int) string {
		if v.sort == c {
			label += " " + arrow
		}
		return padLeft(label, w)
	}
	header := pad("ENG", 5) + " " + pad("NAME", nameW) + " " + hdr(ByCPU, "CPU%", 7) + " " + hdr(ByMem, "MEM", 12) + " " + hdr(ByNet, "NET ↓/↑", 21)
	if showBlk {
		header += " " + hdr(ByDisk, "BLK r/w", 21)
	}
	lines := []string{s.TableHeader.Render(header)}
	if len(rows) == 0 {
		lines = append(lines, "", s.Muted.Render("No samples yet."))
	}
	for _, c := range rows[:min(len(rows), max(0, height-3))] {
		mem := metrics.Bytes(c.p.MemUsed)
		if c.p.MemLimit > 0 {
			mem += "/" + metrics.Bytes(c.p.MemLimit)
		}
		eng, ok := s.EngineName[string(c.in.Engine)]
		if !ok {
			eng = s.Text
		}
		line := eng.Render(pad(c.in.Engine.Short(), 5)) + " " + s.Text.Render(pad(c.in.Name, nameW)) + " " +
			s.ChartCPU.Render(padLeft(metrics.Percent(c.p.CPU), 7)) + " " + s.ChartMem.Render(padLeft(mem, 12)) + " " +
			s.ChartNetRx.Render(padLeft(metrics.BytesRate(c.p.NetRx)+" / "+metrics.BytesRate(c.p.NetTx), 21))
		if showBlk {
			line += " " + s.ChartDisk.Render(padLeft(metrics.BytesRate(c.p.BlkRead)+" / "+metrics.BytesRate(c.p.BlkWrite), 21))
		}
		lines = append(lines, line)
	}
	title := fmt.Sprintf("Top consumers · %d running · sort %s", len(rows), v.sort)
	return panel.Render(s, title, strings.Join(lines, "\n"), width, height, focused)
}

func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

func padLeft(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return strings.Repeat(" ", max(0, w-ansi.StringWidth(s))) + s
}
