// Package help is the help overlay (?): the effective key map for the global
// scope, instance actions and the active view's scope, scrollable.
package help

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Model is the help overlay state.
type Model struct {
	keys          *keys.Map
	styles        *theme.Styles
	scopes        []keys.Scope
	width, height int
	offset        int
}

// New shows the bindings of scopes, in order, for a screen of width×height.
// Scopes without bindings are skipped.
func New(km *keys.Map, s *theme.Styles, width, height int, scopes ...keys.Scope) *Model {
	return &Model{keys: km, styles: s, scopes: scopes, width: width, height: height}
}

// Scope implements modal.Modal.
func (m *Model) Scope() keys.Scope { return keys.Help }

// Update implements modal.Modal.
func (m *Model) Update(msg tea.Msg) (modal.Modal, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		switch m.keys.Action(msg, keys.Help) {
		case keys.ActClose:
			return m, modal.Close
		case keys.ActUp:
			m.offset--
		case keys.ActDown:
			m.offset++
		}
	}
	lines, visible := m.layout(m.styles, m.width, m.height)
	m.offset = max(0, min(m.offset, len(lines)-visible))
	return m, nil
}

func (m *Model) sections(s *theme.Styles) []string {
	var out []string
	for _, sc := range m.scopes {
		bs := m.keys.Bindings(sc)
		if len(bs) == 0 {
			continue
		}
		info, _ := keys.ScopeByName(sc)
		keyW := 0
		for _, b := range bs {
			keyW = max(keyW, ansi.StringWidth(b.Help().Key))
		}
		lines := []string{s.PanelTitle.Render(info.Title)}
		for _, b := range bs {
			k := b.Help().Key
			lines = append(lines, s.Accent.Render(k)+strings.Repeat(" ", keyW-ansi.StringWidth(k))+"  "+s.Text.Render(b.Help().Desc))
		}
		out = append(out, strings.Join(lines, "\n"))
	}
	return out
}

// layout returns the body lines and how many fit on screen. Sections are laid
// out in two columns when the screen is wide enough.
func (m *Model) layout(s *theme.Styles, width, height int) (lines []string, visible int) {
	secs := m.sections(s)
	var body string
	if width >= 100 && len(secs) > 1 {
		// Greedy balance: fill the left column up to half the total height.
		total := 0
		for _, sec := range secs {
			total += lipgloss.Height(sec) + 1
		}
		var left, right []string
		h := 0
		for _, sec := range secs {
			if h < total/2 {
				left = append(left, sec)
				h += lipgloss.Height(sec) + 1
			} else {
				right = append(right, sec)
			}
		}
		col := lipgloss.NewStyle().PaddingRight(4)
		body = lipgloss.JoinHorizontal(lipgloss.Top, col.Render(strings.Join(left, "\n\n")), strings.Join(right, "\n\n"))
	} else {
		body = strings.Join(secs, "\n\n")
	}

	return strings.Split(body, "\n"), max(3, height-10)
}

// View implements modal.Modal.
func (m *Model) View(s *theme.Styles, width, height int) string {
	lines, visible := m.layout(s, width, height)
	end := min(len(lines), m.offset+visible)
	view := strings.Join(lines[m.offset:end], "\n")
	more := ""
	if end < len(lines) || m.offset > 0 {
		more = " · ↑/↓ scroll"
	}
	return modal.Frame(s, "Keys", view+"\n\n"+s.Muted.Render("esc/?/q close"+more), width)
}
