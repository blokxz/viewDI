// Package admin is the Admin dashboard (1): one card per daemon, the active
// alerts, the recent instance events and the main shortcuts.
package admin

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/alerts"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/components/panel"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
)

// View is the Admin dashboard.
type View struct {
	keys *keys.Map
	snap *store.Snapshot
}

var _ views.View = (*View)(nil)

// New returns the view.
func New(km *keys.Map) *View { return &View{keys: km} }

// Title implements views.View.
func (v *View) Title() string { return "Admin" }

// Scope implements views.View.
func (v *View) Scope() keys.Scope { return "" }

// ShowsInstances implements views.View.
func (v *View) ShowsInstances() bool { return true }

// Panes implements views.View: daemon cards, alerts, events.
func (v *View) Panes() int { return 3 }

// Update implements views.View.
func (v *View) Update(msg tea.Msg) (views.View, tea.Cmd) {
	if m, ok := msg.(views.DataMsg); ok {
		v.snap = m.Snapshot
	}
	return v, nil
}

// HandleKey implements views.View. The dashboard has no local keys.
func (v *View) HandleKey(tea.KeyPressMsg, int) (views.View, tea.Cmd, bool) { return v, nil, false }

// Hints implements views.View.
func (v *View) Hints(km *keys.Map) []key.Binding {
	return []key.Binding{km.Binding(keys.Global, keys.ActReconnect), km.Binding(keys.Global, keys.ActProject)}
}

const cardH = 8

// Render implements views.View.
func (v *View) Render(ctx views.Context) string {
	s := ctx.Styles
	var blocks []string
	h := ctx.Height
	if ctx.Mode == layout.Wide {
		var cards []string
		for i, w := range layout.Split(ctx.Width, len(engine.IDs)) {
			cards = append(cards, v.card(s, engine.IDs[i], w, ctx.Focus == 0, ctx.Now))
		}
		blocks = append(blocks, lipgloss.JoinHorizontal(lipgloss.Top, cards...))
		h -= cardH
		alertsH := max(4, min(len(v.alerts())+3, h/2))
		blocks = append(blocks, v.alertsPanel(s, ctx.Width, alertsH, ctx.Focus == 1, ctx.Now))
		h -= alertsH
		if h >= 4 {
			w := layout.Split(ctx.Width, 3)
			left := v.shortcuts(s, w[0], h)
			right := v.eventsPanel(s, w[1]+w[2], h, ctx.Focus == 2)
			blocks = append(blocks, lipgloss.JoinHorizontal(lipgloss.Top, left, right))
		}
	} else {
		// Stacked: compact cards side by side are too narrow; stack them.
		for _, id := range engine.IDs {
			if h >= cardH {
				blocks = append(blocks, v.card(s, id, ctx.Width, ctx.Focus == 0, ctx.Now))
				h -= cardH
			}
		}
		if h >= 4 {
			blocks = append(blocks, v.alertsPanel(s, ctx.Width, h, ctx.Focus == 1, ctx.Now))
		}
	}
	return layout.Fit(strings.Join(blocks, "\n"), ctx.Width, ctx.Height)
}

func (v *View) alerts() []alerts.Alert {
	if v.snap == nil {
		return nil
	}
	return v.snap.Alerts
}

func row(s *theme.Styles, label, value string) string {
	return s.Muted.Render(fmt.Sprintf("%-11s", label)) + value
}

func (v *View) card(s *theme.Styles, id engine.ID, width int, focused bool, now time.Time) string {
	ev := v.snap.Engine(id)
	h, info := ev.Health, ev.Info
	level, state := theme.LevelInfo, "waiting for the supervisor"
	switch h.State {
	case store.StateConnecting:
		state = "connecting…"
	case store.StateConnected:
		level, state = theme.LevelOK, "connected"
	case store.StateDegraded:
		level, state = theme.LevelWarn, "degraded — "+h.Err
	case store.StateUnavailable:
		level, state = theme.LevelErr, "unavailable — "+h.Err
	}
	mode := ""
	switch {
	case id == engine.Docker && info.Rootless:
		mode = " (rootless)"
	case id == engine.Docker && ev.InfoKnown:
		mode = " (rootful)"
	case id == engine.Incus && info.Capabilities.Restricted:
		mode = " (restricted)"
	case id == engine.Incus && ev.InfoKnown:
		mode = " (full access)"
	}
	lines := []string{row(s, "Status", s.LevelText(level).Render("● ")+s.Text.Render(state)+s.Muted.Render(mode))}
	if !ev.InfoKnown {
		if h.Hint != "" {
			lines = append(lines, s.Text.Render(h.Hint))
		}
		return panel.Render(s, id.Label()+" daemon", strings.Join(lines, "\n"), width, cardH, focused)
	}
	c := ev.Counts
	version := info.Version + s.Muted.Render("  API "+info.APIVersion)
	if id == engine.Incus {
		project := ev.Project
		if project == "" {
			project = "all"
		}
		version += s.Muted.Render("  project: " + project)
	}
	label := "Containers"
	counts := fmt.Sprintf("%d  ▶ %d  ■ %d  ⏸ %d", c.Total, c.Running, c.Stopped, c.Paused)
	if id == engine.Incus {
		label = "Instances"
		counts = fmt.Sprintf("%d  ▶ %d (%d VM)  ■ %d  ⏸ %d", c.Total, c.Running, c.VMs, c.Stopped, c.Paused)
	}
	inventory := fmt.Sprintf("Images %d  Volumes %d  Networks %d", info.Images, len(ev.Volumes), len(ev.Networks))
	if id == engine.Incus {
		inventory += fmt.Sprintf("  Projects %d", len(info.Projects))
	}
	storage := info.StorageDriver
	if info.CgroupVersion != "" {
		storage += s.Muted.Render("  cgroup v" + info.CgroupVersion)
	}
	if info.NCPU > 0 {
		storage += s.Muted.Render(fmt.Sprintf("  %d CPUs", info.NCPU))
	}
	lines = append(lines,
		row(s, "Version", s.Text.Render(version)),
		row(s, label, s.Text.Render(counts)),
		row(s, "", s.Text.Render(inventory)),
		row(s, "Storage", s.Text.Render(storage)),
	)
	if h.Socket != "" {
		lines = append(lines, row(s, "Socket", s.Muted.Render(ansi.Truncate(h.Socket, width-17, "…"))))
	}
	if h.State == store.StateDegraded && !h.RetryAt.IsZero() {
		lines[0] += s.Muted.Render(fmt.Sprintf(" · retry in %ds", max(0, int(h.RetryAt.Sub(now).Round(time.Second)/time.Second))))
	}
	return panel.Render(s, id.Label()+" daemon", strings.Join(lines, "\n"), width, cardH, focused)
}

func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

func (v *View) alertsPanel(s *theme.Styles, width, height int, focused bool, now time.Time) string {
	as := v.alerts()
	var lines []string
	if len(as) == 0 {
		lines = append(lines, s.LevelText(theme.LevelOK).Render("✓ ")+s.Muted.Render("No active alerts."))
	}
	inner := width - 4
	for _, a := range as {
		glyph, level := "●", theme.LevelInfo
		switch a.Severity {
		case alerts.Error:
			glyph, level = "✖", theme.LevelErr
		case alerts.Warn:
			glyph, level = "▲", theme.LevelWarn
		}
		eng, ok := s.EngineName[string(a.Engine)]
		if !ok {
			eng = s.Text
		}
		since := age(max(0, now.Sub(a.Since)))
		text := ansi.Truncate(a.Text, max(10, inner-30-len(since)), "…")
		line := s.LevelText(level).Render(glyph) + " " + eng.Render(fmt.Sprintf("%-6s", a.Engine)) + " " +
			s.Bold.Render(fmt.Sprintf("%-16s", ansi.Truncate(a.Subject, 16, "…"))) + " " + s.Text.Render(text)
		gap := inner - ansi.StringWidth(line) - len(since)
		lines = append(lines, line+strings.Repeat(" ", max(1, gap))+s.Muted.Render(since))
	}
	return panel.Render(s, fmt.Sprintf("Alerts (%d)", len(as)), strings.Join(lines, "\n"), width, height, focused)
}

func (v *View) eventsPanel(s *theme.Styles, width, height int, focused bool) string {
	var lines []string
	var events []engine.Event
	if v.snap != nil {
		events = v.snap.Events
	}
	if len(events) == 0 {
		lines = append(lines, s.Muted.Render("No events yet."))
	}
	for _, ev := range events[:min(len(events), max(0, height-2))] {
		eng, ok := s.EngineName[string(ev.Engine)]
		if !ok {
			eng = s.Text
		}
		action := strings.TrimPrefix(ev.Action, "instance-")
		lines = append(lines, s.Muted.Render(ev.Time.Local().Format("15:04:05"))+" "+eng.Render(fmt.Sprintf("%-6s", ev.Engine))+" "+
			s.Text.Render(fmt.Sprintf("%-10s", ansi.Truncate(action, 10, "…")))+" "+s.Bold.Render(ev.Name))
	}
	return panel.Render(s, "Recent events", strings.Join(lines, "\n"), width, height, focused)
}

func (v *View) shortcuts(s *theme.Styles, width, height int) string {
	km := v.keys
	items := []key.Binding{
		km.Binding(keys.Global, keys.ActNew), km.Binding(keys.Global, keys.ActImport),
		km.Binding(keys.Global, keys.ActViewTable), km.Binding(keys.Global, keys.ActViewTree),
		km.Binding(keys.Global, keys.ActViewResources), km.Binding(keys.Global, keys.ActProject),
		km.Binding(keys.Global, keys.ActTheme), km.Binding(keys.Global, keys.ActHelp),
	}
	var lines []string
	for _, b := range items {
		if b.Enabled() {
			lines = append(lines, s.Accent.Render(fmt.Sprintf("%-7s", b.Help().Key))+s.Muted.Render(b.Help().Desc))
		}
	}
	return panel.Render(s, "Shortcuts", strings.Join(lines, "\n"), width, height, false)
}
