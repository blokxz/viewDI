// Package themepicker is the theme picker modal (T). Moving the cursor previews
// a theme live; enter keeps it, esc restores the theme that was active before.
package themepicker

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// PreviewMsg applies a theme without persisting it.
type PreviewMsg struct{ ID string }

// SelectedMsg applies and persists a theme.
type SelectedMsg struct{ ID string }

// Model is the picker state.
type Model struct {
	keys     *keys.Map
	themes   []*theme.Theme
	cursor   int
	original string
}

// New opens the picker with the cursor on the active theme.
func New(km *keys.Map, themes []*theme.Theme, activeID string) *Model {
	m := &Model{keys: km, themes: themes, original: activeID}
	for i, t := range themes {
		if t.ID == activeID {
			m.cursor = i
		}
	}
	return m
}

// Scope implements modal.Modal.
func (m *Model) Scope() keys.Scope { return keys.Picker }

// Cursor returns the highlighted theme ID.
func (m *Model) Cursor() string {
	if len(m.themes) == 0 {
		return ""
	}
	return m.themes[m.cursor].ID
}

// Update implements modal.Modal.
func (m *Model) Update(msg tea.Msg) (modal.Modal, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok || len(m.themes) == 0 {
		return m, nil
	}
	move := 0
	switch m.keys.Action(kp, keys.Picker) {
	case keys.ActUp:
		move = -1
	case keys.ActDown:
		move = 1
	case keys.ActConfirm:
		id := m.Cursor()
		return m, tea.Sequence(modal.Close, func() tea.Msg { return SelectedMsg{ID: id} })
	case keys.ActCancel:
		id := m.original
		return m, tea.Sequence(modal.Close, func() tea.Msg { return PreviewMsg{ID: id} })
	}
	if move == 0 {
		return m, nil
	}
	m.cursor = (m.cursor + move + len(m.themes)) % len(m.themes)
	id := m.Cursor()
	return m, func() tea.Msg { return PreviewMsg{ID: id} }
}

// swatchTokens are previewed next to each theme name in its own colors.
var swatchTokens = []theme.Token{theme.Accent, theme.OK, theme.Warn, theme.Err, theme.Info, theme.EngineDocker, theme.EngineIncus}

// View implements modal.Modal.
func (m *Model) View(s *theme.Styles, width, height int) string {
	nameW := 0
	for _, t := range m.themes {
		nameW = max(nameW, ansi.StringWidth(t.Name))
	}
	rows := max(3, min(len(m.themes), height-12))
	start := max(0, min(m.cursor-rows/2, len(m.themes)-rows))
	var lines []string
	for i := start; i < min(len(m.themes), start+rows); i++ {
		t := m.themes[i]
		var sw strings.Builder
		for _, tok := range swatchTokens {
			sw.WriteString(lipgloss.NewStyle().Foreground(t.Color(tok)).Render("■"))
		}
		mode := "dark "
		if !t.Dark {
			mode = "light"
		}
		name := t.Name + strings.Repeat(" ", nameW-ansi.StringWidth(t.Name))
		row := " " + name + "  " + s.Muted.Render(mode) + "  " + sw.String() + " "
		if i == m.cursor {
			row = s.Selected.Render("›"+name+"  "+mode+"  ") + sw.String() + " "
		}
		lines = append(lines, row)
	}
	hint := s.Muted.Render("↑/↓ preview · enter apply · esc cancel")
	return modal.Frame(s, "Theme", strings.Join(lines, "\n")+"\n\n"+hint, width)
}
