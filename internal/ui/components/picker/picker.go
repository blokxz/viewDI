// Package picker is a generic single-choice list modal (e.g. the Incus
// project selector, P). enter selects, esc cancels.
package picker

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Item is one choice.
type Item struct {
	ID     string // returned in SelectedMsg
	Label  string
	Detail string // muted text after the label
}

// SelectedMsg reports the choice; Kind tells pickers apart.
type SelectedMsg struct {
	Kind string
	ID   string
}

// Model is the picker state.
type Model struct {
	keys   *keys.Map
	kind   string
	title  string
	items  []Item
	cursor int
}

// New opens a picker with the cursor on the item whose ID is current.
func New(km *keys.Map, kind, title string, items []Item, current string) *Model {
	m := &Model{keys: km, kind: kind, title: title, items: items}
	for i, it := range items {
		if it.ID == current {
			m.cursor = i
		}
	}
	return m
}

// Scope implements modal.Modal.
func (m *Model) Scope() keys.Scope { return keys.Picker }

// Cursor returns the highlighted item ID.
func (m *Model) Cursor() string {
	if len(m.items) == 0 {
		return ""
	}
	return m.items[m.cursor].ID
}

// Update implements modal.Modal.
func (m *Model) Update(msg tea.Msg) (modal.Modal, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch m.keys.Action(kp, keys.Picker) {
	case keys.ActUp:
		if len(m.items) > 0 {
			m.cursor = (m.cursor - 1 + len(m.items)) % len(m.items)
		}
	case keys.ActDown:
		if len(m.items) > 0 {
			m.cursor = (m.cursor + 1) % len(m.items)
		}
	case keys.ActConfirm:
		if len(m.items) == 0 {
			return m, modal.Close
		}
		sel := SelectedMsg{Kind: m.kind, ID: m.Cursor()}
		return m, tea.Sequence(modal.Close, func() tea.Msg { return sel })
	case keys.ActCancel:
		return m, modal.Close
	}
	return m, nil
}

// View implements modal.Modal.
func (m *Model) View(s *theme.Styles, width, height int) string {
	labelW := 0
	for _, it := range m.items {
		labelW = max(labelW, ansi.StringWidth(it.Label))
	}
	rows := max(3, min(len(m.items), height-12))
	start := max(0, min(m.cursor-rows/2, len(m.items)-rows))
	var lines []string
	for i := start; i < min(len(m.items), start+rows); i++ {
		it := m.items[i]
		label := it.Label + strings.Repeat(" ", labelW-ansi.StringWidth(it.Label))
		if i == m.cursor {
			lines = append(lines, s.Selected.Render("›"+label+" ")+" "+s.Muted.Render(it.Detail))
			continue
		}
		lines = append(lines, " "+s.Text.Render(label)+"  "+s.Muted.Render(it.Detail))
	}
	if len(m.items) == 0 {
		lines = append(lines, s.Muted.Render("nothing to choose from yet"))
	}
	hint := s.Muted.Render("↑/↓ move · enter select · esc cancel")
	return modal.Frame(s, m.title, strings.Join(lines, "\n")+"\n\n"+hint, width)
}
