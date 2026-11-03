// Package machines is the Machines view: every machine of both engines in
// one place, drawn either as a tree grouped by app (the default) or as the
// flat sortable list. v flips between the two; both read the same snapshot.
package machines

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/views"
	"github.com/blokxz/viewDI/internal/ui/views/table"
	treeview "github.com/blokxz/viewDI/internal/ui/views/tree"
)

// Modes of the view.
const (
	ModeTree = "tree" // grouped, with an inspector (the default)
	ModeList = "list" // flat, sortable columns
)

// DetailMsg asks the app to open the Detail view of the selected machine.
type DetailMsg struct{}

// StateChangedMsg reports a change of the persisted mode.
type StateChangedMsg struct{ State config.MachinesState }

// View is the Machines view.
type View struct {
	keys  *keys.Map
	mode  string
	table *table.View
	tree  *treeview.View
}

var (
	_ views.View          = (*View)(nil)
	_ views.ActionHandler = (*View)(nil)
	_ views.Selector      = (*View)(nil)
	_ views.Inputter      = (*View)(nil)
)

// New returns the view over a list and a tree the app built.
func New(km *keys.Map, st config.MachinesState, t *table.View, tr *treeview.View) *View {
	v := &View{keys: km, mode: ModeTree, table: t, tree: tr}
	if st.Mode == ModeList {
		v.mode = ModeList
	}
	return v
}

// Mode returns the current mode.
func (v *View) Mode() string { return v.mode }

// SetMode switches the mode without reporting it (the app uses it when it
// opens an app's machines as a list).
func (v *View) SetMode(mode string) {
	if mode == ModeList || mode == ModeTree {
		v.mode = mode
	}
}

// Table returns the list the view draws in list mode.
func (v *View) Table() *table.View { return v.table }

// Tree returns the tree the view draws in tree mode.
func (v *View) Tree() *treeview.View { return v.tree }

// active is the view that has the keyboard.
func (v *View) active() views.View {
	if v.mode == ModeList {
		return v.table
	}
	return v.tree
}

// Title implements views.View.
func (v *View) Title() string { return "Machines" }

// Scope implements views.View: the keys of the mode in use.
func (v *View) Scope() keys.Scope { return v.active().Scope() }

// ShowsInstances implements views.View.
func (v *View) ShowsInstances() bool { return true }

// Panes implements views.View.
func (v *View) Panes() int { return v.active().Panes() }

// Selected implements views.Selector.
func (v *View) Selected() (engine.Instance, bool) {
	return v.active().(views.Selector).Selected()
}

// Inputting implements views.Inputter.
func (v *View) Inputting() bool {
	in, ok := v.active().(views.Inputter)
	return ok && in.Inputting()
}

// Update implements views.View: both renderers keep their data current.
func (v *View) Update(msg tea.Msg) (views.View, tea.Cmd) {
	_, c1 := v.table.Update(msg)
	_, c2 := v.tree.Update(msg)
	return v, tea.Batch(c1, c2)
}

// HandleAction implements views.ActionHandler.
func (v *View) HandleAction(action string) (views.View, tea.Cmd, bool) {
	if h, ok := v.active().(views.ActionHandler); ok {
		_, cmd, handled := h.HandleAction(action)
		return v, cmd, handled
	}
	return v, nil, false
}

// HandleKey implements views.View.
func (v *View) HandleKey(msg tea.KeyPressMsg, focus int) (views.View, tea.Cmd, bool) {
	if !v.Inputting() && v.keys.Action(msg, keys.Machines) == "flip" {
		if v.mode == ModeTree {
			v.mode = ModeList
		} else {
			v.mode = ModeTree
		}
		st := config.MachinesState{Mode: v.mode}
		return v, func() tea.Msg { return StateChangedMsg{State: st} }, true
	}
	if v.mode == ModeList && !v.Inputting() && v.keys.Action(msg, keys.Machines) == "details" {
		return v, func() tea.Msg { return DetailMsg{} }, true
	}
	_, cmd, handled := v.active().HandleKey(msg, focus)
	return v, cmd, handled
}

// Render implements views.View.
func (v *View) Render(ctx views.Context) string { return v.active().Render(ctx) }

// Hints implements views.View: the mode's hints and the flip key.
func (v *View) Hints(km *keys.Map) []key.Binding {
	flip := km.Binding(keys.Machines, "flip")
	if v.mode == ModeTree {
		flip.SetHelp(flip.Help().Key, "flat list")
	} else {
		flip.SetHelp(flip.Help().Key, "grouped tree")
	}
	return append(v.active().Hints(km), flip)
}
