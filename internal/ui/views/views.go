// Package views defines the contract between the app router and the six main
// views (1–6). Each view lives in its own package under views/.
package views

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Context is what a view needs to render.
type Context struct {
	Styles *theme.Styles
	Keys   *keys.Map
	Width  int
	Height int
	Mode   layout.Mode
	Focus  int // focused pane, 0 ≤ Focus < Panes()
}

// View is one main view. Views are values owned by the app; Update and
// HandleKey return the updated view.
type View interface {
	// Title is the tab label.
	Title() string
	// Scope is the view's key scope ("" if it has no local keys).
	Scope() keys.Scope
	// ShowsInstances reports whether instance action keys apply in this view.
	ShowsInstances() bool
	// Panes is the number of focusable panes, cycled with tab/shift+tab.
	Panes() int
	// Update receives every non-key message.
	Update(msg tea.Msg) (View, tea.Cmd)
	// HandleKey gets keys not consumed by a modal (layers 2–3). handled=false
	// passes the key on to the global layer.
	HandleKey(msg tea.KeyPressMsg, focus int) (v View, cmd tea.Cmd, handled bool)
	// Render draws exactly ctx.Width×ctx.Height cells.
	Render(ctx Context) string
	// Hints are the view's key hints for the status bar.
	Hints(km *keys.Map) []key.Binding
}
