// Package modal defines the modal layer (layer 1 of the key map): a stack of
// overlays that capture every key while open. It also provides the shared
// frame and the confirm dialog used by every destructive action.
package modal

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Modal is an overlay. While a modal is on top of the stack it receives every
// key press; only ctrl+c bypasses it (handled by the app).
type Modal interface {
	Update(msg tea.Msg) (Modal, tea.Cmd)
	// View renders the framed box; width/height are the screen size.
	View(s *theme.Styles, width, height int) string
	// Scope is the key scope whose hints the status bar shows.
	Scope() keys.Scope
}

// CloseMsg pops the top modal.
type CloseMsg struct{}

// Close is a command that pops the top modal.
func Close() tea.Msg { return CloseMsg{} }

// OpenMsg pushes a modal.
type OpenMsg struct{ Modal Modal }

// Open returns a command that pushes m.
func Open(m Modal) tea.Cmd { return func() tea.Msg { return OpenMsg{Modal: m} } }

// Frame wraps body in the modal box with a title line, capped to the screen.
func Frame(s *theme.Styles, title, body string, screenW int) string {
	maxInner := max(10, screenW-4-s.Modal.GetHorizontalFrameSize())
	content := body
	if title != "" {
		content = s.ModalTitle.Render(ansi.Truncate(title, maxInner, "…")) + "\n\n" + body
	}
	if lipgloss.Width(content) > maxInner {
		content = lipgloss.NewStyle().MaxWidth(maxInner).Render(content)
	}
	return s.Modal.Render(content)
}
