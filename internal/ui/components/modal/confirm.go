package modal

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Confirm asks a yes/no question. Focus starts on Cancel so that a stray
// enter never confirms a destructive action; "y" confirms directly.
type Confirm struct {
	keys      *keys.Map
	title     string
	body      string
	label     string // confirm button label, e.g. "Delete"
	danger    bool
	onConfirm tea.Msg
	focusOK   bool
}

// NewConfirm builds a confirm dialog. onConfirm is emitted (after the dialog
// closes) when the user confirms. danger styles the confirm button with the
// err token.
func NewConfirm(km *keys.Map, title, body, label string, danger bool, onConfirm tea.Msg) *Confirm {
	return &Confirm{keys: km, title: title, body: body, label: label, danger: danger, onConfirm: onConfirm}
}

// Scope implements Modal.
func (c *Confirm) Scope() keys.Scope { return keys.Confirm }

// Update implements Modal.
func (c *Confirm) Update(msg tea.Msg) (Modal, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return c, nil
	}
	switch c.keys.Action(kp, keys.Confirm) {
	case keys.ActConfirm:
		return c, c.confirm()
	case keys.ActCancel:
		return c, Close
	case keys.ActActivate:
		if c.focusOK {
			return c, c.confirm()
		}
		return c, Close
	case keys.ActFocus:
		c.focusOK = !c.focusOK
	}
	return c, nil
}

func (c *Confirm) confirm() tea.Cmd {
	msg := c.onConfirm
	return tea.Sequence(Close, func() tea.Msg { return msg })
}

// FocusOnConfirm reports whether the confirm button has focus (for tests).
func (c *Confirm) FocusOnConfirm() bool { return c.focusOK }

// View implements Modal.
func (c *Confirm) View(s *theme.Styles, width, _ int) string {
	okStyle := s.Button
	if c.danger {
		okStyle = s.ButtonDanger
	}
	cancel, ok := s.Muted.Padding(0, 1), okStyle.Faint(true)
	if c.focusOK {
		ok = okStyle
	} else {
		cancel = s.Selected.Padding(0, 1)
	}
	buttons := lipgloss.JoinHorizontal(lipgloss.Center, cancel.Render("Cancel"), "  ", ok.Render(c.label))
	hint := s.Muted.Render("y confirm · n/esc cancel · tab switch")
	body := c.body + "\n\n" + buttons + "\n\n" + hint
	return Frame(s, c.title, body, width)
}
