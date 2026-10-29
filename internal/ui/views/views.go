// Package views defines the contract between the app router and the six main
// views (1–6). Each view lives in its own package under views/.
package views

import (
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
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
	Focus  int       // focused pane, 0 ≤ Focus < Panes()
	Now    time.Time // render time for relative times (uptime, countdowns)
}

// DataMsg delivers a new store snapshot. The app sends it to every view
// whenever the store changes, so hidden views stay current.
type DataMsg struct{ Snapshot *store.Snapshot }

// ResizeMsg tells views the size of the body area.
type ResizeMsg struct{ Width, Height int }

// ActionHandler is implemented by views that act on global actions (the
// filter prompt, the engine filter, back). The app offers them the action
// before its own handling.
type ActionHandler interface {
	HandleAction(action string) (v View, cmd tea.Cmd, handled bool)
}

// Selector is implemented by views with an instance cursor; instance
// actions and enter apply to the selected instance.
type Selector interface {
	Selected() (engine.Instance, bool)
}

// Inputter is implemented by views that can be in text-input mode, which
// captures every printable key (layer 1 of the key map).
type Inputter interface {
	Inputting() bool
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
