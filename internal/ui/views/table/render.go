package table

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/components/panel"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
)

// cardHeight is the height of an unavailable-engine card.
const cardHeight = 5

// chrome is the number of panel lines that are not rows: borders, the
// header row and the footer line.
const chrome = 4

func pageRows(bodyH, cards int) int { return bodyH - cards*cardHeight - chrome - 1 }

// cardCount is the number of unavailable-engine cards shown above the table.
func (v *View) cardCount() int {
	n := 0
	if v.snap != nil {
		for _, e := range v.snap.Engines {
			if e.Health.State == store.StateUnavailable {
				n++
			}
		}
	}
	return n
}

// cols holds the flexible column widths for one frame.
type cols struct {
	name, image int
	showMetrics bool
}

const (
	wMark   = 2
	wEngine = 5
	wKind   = 6
	wState  = 12
	wCPU    = 5
	wMem    = 6
	wUp     = 4
)

func columns(width int) cols {
	c := cols{showMetrics: width >= 100}
	fixed := wMark + wEngine + wKind + wState + wUp + 6 // six single-space gaps
	if c.showMetrics {
		fixed += wCPU + wMem + 2
	}
	rest := max(0, width-fixed)
	c.name = max(8, rest*45/100)
	c.image = max(6, rest-c.name)
	return c
}

// Render implements views.View.
func (v *View) Render(ctx views.Context) string {
	s := ctx.Styles
	var blocks []string
	h := ctx.Height
	if v.snap != nil {
		for _, e := range v.snap.Engines {
			if e.Health.State == store.StateUnavailable && h >= cardHeight+chrome+2 {
				blocks = append(blocks, unavailableCard(s, e, ctx.Width, ctx.Now))
				h -= cardHeight
			}
		}
	}
	innerW := ctx.Width - 4
	page := max(1, h-chrome-1) // one line for the filter prompt
	start := max(0, min(v.offset, len(v.rows)-page))
	if v.cursor < start {
		start = v.cursor
	} else if v.cursor >= start+page {
		start = v.cursor - page + 1
	}
	c := columns(innerW)

	lines := make([]string, 0, page+3)
	lines = append(lines, v.promptLine(s, innerW))
	lines = append(lines, s.TableHeader.Render(ansi.Truncate(v.headerRow(c), innerW, "")))
	if len(v.rows) == 0 {
		lines = append(lines, "", s.Muted.Render(v.emptyText()))
	}
	for i := start; i < min(len(v.rows), start+page); i++ {
		lines = append(lines, v.row(s, c, v.rows[i], i == v.cursor, ctx.Now, innerW))
	}
	body := layout.Fit(strings.Join(lines, "\n"), innerW, h-3)
	body += "\n" + v.footer(s, innerW)
	blocks = append(blocks, panel.Render(s, "Instances", body, ctx.Width, h, ctx.Focus == 0))
	return strings.Join(blocks, "\n")
}

func (v *View) promptLine(s *theme.Styles, w int) string {
	switch {
	case v.typing:
		return s.Accent.Render("/") + s.Text.Render(v.filter) + s.Selected.Render(" ") + s.Muted.Render("  enter keep · esc clear")
	case v.filter != "":
		return s.Muted.Render("filter: ") + s.Text.Render(v.filter) + s.Muted.Render("  (/ edit · esc clear)")
	}
	return s.Muted.Render(ansi.Truncate("/ filter · e engine · o/O sort · P project", w, "…"))
}

func (v *View) emptyText() string {
	switch {
	case v.filter != "" || v.engine != "":
		return "No instance matches the filter."
	case v.snap == nil || allConnecting(v.snap):
		return "Connecting to the engines…"
	}
	return "No instances."
}

func allConnecting(s *store.Snapshot) bool {
	for _, e := range s.Engines {
		if e.InfoKnown || e.Health.State == store.StateUnavailable {
			return false
		}
	}
	return true
}

func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	if n := w - ansi.StringWidth(s); n > 0 {
		s += strings.Repeat(" ", n)
	}
	return s
}

func padLeft(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	if n := w - ansi.StringWidth(s); n > 0 {
		s = strings.Repeat(" ", n) + s
	}
	return s
}

func (v *View) headerRow(c cols) string {
	arrow := " ↑"
	if v.reverse {
		arrow = " ↓"
	}
	title := func(col Column, label string) string {
		if v.sort == col {
			return label + arrow
		}
		return label
	}
	parts := []string{
		pad("", wMark), pad(title(ColEngine, "ENG"), wEngine), pad(title(ColName, "NAME"), c.name),
		pad(title(ColKind, "KIND"), wKind), pad(title(ColState, "STATE"), wState), pad(title(ColImage, "IMAGE"), c.image),
	}
	if c.showMetrics {
		parts = append(parts, padLeft("CPU%", wCPU), padLeft("MEM", wMem))
	}
	parts = append(parts, padLeft(title(ColUptime, "UP"), wUp))
	return strings.Join(parts, " ")
}

// stateCell returns the glyph+label and the level that colors it.
func stateCell(in engine.Instance) (string, theme.Level, bool) {
	switch in.State {
	case engine.StateRunning:
		switch in.Health {
		case engine.HealthUnhealthy:
			return "▲ unhealthy", theme.LevelWarn, false
		case engine.HealthStarting:
			return "◐ starting", theme.LevelInfo, false
		}
		return "● running", theme.LevelOK, false
	case engine.StatePaused:
		return "⏸ paused", theme.LevelWarn, false
	case engine.StateRestarting:
		return "↻ restarting", theme.LevelWarn, false
	case engine.StateCreated:
		return "○ created", theme.LevelInfo, true
	case engine.StateStopped:
		if x, ok := in.Extra.(*engine.DockerExtra); ok && x.ExitCode != 0 {
			return "■ exited " + strconv.Itoa(x.ExitCode), theme.LevelErr, false
		}
		return "■ stopped", theme.LevelInfo, true
	case engine.StateError, engine.StateDead:
		return "✖ " + in.State.String(), theme.LevelErr, false
	case engine.StateRemoving:
		return "… removing", theme.LevelWarn, false
	}
	return "? unknown", theme.LevelInfo, true
}

func (v *View) row(s *theme.Styles, c cols, in engine.Instance, cursor bool, now time.Time, w int) string {
	stale := v.snap.Engine(in.Engine).Stale
	mark := "  "
	switch {
	case cursor && v.marks[in.Key()]:
		mark = "▶●"
	case cursor:
		mark = "▶ "
	case v.marks[in.Key()]:
		mark = " ●"
	}
	name := in.Name
	if in.Project != "" && in.Project != "default" {
		name += " [" + in.Project + "]"
	}
	state, level, muted := stateCell(in)
	image := in.Image
	if image == "" {
		image = "—"
	}
	cells := []string{
		mark, pad(in.Engine.Short(), wEngine), pad(name, c.name), pad(in.Kind.String(), wKind),
		pad(state, wState), pad(image, c.image),
	}
	if c.showMetrics {
		cpu, mem := "—", "—"
		if p, ok := v.snap.Point(in.Key()); ok {
			cpu, mem = metrics.Percent(p.CPU), metrics.Bytes(p.MemUsed)
		}
		cells = append(cells, padLeft(cpu, wCPU), padLeft(mem, wMem))
	}
	cells = append(cells, padLeft(uptime(in, now), wUp))

	if cursor {
		return s.Selected.Render(pad(strings.Join(cells, " "), w))
	}
	if stale {
		return s.Muted.Render(strings.Join(cells, " "))
	}
	eng, ok := s.EngineName[string(in.Engine)]
	if !ok {
		eng = s.Text
	}
	stateStyle := s.LevelText(level)
	if muted {
		stateStyle = s.Muted
	}
	var b strings.Builder
	b.WriteString(s.Accent.Render(cells[0]))
	b.WriteString(" " + eng.Render(cells[1]))
	b.WriteString(" " + s.Text.Render(cells[2]))
	b.WriteString(" " + s.Muted.Render(cells[3]))
	b.WriteString(" " + stateStyle.Render(cells[4]))
	b.WriteString(" " + s.Muted.Render(cells[5]))
	if c.showMetrics {
		b.WriteString(" " + s.ChartCPU.Render(cells[6]) + " " + s.ChartMem.Render(cells[7]))
	}
	b.WriteString(" " + s.Muted.Render(cells[len(cells)-1]))
	return b.String()
}

// uptime formats how long an instance has been running ("2d", "6h", "45m").
func uptime(in engine.Instance, now time.Time) string {
	d := in.Uptime(now)
	switch {
	case d <= 0:
		return "—"
	case d >= 24*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d >= time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d >= time.Minute:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	}
	return strconv.Itoa(int(d/time.Second)) + "s"
}

func (v *View) footer(s *theme.Styles, w int) string {
	total := 0
	if v.snap != nil {
		total = len(v.snap.Instances)
	}
	dir := "↑"
	if v.reverse {
		dir = "↓"
	}
	eng := "all"
	if v.engine != "" {
		eng = string(v.engine)
	}
	project := "all"
	if v.snap != nil {
		if p := v.snap.Engine(engine.Incus).Project; p != "" {
			project = p
		}
	}
	parts := []string{
		fmt.Sprintf("%d of %d shown", len(v.rows), total),
		"sort " + v.sort.ID() + " " + dir,
		"engine " + eng,
		"incus project " + project,
	}
	if n := len(v.marks); n > 0 {
		parts = append(parts, fmt.Sprintf("%d marked", n))
	}
	text := s.Muted.Render(strings.Join(parts, " · "))
	if v.snap != nil {
		for _, e := range v.snap.Engines {
			if e.Stale {
				text += s.LevelText(theme.LevelWarn).Render(fmt.Sprintf(" · %s stale (data from %s)", e.ID, e.LastSync.Local().Format("15:04:05")))
			}
		}
	}
	return lipgloss.PlaceHorizontal(w, lipgloss.Right, ansi.Truncate(text, w, "…"))
}

// unavailableCard explains why an engine is unavailable and how to fix it.
func unavailableCard(s *theme.Styles, e store.EngineView, width int, now time.Time) string {
	h := e.Health
	retry := "retrying soon"
	if d := h.RetryAt.Sub(now); d > 0 {
		retry = fmt.Sprintf("retrying in %ds", int(d.Round(time.Second)/time.Second))
	}
	lines := []string{
		s.LevelText(theme.LevelErr).Render("✖ "+e.ID.Label()+" unavailable") + s.Muted.Render(" — "+h.Err),
		s.Text.Render(h.Hint),
		s.Muted.Render(retry + " · ctrl+r to retry now"),
	}
	return panel.RenderFrame(s, s.PanelError, e.ID.Label(), strings.Join(lines, "\n"), width, cardHeight)
}
