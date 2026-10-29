// Package detail is the instance Detail view: Overview, Logs, Inspect,
// Network, Volumes and Resources tabs for one instance, fed by the app
// with the instance's live streams (BatchMsg) and store snapshots.
//
// The view never does I/O: the app owns the stream subscription and
// translates its batches into BatchMsg. When the instance disappears from
// a connected engine the view keeps its last data and says so.
package detail

import (
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/components/jsontree"
	"github.com/blokxz/viewDI/internal/ui/components/logview"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
)

// Tab is a detail tab.
type Tab int

// Tabs, in tab order.
const (
	TabOverview Tab = iota
	TabLogs
	TabInspect
	TabNetwork
	TabVolumes
	TabResources
	numTabs
)

var tabNames = [numTabs]string{"Overview", "Logs", "Inspect", "Network", "Volumes", "Resources"}

func (t Tab) String() string { return tabNames[t] }

// TabByName finds a tab by its name (case-insensitive).
func TabByName(name string) (Tab, bool) {
	for i, n := range tabNames {
		if strings.EqualFold(n, name) {
			return Tab(i), true
		}
	}
	return TabOverview, false
}

// StreamState is the state of one live stream (mirrors the stream registry).
type StreamState int

// Stream states.
const (
	Connecting StreamState = iota
	Live
	Ended
	Retrying
	Removed
	Failed
)

// StreamStatus describes one live stream.
type StreamStatus struct {
	State StreamState
	Err   string
	Retry time.Time
}

// Inspected is an inspect result.
type Inspected struct {
	Instance engine.Instance
	Raw      []byte
	Err      error
}

// BatchMsg carries what the instance's streams delivered since the last
// batch. Batches for another key are ignored.
type BatchMsg struct {
	Key     engine.Key
	Lines   []engine.LogLine
	Dropped int
	Samples []engine.Sample
	Inspect *Inspected
	Logs    *StreamStatus
	Stats   *StreamStatus
}

// TabChangedMsg reports the tab the user switched to, for state.json.
type TabChangedMsg struct {
	Engine engine.ID
	Tab    Tab
}

// Windows are the Resources tab's time windows (+/- cycles them).
var Windows = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

// historyLen holds the longest window at one sample per second.
const historyLen = 900

// Options configures a new view.
type Options struct {
	Tab         Tab // initial tab (the last one used for this engine)
	LogCapacity int // log lines kept; 0 = logview.DefaultCapacity
}

// View is the Detail view of one instance.
type View struct {
	keys *keys.Map
	key  engine.Key
	inst engine.Instance
	snap *store.Snapshot

	removed    bool
	inspected  bool // an Inspect result arrived
	inspectErr string
	tab        Tab

	ovCursor, ovOffset int
	revealed           map[string]bool // env names shown unmasked

	logs      *logview.Model
	logStatus StreamStatus

	tree    *jsontree.Model
	raw     []byte
	treeErr string

	statsStatus StreamStatus
	last        engine.Sample
	haveLast    bool
	points      *metrics.Ring[metrics.Point]
	ifaces      []ifaceRate
	disks       []engine.DiskUsage
	window      int

	scroll int // Network and Volumes tabs
	height int // body height of the last render
}

type ifaceRate struct {
	engine.IfaceCounters
	RxRate, TxRate float64
}

var (
	_ views.View          = (*View)(nil)
	_ views.ActionHandler = (*View)(nil)
	_ views.Selector      = (*View)(nil)
	_ views.Inputter      = (*View)(nil)
)

// New returns the detail view of in.
func New(km *keys.Map, in engine.Instance, opts Options) *View {
	return &View{
		keys: km, key: in.Key(), inst: in, tab: opts.Tab,
		revealed: map[string]bool{}, logs: logview.New(opts.LogCapacity),
		points: metrics.NewRing[metrics.Point](historyLen), window: 1, height: 20,
	}
}

// Key returns the instance key.
func (v *View) Key() engine.Key { return v.key }

// Instance returns the latest known instance data.
func (v *View) Instance() engine.Instance { return v.inst }

// Tab returns the active tab.
func (v *View) Tab() Tab { return v.tab }

// Removed reports whether the instance no longer exists.
func (v *View) Removed() bool { return v.removed }

// Logs returns the log viewer (tests).
func (v *View) Logs() *logview.Model { return v.logs }

// Tree returns the inspect tree, nil until the document arrived (tests).
func (v *View) Tree() *jsontree.Model { return v.tree }

// Title implements views.View.
func (v *View) Title() string { return v.inst.Name }

// Scope implements views.View: the active tab's component scope.
func (v *View) Scope() keys.Scope {
	switch v.tab {
	case TabLogs:
		return keys.Logs
	case TabOverview:
		return keys.Overview
	case TabInspect:
		return keys.Inspect
	}
	return keys.Detail
}

// ShowsInstances implements views.View.
func (v *View) ShowsInstances() bool { return true }

// Panes implements views.View: tab and shift+tab switch tabs instead.
func (v *View) Panes() int { return 1 }

// Selected implements views.Selector: instance actions apply to this instance.
func (v *View) Selected() (engine.Instance, bool) { return v.inst, !v.removed }

// Inputting implements views.Inputter.
func (v *View) Inputting() bool { return v.logs.Typing() || (v.tree != nil && v.tree.Typing()) }

// Update implements views.View.
func (v *View) Update(msg tea.Msg) (views.View, tea.Cmd) {
	switch msg := msg.(type) {
	case views.DataMsg:
		v.applySnapshot(msg.Snapshot)
	case BatchMsg:
		if msg.Key == v.key {
			v.applyBatch(msg)
		}
	case views.ResizeMsg:
		v.height = max(1, msg.Height-1)
	}
	return v, nil
}

func (v *View) applySnapshot(snap *store.Snapshot) {
	v.snap = snap
	if snap == nil {
		return
	}
	if in, ok := snap.Lookup(v.key); ok {
		v.inst, v.removed = merge(in, v.inst), false
		return
	}
	// Gone from a live engine: removed. A degraded engine shows stale data instead.
	if snap.Engine(v.key.Engine()).Health.State == store.StateConnected {
		v.removed = true
	}
}

// merge keeps what only Inspect knows when a list result replaces it.
func merge(in, old engine.Instance) engine.Instance {
	if len(in.Env) == 0 {
		in.Env = old.Env
	}
	if in.Extra == nil {
		in.Extra = old.Extra
	}
	return in
}

func (v *View) applyBatch(b BatchMsg) {
	if b.Inspect != nil {
		switch {
		case b.Inspect.Err != nil:
			v.inspectErr = b.Inspect.Err.Error()
			if engine.Classify(b.Inspect.Err) == engine.ClassNotFound {
				v.removed = true
			}
		default:
			v.inspectErr, v.inspected = "", true
			v.inst = b.Inspect.Instance
			v.setRaw(b.Inspect.Raw)
		}
	}
	if b.Logs != nil {
		v.logStatus = *b.Logs
	}
	if b.Stats != nil {
		v.statsStatus = *b.Stats
	}
	if len(b.Lines) > 0 || b.Dropped > 0 {
		v.logs.Append(b.Lines, b.Dropped)
	}
	for _, s := range b.Samples {
		v.addSample(s)
	}
}

// setRaw parses a new inspect document; an unchanged one keeps the tree
// (and its cursor and expansion).
func (v *View) setRaw(raw []byte) {
	if v.tree != nil && slices.Equal(raw, v.raw) {
		return
	}
	v.raw = raw
	t, err := jsontree.Parse(raw)
	if t == nil {
		v.treeErr = err.Error()
		return
	}
	v.tree, v.treeErr = t, ""
}

func (v *View) addSample(s engine.Sample) {
	if v.haveLast {
		if p, ok := metrics.Rate(v.last, s); ok {
			v.points.Push(p)
		}
		dt := s.Time.Sub(v.last.Time).Seconds()
		prev := map[string]engine.IfaceCounters{}
		for _, i := range v.last.Ifaces {
			prev[i.Name] = i
		}
		v.ifaces = v.ifaces[:0]
		for _, i := range s.Ifaces {
			r := ifaceRate{IfaceCounters: i}
			if p, ok := prev[i.Name]; ok && dt > 0 && i.Rx >= p.Rx && i.Tx >= p.Tx {
				r.RxRate, r.TxRate = float64(i.Rx-p.Rx)/dt, float64(i.Tx-p.Tx)/dt
			}
			v.ifaces = append(v.ifaces, r)
		}
	} else {
		for _, i := range s.Ifaces {
			v.ifaces = append(v.ifaces, ifaceRate{IfaceCounters: i})
		}
	}
	v.last, v.haveLast = s, true
	v.disks = s.Disks
}

// ----- keys -----

// HandleAction implements views.ActionHandler: tab/shift+tab switch tabs,
// "/" filters the log or searches the inspect tree, esc clears them, enter
// toggles an inspect node.
func (v *View) HandleAction(action string) (views.View, tea.Cmd, bool) {
	switch action {
	case keys.ActNextPane, keys.ActPrevPane:
		step := Tab(1)
		if action == keys.ActPrevPane {
			step = numTabs - 1
		}
		return v, v.setTab((v.tab + step) % numTabs), true
	case keys.ActFilter:
		switch v.tab {
		case TabLogs:
			v.logs.StartFilter()
		case TabInspect:
			if v.tree != nil {
				v.tree.StartSearch()
			}
		default:
			return v, toast.Show(theme.LevelInfo, "Filter in the Logs tab, search in the Inspect tab"), true
		}
		return v, nil, true
	case keys.ActBack:
		if f, _ := v.logs.Filter(); v.tab == TabLogs && f != "" {
			v.logs.SetFilter("")
			return v, nil, true
		}
		if v.tab == TabInspect && v.tree != nil && v.tree.Query() != "" {
			v.tree.SetQuery("")
			return v, nil, true
		}
	case keys.ActOpen:
		if v.tab == TabInspect && v.tree != nil {
			v.tree.Toggle()
		}
		return v, nil, true
	}
	return v, nil, false
}

// SetTab switches tab without reporting it (the app restores the last tab).
func (v *View) SetTab(t Tab) { v.tab, v.scroll = t%numTabs, 0 }

func (v *View) setTab(t Tab) tea.Cmd {
	v.SetTab(t)
	v.logs.ResetG()
	msg := TabChangedMsg{Engine: v.key.Engine(), Tab: v.tab}
	return func() tea.Msg { return msg }
}

// HandleKey implements views.View.
func (v *View) HandleKey(msg tea.KeyPressMsg, _ int) (views.View, tea.Cmd, bool) {
	if v.Inputting() {
		a := v.keys.Action(msg, keys.Input)
		input := v.tree.Input
		if v.logs.Typing() {
			input = v.logs.Input
		}
		return v, nil, input(msg.String(), msg.Text, a == keys.ActConfirm, a == keys.ActCancel, a == "clear")
	}
	switch v.tab {
	case TabLogs:
		if cmd, ok := v.logKey(msg); ok {
			return v, cmd, true
		}
	case TabInspect:
		if cmd, ok := v.inspectKey(msg); ok {
			return v, cmd, true
		}
	case TabOverview:
		if v.keys.Matches(msg, keys.Overview, "reveal") {
			v.reveal()
			return v, nil, true
		}
	}
	return v, nil, v.scrollKey(msg)
}

func (v *View) logKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if msg.String() == "g" {
		if v.logs.G() {
			v.logs.Top()
		}
		return nil, true
	}
	v.logs.ResetG()
	switch v.keys.Action(msg, keys.Logs) {
	case "pause":
		v.logs.TogglePause()
	case "wrap":
		v.logs.ToggleWrap()
	case "timestamps":
		v.logs.ToggleTimestamps()
	case "stream":
		v.logs.CycleSource()
	case "end":
		v.logs.End()
	default:
		switch v.keys.Action(msg, keys.Filter) {
		case "next_match":
			v.logs.NextMatch(true)
		case "prev_match":
			v.logs.NextMatch(false)
		default:
			return nil, false
		}
	}
	return nil, true
}

func (v *View) inspectKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if v.tree == nil {
		return nil, false
	}
	switch v.keys.Action(msg, keys.Inspect) {
	case "expand":
		v.tree.Expand()
	case "collapse":
		v.tree.Collapse()
	case "toggle":
		v.tree.Toggle()
	case "expand_all":
		v.tree.ExpandAll()
	case "copy":
		path := v.tree.Cursor().Path
		return tea.Batch(tea.SetClipboard(path), toast.Show(theme.LevelOK, "Copied "+path)), true
	default:
		switch v.keys.Action(msg, keys.Filter) {
		case "next_match":
			v.tree.NextMatch(true)
		case "prev_match":
			v.tree.NextMatch(false)
		default:
			return nil, false
		}
	}
	return nil, true
}

// scrollKey applies the Detail scope's movement keys to the active tab.
func (v *View) scrollKey(msg tea.KeyPressMsg) bool {
	a := v.keys.Action(msg, keys.Detail)
	if a == "" {
		return false
	}
	page := max(1, v.height-2)
	switch v.tab {
	case TabLogs:
		switch a {
		case "up":
			v.logs.Scroll(-1)
		case "down":
			v.logs.Scroll(1)
		case "page_up":
			v.logs.Page(false)
		case "page_down":
			v.logs.Page(true)
		case "home":
			v.logs.Top()
		case "end":
			v.logs.End()
		default:
			return false
		}
	case TabInspect:
		if v.tree == nil {
			return false
		}
		switch a {
		case "up":
			v.tree.Move(-1)
		case "down":
			v.tree.Move(1)
		case "page_up":
			v.tree.Page(false)
		case "page_down":
			v.tree.Page(true)
		case "home":
			v.tree.Home()
		case "end":
			v.tree.End()
		default:
			return false
		}
	case TabOverview:
		n := len(v.overviewRows(nil, time.Time{}))
		delta := map[string]int{"up": -1, "down": 1, "page_up": -page, "page_down": page, "home": -n, "end": n}[a]
		if delta == 0 {
			return false
		}
		v.ovCursor = max(0, min(n-1, v.ovCursor+delta))
	case TabResources:
		switch a {
		case "window_up":
			v.window = min(len(Windows)-1, v.window+1)
		case "window_down":
			v.window = max(0, v.window-1)
		default:
			return false
		}
	default: // Network, Volumes: plain scrolling
		delta := map[string]int{"up": -1, "down": 1, "page_up": -page, "page_down": page, "home": -1 << 20, "end": 1 << 20}[a]
		if delta == 0 {
			return false
		}
		v.scroll = max(0, v.scroll+delta) // clamped at render
	}
	return true
}

// reveal toggles the masked environment value under the overview cursor.
func (v *View) reveal() {
	rows := v.overviewRows(nil, time.Time{})
	if v.ovCursor < len(rows) && rows[v.ovCursor].secret != "" {
		name := rows[v.ovCursor].secret
		v.revealed[name] = !v.revealed[name]
	}
}

// Hints implements views.View.
func (v *View) Hints(km *keys.Map) []key.Binding {
	if v.Inputting() {
		return km.Bindings(keys.Input)
	}
	next := km.Binding(keys.Global, keys.ActNextPane)
	next.SetHelp(next.Help().Key, "next tab")
	back := km.Binding(keys.Global, keys.ActBack)
	back.SetHelp(back.Help().Key, "back")
	hints := []key.Binding{next}
	switch v.tab {
	case TabLogs:
		hints = append(hints, km.Binding(keys.Logs, "pause"), km.Binding(keys.Global, keys.ActFilter),
			km.Binding(keys.Logs, "wrap"), km.Binding(keys.Logs, "timestamps"), km.Binding(keys.Logs, "stream"),
			km.Binding(keys.Logs, "end"), km.Binding(keys.Logs, "top"))
		if f, _ := v.logs.Filter(); f != "" {
			hints = append(hints, km.Binding(keys.Filter, "next_match"), km.Binding(keys.Filter, "prev_match"))
		}
	case TabInspect:
		hints = append(hints, km.Binding(keys.Inspect, "expand"), km.Binding(keys.Inspect, "collapse"),
			km.Binding(keys.Global, keys.ActFilter), km.Binding(keys.Inspect, "copy"))
		if v.tree != nil && v.tree.Query() != "" {
			hints = append(hints, km.Binding(keys.Filter, "next_match"), km.Binding(keys.Filter, "prev_match"))
		}
	case TabOverview:
		hints = append(hints, km.Binding(keys.Overview, "reveal"))
	case TabResources:
		hints = append(hints, km.Binding(keys.Detail, "window_up"), km.Binding(keys.Detail, "window_down"))
	}
	return append(hints, back)
}
