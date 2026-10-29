// Package table is the Table view (3): every instance of both engines in one
// sortable, filterable, virtualised table. The cursor follows an instance
// key, so selection survives refreshes; rows are rebuilt only when the
// data, the filter or the sort change, never per frame.
package table

import (
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/views"
)

// maxFilter caps the filter prompt.
const maxFilter = 64

// StateChangedMsg reports a change of the persisted table state (sort,
// engine filter); the app stores it in state.json.
type StateChangedMsg struct{ State config.TableState }

// View is the Table view.
type View struct {
	keys *keys.Map
	snap *store.Snapshot

	sort    Column
	reverse bool
	engine  engine.ID // "" = all
	filter  string
	typing  bool // the filter prompt has the keyboard

	rows     []engine.Instance // filtered and sorted
	cursor   int
	selected engine.Key // key under the cursor; survives rebuilds
	offset   int        // first visible row
	page     int        // visible rows, from the last resize
	marks    map[engine.Key]bool
}

var (
	_ views.View          = (*View)(nil)
	_ views.ActionHandler = (*View)(nil)
	_ views.Selector      = (*View)(nil)
	_ views.Inputter      = (*View)(nil)
)

// New returns the view with the persisted sort and engine filter.
func New(km *keys.Map, st config.TableState) *View {
	v := &View{keys: km, sort: ColName, page: 20, marks: map[engine.Key]bool{}}
	if c, ok := columnByID(st.Sort); ok {
		v.sort = c
	}
	v.reverse = st.Reverse
	if id := engine.ID(st.Engine); slices.Contains(engine.IDs, id) {
		v.engine = id
	}
	return v
}

// Title implements views.View.
func (v *View) Title() string { return "Table" }

// Scope implements views.View.
func (v *View) Scope() keys.Scope { return keys.Table }

// ShowsInstances implements views.View.
func (v *View) ShowsInstances() bool { return true }

// Panes implements views.View.
func (v *View) Panes() int { return 1 }

// Inputting implements views.Inputter.
func (v *View) Inputting() bool { return v.typing }

// Selected implements views.Selector.
func (v *View) Selected() (engine.Instance, bool) {
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return engine.Instance{}, false
	}
	return v.rows[v.cursor], true
}

// Marked returns the marked instances, in table order.
func (v *View) Marked() []engine.Instance {
	var out []engine.Instance
	for _, in := range v.rows {
		if v.marks[in.Key()] {
			out = append(out, in)
		}
	}
	return out
}

// Rows returns the visible (filtered, sorted) instances.
func (v *View) Rows() []engine.Instance { return v.rows }

// Filter returns the filter text.
func (v *View) Filter() string { return v.filter }

// EngineFilter returns the engine filter ("" = all).
func (v *View) EngineFilter() engine.ID { return v.engine }

// SortColumn returns the sort column and direction.
func (v *View) SortColumn() (Column, bool) { return v.sort, v.reverse }

// Update implements views.View.
func (v *View) Update(msg tea.Msg) (views.View, tea.Cmd) {
	switch msg := msg.(type) {
	case views.DataMsg:
		v.snap = msg.Snapshot
		v.rebuild()
	case views.ResizeMsg:
		v.page = max(1, pageRows(msg.Height, v.cardCount()))
		v.scroll()
	}
	return v, nil
}

// HandleAction implements views.ActionHandler: "/" opens the filter prompt,
// "e" cycles the engine filter, esc clears the filter.
func (v *View) HandleAction(action string) (views.View, tea.Cmd, bool) {
	switch action {
	case keys.ActFilter:
		v.typing = true
		return v, nil, true
	case keys.ActEngine:
		v.engine = nextEngine(v.engine)
		v.rebuild()
		return v, v.stateChanged(), true
	case keys.ActBack:
		if v.filter != "" {
			v.filter = ""
			v.rebuild()
			return v, nil, true
		}
	}
	return v, nil, false
}

func nextEngine(cur engine.ID) engine.ID {
	if cur == "" {
		return engine.IDs[0]
	}
	i := slices.Index(engine.IDs, cur)
	if i < 0 || i == len(engine.IDs)-1 {
		return ""
	}
	return engine.IDs[i+1]
}

// HandleKey implements views.View.
func (v *View) HandleKey(msg tea.KeyPressMsg, _ int) (views.View, tea.Cmd, bool) {
	if v.typing {
		return v.handleInput(msg)
	}
	switch v.keys.Action(msg, keys.Table) {
	case "up":
		v.move(-1)
	case "down":
		v.move(1)
	case "page_up":
		v.move(-v.page)
	case "page_down":
		v.move(v.page)
	case "home":
		v.move(-len(v.rows))
	case "end":
		v.move(len(v.rows))
	case "sort":
		v.sort = (v.sort + 1) % numColumns
		v.rebuild()
		return v, v.stateChanged(), true
	case "sort_reverse":
		v.reverse = !v.reverse
		v.rebuild()
		return v, v.stateChanged(), true
	case "mark":
		if in, ok := v.Selected(); ok {
			if v.marks[in.Key()] {
				delete(v.marks, in.Key())
			} else {
				v.marks[in.Key()] = true
			}
			v.move(1)
		}
	default:
		return v, nil, false
	}
	return v, nil, true
}

// handleInput edits the filter prompt. Every printable key is text; enter
// keeps the filter, esc clears it, tab and ctrl+c keep their meaning.
func (v *View) handleInput(msg tea.KeyPressMsg) (views.View, tea.Cmd, bool) {
	switch v.keys.Action(msg, keys.Input) {
	case keys.ActConfirm:
		v.typing = false
		return v, nil, true
	case keys.ActCancel:
		v.typing, v.filter = false, ""
		v.rebuild()
		return v, nil, true
	case "clear":
		v.filter = ""
		v.rebuild()
		return v, nil, true
	}
	switch msg.String() {
	case "tab", "shift+tab", "ctrl+c":
		return v, nil, false
	case "backspace":
		if v.filter != "" {
			_, n := utf8.DecodeLastRuneInString(v.filter)
			v.filter = v.filter[:len(v.filter)-n]
			v.rebuild()
		}
		return v, nil, true
	}
	if t := msg.Text; t != "" && utf8.RuneCountInString(v.filter) < maxFilter && printable(t) {
		v.filter += t
		v.rebuild()
	}
	return v, nil, true // the prompt swallows everything else
}

func printable(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func (v *View) move(delta int) {
	if len(v.rows) == 0 {
		return
	}
	v.cursor = max(0, min(len(v.rows)-1, v.cursor+delta))
	v.selected = v.rows[v.cursor].Key()
	v.scroll()
}

// scroll keeps the cursor inside the visible window.
func (v *View) scroll() {
	switch {
	case v.cursor < v.offset:
		v.offset = v.cursor
	case v.cursor >= v.offset+v.page:
		v.offset = v.cursor - v.page + 1
	}
	v.offset = max(0, min(v.offset, len(v.rows)-v.page))
}

// rebuild recomputes the rows and puts the cursor back on the selected key.
func (v *View) rebuild() {
	v.rows = v.rows[:0]
	if v.snap != nil {
		q := strings.ToLower(v.filter)
		for _, in := range v.snap.Instances {
			if v.engine != "" && in.Engine != v.engine {
				continue
			}
			if q != "" && !match(q, in) {
				continue
			}
			v.rows = append(v.rows, in)
		}
	}
	sortRows(v.rows, v.sort, v.reverse)
	for k := range v.marks {
		if !slices.ContainsFunc(v.rows, func(in engine.Instance) bool { return in.Key() == k }) {
			delete(v.marks, k)
		}
	}
	if i := slices.IndexFunc(v.rows, func(in engine.Instance) bool { return in.Key() == v.selected }); i >= 0 {
		v.cursor = i
	} else {
		v.cursor = max(0, min(v.cursor, len(v.rows)-1))
	}
	if len(v.rows) > 0 {
		v.selected = v.rows[v.cursor].Key()
	}
	v.scroll()
}

func (v *View) stateChanged() tea.Cmd {
	st := config.TableState{Sort: v.sort.ID(), Reverse: v.reverse, Engine: string(v.engine)}
	return func() tea.Msg { return StateChangedMsg{State: st} }
}

// Hints implements views.View.
func (v *View) Hints(km *keys.Map) []key.Binding {
	if v.typing {
		return km.Bindings(keys.Input)
	}
	return []key.Binding{
		km.Binding(keys.Global, keys.ActOpen),
		km.Binding(keys.Global, keys.ActFilter),
		km.Binding(keys.Global, keys.ActEngine),
		km.Binding(keys.Table, "sort"),
		km.Binding(keys.Global, keys.ActProject),
	}
}
