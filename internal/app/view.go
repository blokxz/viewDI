package app

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/blokxz/viewDI/internal/ui/components/banner"
	"github.com/blokxz/viewDI/internal/ui/components/debugoverlay"
	"github.com/blokxz/viewDI/internal/ui/components/header"
	"github.com/blokxz/viewDI/internal/ui/components/statusbar"
	"github.com/blokxz/viewDI/internal/ui/components/tabbar"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
)

// View implements tea.Model.
func (m Model) View() tea.View {
	if m.opts.Debug {
		start := time.Now()
		defer func() { m.stats.RecordFrame(time.Since(start)) }()
	}
	v := tea.NewView(m.Render())
	v.AltScreen = true
	v.ReportFocus = true
	if m.opts.TerminalColors {
		v.BackgroundColor, v.ForegroundColor = m.styles.TerminalColors()
	}
	v.WindowTitle = "viewDI — " + m.views[m.active].Title()
	return v
}

// Render returns the full screen content (exactly width×height cells once
// the size is known). Golden tests compare this string.
func (m Model) Render() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	mode := layout.ModeFor(m.width, m.height)
	if mode == layout.TooSmall {
		return m.renderTooSmall()
	}

	s := m.styles
	top := []string{
		header.Render(s, m.headerData(), m.width),
		tabbar.Render(s, m.tabs(), m.active, s.Muted.Render("theme: ")+s.Text.Render(s.Theme.Name)+" ", m.width),
	}
	if m.banner.Visible() {
		top = append(top, banner.Render(s, m.banner, m.width))
	}
	status := statusbar.Render(s, m.hints(), m.pinnedHints(), m.indicators(), m.width)
	bodyH := m.height - len(top) - 1

	view := m.views[m.active]
	body := view.Render(views.Context{
		Styles: s, Keys: m.keys, Width: m.width, Height: bodyH, Mode: mode, Focus: m.focus,
	})
	base := strings.Join(top, "\n") + "\n" + body + "\n" + status
	return m.overlay(base, len(top), bodyH)
}

// overlay composes toasts, the debug overlay and the top modal over base.
func (m Model) overlay(base string, bodyTop, bodyH int) string {
	layers := []*lipgloss.Layer{lipgloss.NewLayer(base)}

	if t := m.toasts.View(m.styles, m.width/2); t != "" {
		w, h := lipgloss.Size(t)
		y := max(bodyTop, bodyTop+bodyH-h)
		layers = append(layers, lipgloss.NewLayer(t).X(max(0, m.width-w-1)).Y(y).Z(1))
	}
	if m.showDebug {
		d := debugoverlay.View(m.styles, m.stats.Snapshot(), layout.ModeFor(m.width, m.height).String(), m.width, m.height)
		w, _ := lipgloss.Size(d)
		layers = append(layers, lipgloss.NewLayer(d).X(max(0, m.width-w-1)).Y(bodyTop).Z(2))
	}
	if n := len(m.modals); n > 0 {
		box := m.modals[n-1].View(m.styles, m.width, m.height)
		w, h := lipgloss.Size(box)
		layers = append(layers, lipgloss.NewLayer(box).X(max(0, (m.width-w)/2)).Y(max(0, (m.height-h)/2)).Z(3))
	}
	if len(layers) == 1 {
		return base
	}
	canvas := lipgloss.NewCanvas(m.width, m.height)
	canvas.Compose(lipgloss.NewCompositor(layers...))
	// The canvas trims trailing blanks; pad back to the full frame.
	return layout.Fit(canvas.Render(), m.width, m.height)
}

func (m Model) renderTooSmall() string {
	s := m.styles
	msg := strings.Join([]string{
		s.LevelText(theme.LevelWarn).Render("Terminal too small"),
		"",
		s.Text.Render(fmt.Sprintf("%d×%d — viewDI needs at least %d×%d", m.width, m.height, layout.MinWidth, layout.MinHeight)),
		s.Muted.Render("enlarge the window, or press q to quit"),
	}, "\n")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, msg)
}

func (m Model) headerData() header.Data {
	state := "—"
	if m.opts.Mock != "" {
		state = "mock"
	}
	return header.Data{Engines: []header.Engine{
		{ID: "docker", Label: "Docker", State: state, Level: theme.LevelInfo},
		{ID: "incus", Label: "Incus", State: state, Level: theme.LevelInfo},
	}}
}

func (m Model) tabs() []tabbar.Tab {
	tabs := make([]tabbar.Tab, len(m.views))
	for i, v := range m.views {
		tabs[i] = tabbar.Tab{Key: fmt.Sprint(i + 1), Title: v.Title()}
	}
	return tabs
}

func (m Model) indicators() []string {
	var out []string
	if m.opts.Mock != "" {
		out = append(out, "MOCK "+m.opts.Mock)
	}
	if m.opts.ReadOnly {
		out = append(out, "READ-ONLY")
	}
	return out
}

// globalHints are the global actions advertised in the status bar, after the
// view's own hints and the instance actions; help is pinned separately.
var globalHints = []string{keys.ActNextPane, keys.ActTheme, keys.ActQuit}

// pinnedHints stay visible at any width: help lists everything else.
func (m Model) pinnedHints() []key.Binding {
	if len(m.modals) > 0 {
		return nil
	}
	return []key.Binding{m.keys.Binding(keys.Global, keys.ActHelp)}
}

// hints returns the status bar hints for the current layer.
func (m Model) hints() []key.Binding {
	km := m.keys
	if n := len(m.modals); n > 0 {
		return km.Bindings(m.modals[n-1].Scope())
	}
	view := m.views[m.active]
	hints := view.Hints(km)
	if view.ShowsInstances() && !m.opts.ReadOnly {
		hints = append(hints, km.Bindings(keys.Instance)...)
	}
	for _, a := range globalHints {
		hints = append(hints, km.Binding(keys.Global, a))
	}
	return hints
}
