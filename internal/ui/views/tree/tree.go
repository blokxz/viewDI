// Package tree is the Tree view (4): engines, their networks (or projects,
// or Compose projects) and the instances in them, as a navigable tree with
// an inspector that follows the cursor. The inspector reads the store
// snapshot only: moving the cursor never calls a daemon.
package tree

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/components/form"
	"github.com/blokxz/viewDI/internal/ui/components/picker"
	"github.com/blokxz/viewDI/internal/ui/components/tree"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/views"
)

// maxFilter caps the filter prompt.
const maxFilter = 64

// StateChangedMsg reports a change of the persisted tree state (grouping,
// expansion); the app stores it in state.json.
type StateChangedMsg struct{ State config.TreeState }

// LabsChangedMsg carries the labs after the user edited them; the app
// saves them in labs.json.
type LabsChangedMsg struct{ Labs config.Labs }

// inspectMsg ends a debounce wait: the inspector catches up with the cursor.
type inspectMsg struct{ seq uint64 }

// frameMsg advances the link animation; gen tells a stale tick chain from
// the current one.
type frameMsg struct {
	gen uint64
	at  time.Time
}

// Options configures the view.
type Options struct {
	State config.TreeState
	// ASCII draws ASCII icons and guides.
	ASCII bool
	// InspectDelay debounces the inspector while the cursor moves; 0
	// updates it at once.
	InspectDelay time.Duration
	// Labs are the user's labs (labs.json).
	Labs config.Labs
	// Animation is the frame interval of the dots on lab links; 0 turns
	// the animation off.
	Animation time.Duration
}

// View is the Tree view.
type View struct {
	keys  *keys.Map
	opts  Options
	snap  *store.Snapshot
	icons icons

	grouping Grouping
	trees    [numGroupings]*tree.Model // one per grouping: own expansion and cursor

	filter string
	typing bool

	inspected string // node the inspector shows (lags the cursor while debouncing)
	seq       uint64
	page      int

	labs config.Labs
	menu labTarget                   // what the open lab menu acts on
	pick map[string]config.LabMember // the open link picker's choices

	// byMember indexes the machines of snapshot byMemberOf by lab member.
	byMember   map[config.LabMember]engine.Key
	byMemberOf *store.Snapshot

	// Link animation: it ticks only while the view is visible in the lab
	// grouping with an active lane on it.
	visible bool
	ticking bool
	gen     uint64
	frame   int64
}

var (
	_ views.View          = (*View)(nil)
	_ views.ActionHandler = (*View)(nil)
	_ views.Selector      = (*View)(nil)
	_ views.Inputter      = (*View)(nil)
)

// New returns the view with the persisted grouping and expansion.
func New(km *keys.Map, opts Options) *View {
	v := &View{keys: km, opts: opts, icons: unicodeIcons, page: 10, labs: opts.Labs.Clone()}
	if opts.ASCII {
		v.icons = asciiIcons
	}
	if g, ok := groupingByID(opts.State.Grouping); ok {
		v.grouping = g
	}
	for g := range numGroupings {
		toggled := opts.State.Toggled[g.ID()]
		if len(toggled) > config.MaxTreeToggled {
			toggled = toggled[len(toggled)-config.MaxTreeToggled:]
		}
		v.trees[g] = tree.New(toggled)
		v.trees[g].SetRoots(build(nil, g, &v.labs))
	}
	return v
}

func (v *View) t() *tree.Model { return v.trees[v.grouping] }

// Title implements views.View.
func (v *View) Title() string { return "Tree" }

// Scope implements views.View.
func (v *View) Scope() keys.Scope { return keys.Tree }

// ShowsInstances implements views.View.
func (v *View) ShowsInstances() bool { return true }

// Panes implements views.View.
func (v *View) Panes() int { return 1 }

// Inputting implements views.Inputter.
func (v *View) Inputting() bool { return v.typing }

// Grouping returns the current grouping.
func (v *View) Grouping() Grouping { return v.grouping }

// Filter returns the filter text.
func (v *View) Filter() string { return v.filter }

// Rows returns the visible rows.
func (v *View) Rows() []tree.Row { return v.t().Rows() }

// Cursor returns the ID of the node under the cursor.
func (v *View) Cursor() string {
	if n, ok := v.t().Current(); ok {
		return n.ID
	}
	return ""
}

// Selected implements views.Selector: the instance under the cursor, or the
// one owning the leaf under it.
func (v *View) Selected() (engine.Instance, bool) {
	n, ok := v.t().Current()
	if !ok || v.snap == nil {
		return engine.Instance{}, false
	}
	it := data(n)
	switch it.kind {
	case kInstance, kPort, kMount, kAlso:
		return v.snap.Lookup(it.key)
	}
	return engine.Instance{}, false
}

// Labs returns the labs.
func (v *View) Labs() config.Labs { return v.labs.Clone() }

// Update implements views.View.
func (v *View) Update(msg tea.Msg) (views.View, tea.Cmd) {
	switch msg := msg.(type) {
	case views.DataMsg:
		v.snap = msg.Snapshot
		v.rebuild()
	case views.ResizeMsg:
		v.page = max(1, treeRows(msg.Width, msg.Height))
		for _, t := range v.trees {
			t.SetPage(v.page)
		}
	case inspectMsg:
		if msg.seq == v.seq {
			v.inspected = v.Cursor()
		}
	case frameMsg:
		return v, v.onFrame(msg)
	case picker.SelectedMsg:
		return v, v.labPicked(msg)
	case form.SubmittedMsg:
		return v, v.labSubmitted(msg)
	case labDeleteMsg:
		return v, v.labDelete(msg.lab)
	}
	return v, nil
}

// rebuild rebuilds the current grouping's tree; the others are rebuilt
// when shown.
func (v *View) rebuild() {
	t := v.t()
	t.SetRoots(build(v.snap, v.grouping, &v.labs))
	switch {
	case v.filter != "":
		t.SetFilter(v.matcher())
	case t.Filtering(): // cleared while another grouping was shown
		t.SetFilter(nil)
	}
	v.inspected = v.Cursor()
}

// HandleAction implements views.ActionHandler: "/" opens the filter prompt,
// esc clears the filter.
func (v *View) HandleAction(action string) (views.View, tea.Cmd, bool) {
	switch action {
	case keys.ActFilter:
		v.typing = true
		return v, nil, true
	case keys.ActBack:
		if v.filter != "" {
			v.setFilter("")
			return v, nil, true
		}
	}
	return v, nil, false
}

// HandleKey implements views.View.
func (v *View) HandleKey(msg tea.KeyPressMsg, _ int) (views.View, tea.Cmd, bool) {
	if v.typing {
		return v.handleInput(msg)
	}
	t := v.t()
	if v.filter != "" {
		switch v.keys.Action(msg, keys.Filter) {
		case "next_match":
			t.NextMatch()
			return v, v.moved(), true
		case "prev_match":
			t.PrevMatch()
			return v, v.moved(), true
		}
	}
	changed := false
	switch v.keys.Action(msg, keys.Tree) {
	case "up":
		t.Move(-1)
	case "down":
		t.Move(1)
	case "page_up":
		t.PageUp()
	case "page_down":
		t.PageDown()
	case "home":
		t.Home()
	case "end":
		t.End()
	case "expand":
		changed = t.Right()
	case "collapse":
		changed = t.Left()
	case "prev_sibling":
		t.PrevSibling()
	case "next_sibling":
		t.NextSibling()
	case "toggle":
		changed = t.Toggle()
	case "expand_all":
		changed = t.ExpandAll()
	case "collapse_all":
		changed = t.CollapseAll()
	case "next_occurrence":
		v.nextOccurrence()
	case "grouping":
		v.cycleGrouping()
		return v, tea.Batch(v.moved(), v.stateChanged()), true
	case "lab_menu":
		return v, v.labMenu(), true
	default:
		return v, nil, false
	}
	cmd := v.moved()
	if changed {
		cmd = tea.Batch(cmd, v.stateChanged())
	}
	return v, cmd, true
}

// moved starts (or restarts) the inspector's debounce.
func (v *View) moved() tea.Cmd {
	v.seq++
	if v.opts.InspectDelay <= 0 {
		v.inspected = v.Cursor()
		return nil
	}
	seq := v.seq
	return tea.Tick(v.opts.InspectDelay, func(time.Time) tea.Msg { return inspectMsg{seq: seq} })
}

// nextOccurrence moves to the next node of the same instance (an instance
// on several networks is listed under each), opening its ancestors.
func (v *View) nextOccurrence() {
	t := v.t()
	n, ok := t.Current()
	if !ok || data(n).kind != kInstance {
		return
	}
	k := data(n).key
	var all []string
	var walk func(ns []*tree.Node)
	walk = func(ns []*tree.Node) {
		for _, c := range ns {
			if it := data(c); it.kind == kInstance && it.key == k {
				all = append(all, c.ID)
			}
			walk(c.Children)
		}
	}
	root, _ := t.Node(rootID)
	walk([]*tree.Node{root})
	if len(all) < 2 {
		return
	}
	i := slices.Index(all, n.ID)
	for step := 1; step < len(all); step++ {
		if t.Select(all[(i+step)%len(all)]) {
			return
		}
	}
}

// cycleGrouping switches to the next grouping, keeping the cursor on the
// same instance (or the same engine) when it can.
func (v *View) cycleGrouping() {
	var k engine.Key
	var eng engine.ID
	if n, ok := v.t().Current(); ok {
		k, eng = data(n).key, data(n).engine
	}
	v.grouping = (v.grouping + 1) % numGroupings
	v.rebuild()
	t := v.t()
	if k != "" {
		var hit string
		var walk func(ns []*tree.Node)
		walk = func(ns []*tree.Node) {
			for _, c := range ns {
				if hit != "" {
					return
				}
				if it := data(c); it.kind == kInstance && it.key == k && !it.secondary {
					hit = c.ID
				}
				walk(c.Children)
			}
		}
		root, _ := t.Node(rootID)
		walk([]*tree.Node{root})
		if hit != "" && t.Select(hit) {
			return
		}
	}
	if eng != "" {
		t.Select(string(eng))
	}
}

// handleInput edits the filter prompt like the Table view's.
func (v *View) handleInput(msg tea.KeyPressMsg) (views.View, tea.Cmd, bool) {
	switch v.keys.Action(msg, keys.Input) {
	case keys.ActConfirm:
		v.typing = false
		return v, nil, true
	case keys.ActCancel:
		v.typing = false
		v.setFilter("")
		return v, v.moved(), true
	case "clear":
		v.setFilter("")
		return v, v.moved(), true
	}
	switch msg.String() {
	case "tab", "shift+tab", "ctrl+c":
		return v, nil, false
	case "backspace":
		if v.filter != "" {
			_, n := utf8.DecodeLastRuneInString(v.filter)
			v.setFilter(v.filter[:len(v.filter)-n])
		}
		return v, v.moved(), true
	}
	if t := msg.Text; t != "" && utf8.RuneCountInString(v.filter) < maxFilter && printable(t) {
		v.setFilter(v.filter + t)
		return v, v.moved(), true
	}
	return v, nil, true
}

func printable(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func (v *View) setFilter(f string) {
	v.filter = f
	if f == "" {
		// Clearing the filter keeps the node found under the cursor in view,
		// opening its ancestors if the user's folds would hide it.
		id := v.Cursor()
		v.t().SetFilter(nil)
		if id != "" {
			v.t().Select(id)
		}
	} else {
		v.t().SetFilter(v.matcher())
		v.t().FirstMatch()
	}
	v.inspected = v.Cursor()
}

// matcher matches instances by name, image, network or IP, and groups by
// name (case-insensitive substrings).
func (v *View) matcher() func(*tree.Node) bool {
	q := strings.ToLower(v.filter)
	snap := v.snap
	return func(n *tree.Node) bool {
		it := data(n)
		switch it.kind {
		case kNetwork, kProject, kCompose, kLab, kMissing:
			return strings.Contains(strings.ToLower(it.name), q)
		case kInstance:
			in, ok := snap.Lookup(it.key)
			if !ok {
				return false
			}
			if strings.Contains(strings.ToLower(in.Name), q) || strings.Contains(strings.ToLower(in.Image), q) {
				return true
			}
			for _, a := range in.Networks {
				if strings.Contains(strings.ToLower(a.Network), q) || strings.Contains(a.IPv4, q) || strings.Contains(strings.ToLower(a.IPv6), q) {
					return true
				}
			}
		}
		return false
	}
}

func (v *View) stateChanged() tea.Cmd {
	st := config.TreeState{Grouping: v.grouping.ID(), Toggled: map[string][]string{}}
	for g, t := range v.trees {
		ids := t.Toggled()
		if len(ids) > config.MaxTreeToggled {
			ids = ids[len(ids)-config.MaxTreeToggled:]
		}
		if len(ids) > 0 {
			st.Toggled[Grouping(g).ID()] = ids
		}
	}
	if len(st.Toggled) == 0 {
		st.Toggled = nil
	}
	return func() tea.Msg { return StateChangedMsg{State: st} }
}

// Hints implements views.View.
func (v *View) Hints(km *keys.Map) []key.Binding {
	if v.typing {
		return km.Bindings(keys.Input)
	}
	hints := []key.Binding{
		km.Binding(keys.Global, keys.ActOpen),
		km.Binding(keys.Tree, "toggle"),
		km.Binding(keys.Global, keys.ActFilter),
		km.Binding(keys.Tree, "grouping"),
	}
	if in, ok := v.Selected(); ok && len(in.Networks) > 1 {
		hints = append(hints, km.Binding(keys.Tree, "next_occurrence"))
	}
	if v.filter != "" {
		hints = append(hints, km.Binding(keys.Filter, "next_match"))
	}
	if v.grouping == ByLab {
		// Elsewhere L still works and is listed in help; the footer keeps
		// room for the actions.
		hints = append(hints, km.Binding(keys.Tree, "lab_menu"))
	}
	return hints
}
