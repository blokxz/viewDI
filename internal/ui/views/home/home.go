// Package home is the Home view (1): one sentence of status, what needs the
// user, and one card per app. It renders what the app derived (apps.Derive,
// apps.Needs) and a store snapshot; it never reads a daemon.
package home

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/apps"
	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/metrics"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/panel"
	"github.com/blokxz/viewDI/internal/ui/components/sparkline"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
	"github.com/blokxz/viewDI/internal/ui/views/resources"
)

// DerivedMsg delivers what the app derived from the store: the apps (in the
// Home's order) and the needs-you items. The app sends it whenever the data,
// the labs or the stopped-on-their-own set change.
type DerivedMsg struct {
	Apps  []apps.App
	Needs []apps.Item
}

// ActionMsg asks the app to run an action of a needs-you item.
type ActionMsg struct {
	Item   apps.Item
	Action string
}

// OpenAppMsg asks the app to show the machines of an app.
type OpenAppMsg struct{ App apps.App }

// StateChangedMsg reports a change of the persisted density or pins.
type StateChangedMsg struct{ State config.HomeState }

// Densities the grid can take.
const (
	DensityCards  = "cards"
	DensityRows   = "rows"
	DensityMosaic = "mosaic"
)

var densities = []string{"", DensityCards, DensityRows, DensityMosaic} // "" = automatic

const (
	cardW      = 30
	cardH      = 5
	maxNeeds   = 5
	maxFilter  = 64
	rowColW    = 56
	mosaicColW = 38
)

// Panes of the view.
const (
	paneNeeds = iota
	paneApps
)

// View is the Home view.
type View struct {
	keys  *keys.Map
	snap  *store.Snapshot
	apps  []apps.App
	needs []apps.Item
	state config.HomeState

	cursorNeed int
	needID     string
	cursorApp  int
	appID      string
	offset     int // first visible row of the grid

	filter string
	typing bool

	expanded bool
	res      *resources.View

	// Set by Render: what the last frame showed, for navigation.
	cols    int
	rowsVis int
	shown   string // the density the last frame used
}

var (
	_ views.View               = (*View)(nil)
	_ views.ActionHandler      = (*View)(nil)
	_ views.FocusActionHandler = (*View)(nil)
	_ views.Inputter           = (*View)(nil)
)

// New returns the view with the persisted density and pins.
func New(km *keys.Map, st config.HomeState) *View {
	if !slices.Contains(densities, st.Density) {
		st.Density = ""
	}
	return &View{keys: km, state: st, res: resources.New(km), cols: 1, rowsVis: 1}
}

// Expanded reports whether the usage charts fill the view.
func (v *View) Expanded() bool { return v.expanded }

// Title implements views.View.
func (v *View) Title() string { return "Home" }

// Scope implements views.View.
func (v *View) Scope() keys.Scope { return keys.Home }

// ShowsInstances implements views.View.
func (v *View) ShowsInstances() bool { return false }

// Panes implements views.View: the needs-you list and the apps.
func (v *View) Panes() int { return 2 }

// Inputting implements views.Inputter.
func (v *View) Inputting() bool { return v.typing }

// Density returns the chosen density ("" is automatic).
func (v *View) Density() string { return v.state.Density }

// Apps returns the apps as shown: pinned first, filtered.
func (v *View) Apps() []apps.App { return v.display() }

// Update implements views.View.
func (v *View) Update(msg tea.Msg) (views.View, tea.Cmd) {
	switch msg := msg.(type) {
	case views.DataMsg:
		v.snap = msg.Snapshot
		v.res.Update(msg)
	case views.ResizeMsg:
		v.res.Update(msg)
	case DerivedMsg:
		v.apps, v.needs = msg.Apps, msg.Needs
		v.restore()
	}
	return v, nil
}

// restore puts both cursors back on the item they were on.
func (v *View) restore() {
	if i := slices.IndexFunc(v.needs, func(it apps.Item) bool { return it.ID == v.needID }); i >= 0 {
		v.cursorNeed = i
	} else {
		v.cursorNeed = max(0, min(v.cursorNeed, len(v.needs)-1))
	}
	if len(v.needs) > 0 {
		v.needID = v.needs[v.cursorNeed].ID
	}
	list := v.display()
	if i := slices.IndexFunc(list, func(a apps.App) bool { return a.ID == v.appID }); i >= 0 {
		v.cursorApp = i
	} else {
		v.cursorApp = max(0, min(v.cursorApp, len(list)-1))
	}
	if len(list) > 0 {
		v.appID = list[v.cursorApp].ID
	}
}

// display is the apps in the order shown: pinned in the order pinned, then
// the rest as derived, minus those the filter hides.
func (v *View) display() []apps.App {
	q := strings.ToLower(v.filter)
	var pinned, rest []apps.App
	for _, a := range v.apps {
		if q != "" && !strings.Contains(strings.ToLower(a.Name), q) {
			continue
		}
		if slices.Contains(v.state.Pinned, a.ID) {
			pinned = append(pinned, a)
		} else {
			rest = append(rest, a)
		}
	}
	slices.SortStableFunc(pinned, func(a, b apps.App) int {
		return slices.Index(v.state.Pinned, a.ID) - slices.Index(v.state.Pinned, b.ID)
	})
	return append(pinned, rest...)
}

func (v *View) selectedApp() (apps.App, bool) {
	list := v.display()
	if v.cursorApp < 0 || v.cursorApp >= len(list) {
		return apps.App{}, false
	}
	return list[v.cursorApp], true
}

func (v *View) selectedNeed() (apps.Item, bool) {
	if v.cursorNeed < 0 || v.cursorNeed >= len(v.needs) {
		return apps.Item{}, false
	}
	return v.needs[v.cursorNeed], true
}

// HandleActionAt implements views.FocusActionHandler: enter does the first
// action of the focused item, or opens the focused app.
func (v *View) HandleActionAt(action string, focus int) (views.View, tea.Cmd, bool) {
	if action == keys.ActOpen && !v.expanded && !v.typing {
		return v.open(focus)
	}
	return v.HandleAction(action)
}

// HandleAction implements views.ActionHandler: "/" finds an app, esc clears
// the search or closes the full charts.
func (v *View) HandleAction(action string) (views.View, tea.Cmd, bool) {
	switch action {
	case keys.ActFilter:
		v.typing = true
		return v, nil, true
	case keys.ActBack:
		if v.expanded {
			v.expanded = false
			return v, nil, true
		}
		if v.filter != "" {
			v.filter = ""
			v.restore()
			return v, nil, true
		}
	}
	return v, nil, false
}

// open is enter: with the needs list focused, the item's first action; with
// the apps focused, the app's machines.
func (v *View) open(focus int) (views.View, tea.Cmd, bool) {
	if focus == paneNeeds && len(v.needs) > 0 {
		it, ok := v.selectedNeed()
		if !ok || len(it.Actions) == 0 {
			return v, nil, true
		}
		return v, act(it, it.Actions[0].ID), true
	}
	a, ok := v.selectedApp()
	if !ok {
		return v, nil, true
	}
	return v, func() tea.Msg { return OpenAppMsg{App: a} }, true
}

func act(it apps.Item, action string) tea.Cmd {
	return func() tea.Msg { return ActionMsg{Item: it, Action: action} }
}

// HandleKey implements views.View.
func (v *View) HandleKey(msg tea.KeyPressMsg, focus int) (views.View, tea.Cmd, bool) {
	if v.typing {
		return v.handleInput(msg)
	}
	name := v.keys.Action(msg, keys.Home)
	if v.expanded {
		if name == "usage" {
			v.expanded = false
			return v, nil, true
		}
		rv, cmd, handled := v.res.HandleKey(msg, 0)
		_ = rv
		return v, cmd, handled
	}
	switch name {
	case "usage":
		v.expanded = true
		return v, nil, true
	case "density":
		i := slices.Index(densities, v.state.Density)
		v.state.Density = densities[(i+1)%len(densities)]
		return v, v.stateChanged(), true
	case "pin":
		if focus == paneApps {
			return v, v.togglePin(), true
		}
	case "up", "down", "left", "right", "home", "end":
		v.move(name, focus)
		return v, nil, true
	}
	if focus == paneNeeds {
		if it, ok := v.selectedNeed(); ok && name != "" {
			for _, a := range it.Actions {
				if a.ID == name {
					return v, act(it, name), true
				}
			}
		}
	}
	return v, nil, false
}

func (v *View) move(name string, focus int) {
	if focus == paneNeeds && len(v.needs) > 0 {
		switch name {
		case "up", "left":
			v.cursorNeed = max(0, v.cursorNeed-1)
		case "down", "right":
			v.cursorNeed = min(len(v.needs)-1, v.cursorNeed+1)
		case "home":
			v.cursorNeed = 0
		case "end":
			v.cursorNeed = len(v.needs) - 1
		}
		v.needID = v.needs[v.cursorNeed].ID
		return
	}
	list := v.display()
	if len(list) == 0 {
		return
	}
	cols := max(1, v.cols)
	switch name {
	case "left":
		v.cursorApp--
	case "right":
		v.cursorApp++
	case "up":
		v.cursorApp -= cols
	case "down":
		v.cursorApp += cols
	case "home":
		v.cursorApp = 0
	case "end":
		v.cursorApp = len(list) - 1
	}
	v.cursorApp = max(0, min(len(list)-1, v.cursorApp))
	v.appID = list[v.cursorApp].ID
	row := v.cursorApp / cols
	switch {
	case row < v.offset:
		v.offset = row
	case row >= v.offset+max(1, v.rowsVis):
		v.offset = row - max(1, v.rowsVis) + 1
	}
}

func (v *View) togglePin() tea.Cmd {
	a, ok := v.selectedApp()
	if !ok {
		return nil
	}
	if i := slices.Index(v.state.Pinned, a.ID); i >= 0 {
		v.state.Pinned = slices.Delete(slices.Clone(v.state.Pinned), i, i+1)
	} else if len(v.state.Pinned) < config.MaxPinnedApps {
		v.state.Pinned = append(slices.Clone(v.state.Pinned), a.ID)
	}
	v.restore()
	// The pinned app moved: follow it.
	for i, x := range v.display() {
		if x.ID == a.ID {
			v.cursorApp = i
		}
	}
	return v.stateChanged()
}

func (v *View) stateChanged() tea.Cmd {
	st := config.HomeState{Density: v.state.Density, Pinned: slices.Clone(v.state.Pinned)}
	return func() tea.Msg { return StateChangedMsg{State: st} }
}

// handleInput edits the app search.
func (v *View) handleInput(msg tea.KeyPressMsg) (views.View, tea.Cmd, bool) {
	switch v.keys.Action(msg, keys.Input) {
	case keys.ActConfirm:
		v.typing = false
		return v, nil, true
	case keys.ActCancel:
		v.typing, v.filter = false, ""
		v.restore()
		return v, nil, true
	case "clear":
		v.filter = ""
		v.restore()
		return v, nil, true
	}
	switch msg.String() {
	case "tab", "shift+tab", "ctrl+c":
		return v, nil, false
	case "backspace":
		if v.filter != "" {
			_, n := utf8.DecodeLastRuneInString(v.filter)
			v.filter = v.filter[:len(v.filter)-n]
			v.restore()
		}
		return v, nil, true
	}
	if t := msg.Text; t != "" && utf8.RuneCountInString(v.filter) < maxFilter && !strings.ContainsFunc(t, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		v.filter += t
		v.restore()
	}
	return v, nil, true
}

// Hints implements views.View.
func (v *View) Hints(km *keys.Map) []key.Binding {
	if v.typing {
		return km.Bindings(keys.Input)
	}
	if v.expanded {
		back := km.Binding(keys.Home, "usage")
		back.SetHelp(back.Help().Key, "back")
		return []key.Binding{back}
	}
	open := km.Binding(keys.Global, keys.ActOpen)
	open.SetHelp(open.Help().Key, "do the first action / open")
	return []key.Binding{open, km.Binding(keys.Home, "usage"), km.Binding(keys.Home, "density"), km.Binding(keys.Home, "pin"), km.Binding(keys.Global, keys.ActFilter), km.Binding(keys.Global, keys.ActNew)}
}

// ----- rendering -----

// Render implements views.View.
func (v *View) Render(ctx views.Context) string {
	if v.expanded {
		return v.res.Render(ctx)
	}
	s := ctx.Styles
	inner := ctx.Width - 4
	var lines []string
	lines = append(lines, "", v.statusLine(s, inner), "")
	needs := v.needsLines(s, inner, ctx)
	lines = append(lines, needs...)
	if usage := v.usageLines(s, inner); len(usage) > 0 {
		lines = append(lines, "")
		lines = append(lines, usage...)
	}
	title := "Home"
	list := v.display()
	head := s.TableHeader.Render("APPS") + s.Muted.Render(fmt.Sprintf("  %d", len(list)))
	if v.typing || v.filter != "" {
		cursor := ""
		if v.typing {
			cursor = "█"
		}
		head += s.Muted.Render("  find: ") + s.Text.Render(textsafe.Clean(v.filter)+cursor)
	}
	lines = append(lines, "", head)
	avail := ctx.Height - 2 - len(lines) - 2 // panel borders, the blank and the hint line
	grid := v.gridLines(s, ctx, inner, avail, list)
	lines = append(lines, grid...)
	return panel.Render(s, title, layout.Fit(strings.Join(lines, "\n"), inner, ctx.Height-2), ctx.Width, ctx.Height, true)
}

func (v *View) statusLine(s *theme.Styles, w int) string {
	var up, starting, off, machines int
	for _, a := range v.apps {
		up += a.Up
		starting += a.Starting
		off += a.Off
		machines += len(a.Machines)
	}
	counts := fmt.Sprintf("%d apps · %d machines · %d up", len(v.apps), machines, up)
	if starting > 0 {
		counts += fmt.Sprintf(" · %d starting", starting)
	}
	if off > 0 {
		counts += fmt.Sprintf(" · %d off", off)
	}
	text, lvl := v.sentence()
	return "  " + s.LevelText(lvl).Render(ansi.Truncate(text, w-4, "…")) + s.Muted.Render(ansi.Truncate("   "+counts, max(0, w-4-ansi.StringWidth(text)), "…"))
}

// sentence is the one line of status, in words.
func (v *View) sentence() (string, theme.Level) {
	if v.snap != nil {
		var down, degraded []string
		connected := 0
		for _, ev := range v.snap.Engines {
			switch ev.Health.State {
			case store.StateConnected:
				connected++
			case store.StateUnavailable:
				down = append(down, ev.ID.Label())
			case store.StateDegraded:
				degraded = append(degraded, ev.ID.Label())
			}
		}
		switch {
		case len(down) > 0 && connected == 0:
			return "✖ " + strings.Join(down, " and ") + " cannot be reached: nothing here is current", theme.LevelErr
		case len(down) > 0:
			return "⚠ " + strings.Join(down, " and ") + " cannot be reached: its machines are the last ones seen", theme.LevelWarn
		case len(degraded) > 0:
			return "⚠ " + strings.Join(degraded, " and ") + " is slow to answer", theme.LevelWarn
		}
	}
	n := len(v.needs)
	var starting int
	for _, a := range v.apps {
		starting += a.Starting
	}
	switch {
	case n == 1:
		return "⚠ 1 thing needs you", theme.LevelWarn
	case n > 1:
		return fmt.Sprintf("⚠ %d things need you", n), theme.LevelWarn
	case starting > 0:
		return fmt.Sprintf("◐ %d starting", starting), theme.LevelInfo
	case len(v.apps) == 0:
		return "Nothing here yet: press n to create a machine, or open the Library", theme.LevelInfo
	}
	return "✓ Everything that should be running is running", theme.LevelOK
}

func (v *View) needsLines(s *theme.Styles, w int, ctx views.Context) []string {
	if len(v.needs) == 0 {
		return nil
	}
	lines := []string{s.TableHeader.Render("NEEDS YOU") + s.Muted.Render(fmt.Sprintf("  %d", len(v.needs)))}
	shown := min(len(v.needs), maxNeeds)
	if ctx.Height < 30 {
		shown = min(shown, 3)
	}
	first := 0
	if v.cursorNeed >= shown {
		first = v.cursorNeed - shown + 1
	}
	focused := ctx.Focus == paneNeeds
	for i := first; i < first+shown; i++ {
		it := v.needs[i]
		sym, lvl := "⚠", theme.LevelWarn
		switch {
		case it.Severity >= 2:
			sym, lvl = "✖", theme.LevelErr
		case it.Rule == "engine-update" || it.Rule == "image-updates":
			sym, lvl = "⬆", theme.LevelInfo
		}
		keysText := v.actionKeys(it)
		subject := textsafe.Clean(it.Subject)
		text := textsafe.Clean(it.Text)
		room := w - 6 - ansi.StringWidth(keysText) - 2
		body := pad(subject, 14) + " " + ansi.Truncate(text, max(10, room-15), "…")
		row := "  " + s.LevelText(lvl).Render(sym) + "  " + s.Text.Render(pad(body, max(10, room))) + s.Muted.Render("  "+keysText)
		if i == v.cursorNeed && focused {
			row = s.Selected.Render(pad("▶ "+ansi.Strip(row)[2:], w))
		}
		lines = append(lines, row)
	}
	if more := len(v.needs) - (first + shown); more > 0 {
		lines = append(lines, s.Muted.Render(fmt.Sprintf("    ▼ %d more", more)))
	}
	return lines
}

// actionKeys is "s start it   L logs": the key of each action of an item.
func (v *View) actionKeys(it apps.Item) string {
	var parts []string
	for _, a := range it.Actions {
		k := v.keys.Binding(keys.Home, a.ID).Help().Key
		if k == "" {
			continue
		}
		parts = append(parts, k+" "+a.Label)
	}
	return strings.Join(parts, "   ")
}

// usageLines is one line per connected engine: CPU and memory as they are now.
func (v *View) usageLines(s *theme.Styles, w int) []string {
	if v.snap == nil {
		return nil
	}
	var out []string
	for _, ev := range v.snap.Engines {
		if ev.Health.State != store.StateConnected {
			continue
		}
		p, ok := v.snap.EnginePoint(ev.ID)
		if !ok {
			continue
		}
		var cpu []float64
		for _, pt := range v.snap.EngineSeries(ev.ID, 12) {
			cpu = append(cpu, pt.CPU)
		}
		line := "  " + s.Muted.Render(pad(ev.ID.Label(), 7)) + s.Muted.Render("cpu ") + s.ChartCPU.Render(sparkline.Render(cpu, 12, 100)) +
			s.Text.Render(fmt.Sprintf(" %.0f%%", p.CPU)) + s.Muted.Render("   mem ") + s.Text.Render(metrics.Bytes(p.MemUsed))
		out = append(out, ansi.Truncate(line, w, "…"))
	}
	if len(out) > 0 {
		out[0] += s.Muted.Render("   · " + v.keys.Binding(keys.Home, "usage").Help().Key + " full charts")
	}
	return out
}

// densityFor picks the roomiest density that fits n apps in avail lines.
func (v *View) densityFor(n, inner, avail int) string {
	if d := v.state.Density; d != "" {
		return d
	}
	cardCols := max(1, (inner+1)/(cardW+1))
	if n <= cardCols*max(1, avail/cardH) {
		return DensityCards
	}
	rowCols := max(1, inner/rowColW)
	if n <= rowCols*max(1, avail) {
		return DensityRows
	}
	return DensityMosaic
}

func (v *View) gridLines(s *theme.Styles, ctx views.Context, inner, avail int, list []apps.App) []string {
	if len(list) == 0 {
		v.cols, v.rowsVis = 1, 1
		if v.filter != "" {
			return []string{s.Muted.Render("  No app matches. Esc clears the search.")}
		}
		return nil
	}
	avail = max(1, avail)
	d := v.densityFor(len(list), inner, avail)
	v.shown = d
	focused := ctx.Focus == paneApps
	var lines []string
	switch d {
	case DensityCards:
		lines = v.cards(s, inner, avail, list, focused)
	case DensityRows:
		lines = v.rows(s, inner, avail, list, focused, rowColW, 56, false)
	default:
		lines = v.rows(s, inner, avail, list, focused, mosaicColW, 38, true)
	}
	return lines
}

// window returns the first and last-plus-one app index of the visible rows.
func (v *View) window(n, cols, rows int) (int, int) {
	v.cols, v.rowsVis = cols, rows
	total := (n + cols - 1) / cols
	v.offset = max(0, min(v.offset, max(0, total-rows)))
	row := v.cursorApp / cols
	switch {
	case row < v.offset:
		v.offset = row
	case row >= v.offset+rows:
		v.offset = row - rows + 1
	}
	return v.offset * cols, min(n, (v.offset+rows)*cols)
}

func (v *View) overflow(s *theme.Styles, list []apps.App, from, to int) string {
	hidden := list[:from]
	after := list[to:]
	n := len(hidden) + len(after)
	if n == 0 {
		return ""
	}
	var attention int
	for _, a := range append(slices.Clone(hidden), after...) {
		if a.Health >= apps.Attention {
			attention++
		}
	}
	text := fmt.Sprintf("  ▼ %d more apps", n)
	if attention > 0 {
		text += fmt.Sprintf(" (%d need attention)", attention)
	} else {
		text += ", all healthy"
	}
	return s.Muted.Render(text + " · ↓ scrolls · d density")
}

// ----- cards -----

func (v *View) cards(s *theme.Styles, inner, avail int, list []apps.App, focused bool) []string {
	cols := max(1, (inner+1)/(cardW+1))
	rowsFit := max(1, (avail-1)/cardH) // one line stays free for the overflow sentence
	if len(list) <= cols*rowsFit {
		rowsFit = max(1, avail/cardH)
	}
	from, to := v.window(len(list), cols, rowsFit)
	var lines []string
	for r := from; r < to; r += cols {
		var blocks []string
		for i := r; i < min(to, r+cols); i++ {
			blocks = append(blocks, v.card(s, list[i], i == v.cursorApp, focused))
		}
		lines = append(lines, strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, joinGap(blocks)...), "\n")...)
	}
	if o := v.overflow(s, list, from, to); o != "" {
		lines = append(lines, o)
	}
	return lines
}

func joinGap(blocks []string) []string {
	out := make([]string, 0, len(blocks)*2)
	for i, b := range blocks {
		if i > 0 {
			out = append(out, " ")
		}
		out = append(out, b)
	}
	return out
}

func healthStyle(s *theme.Styles, h apps.Health) (lipgloss.Style, string) {
	switch h {
	case apps.Down:
		return s.LevelText(theme.LevelErr), "✖"
	case apps.Attention:
		return s.LevelText(theme.LevelWarn), "⚠"
	case apps.Starting:
		return s.LevelText(theme.LevelInfo), "◐"
	case apps.OK:
		return s.LevelText(theme.LevelOK), "●"
	}
	return s.Muted, "○"
}

func (v *View) card(s *theme.Styles, a apps.App, selected, focused bool) string {
	hs, sym := healthStyle(s, a.Health)
	border := s.Muted
	switch {
	case selected && focused:
		border = s.Accent
	case a.Health >= apps.Attention:
		border = hs
	}
	w := cardW - 4
	name := textsafe.Clean(a.Name)
	if slices.Contains(v.state.Pinned, a.ID) {
		name = "★ " + name
	}
	line1 := hs.Render(sym) + " " + s.Text.Render(ansi.Truncate(summary(a), w-2, "…"))
	line2 := s.Muted.Render(ansi.Truncate(machineNames(a), w, "…"))
	if a.Reach != "" {
		line2 = s.Accent.Render(ansi.Truncate(textsafe.Clean(a.Reach), w, "…"))
	}
	line3 := s.Muted.Render(ansi.Truncate(usage(a), w, "…"))
	top := border.Render("╭─ ") + s.PanelTitle.Render(ansi.Truncate(name, cardW-8, "…")) + border.Render(" "+strings.Repeat("─", max(0, cardW-5-ansi.StringWidth(ansi.Truncate(name, cardW-8, "…"))))+"╮")
	row := func(t string) string { return border.Render("│ ") + pad(t, w) + border.Render(" │") }
	return strings.Join([]string{top, row(line1), row(line2), row(line3), border.Render("╰" + strings.Repeat("─", cardW-2) + "╯")}, "\n")
}

// summary is the first line of a card: what is going on, in words.
func summary(a apps.App) string {
	switch a.Health {
	case apps.Down, apps.Attention:
		if a.Reason != "" {
			return textsafe.Clean(a.Reason)
		}
	case apps.Starting:
		return fmt.Sprintf("%d of %d starting", a.Starting, len(a.Machines))
	case apps.Off:
		return "off"
	}
	if len(a.Machines) == 1 {
		return "up"
	}
	if a.Up == len(a.Machines) {
		return fmt.Sprintf("all up · %d machines", len(a.Machines))
	}
	return fmt.Sprintf("%d of %d up", a.Up, len(a.Machines))
}

func machineNames(a apps.App) string {
	if len(a.Names) <= 1 {
		return ""
	}
	names := make([]string, 0, len(a.Names))
	for _, n := range a.Names {
		names = append(names, textsafe.Clean(n))
	}
	return strings.Join(names, "  ")
}

func usage(a apps.App) string {
	if a.Up == 0 {
		return ""
	}
	return fmt.Sprintf("cpu %.0f%%  mem %s", a.CPU, metrics.Bytes(a.MemUsed))
}

// ----- rows and mosaic -----

func (v *View) rows(s *theme.Styles, inner, avail int, list []apps.App, focused bool, colW, _ int, mosaic bool) []string {
	cols := max(1, inner/colW)
	reserve := 1
	rowsFit := max(1, avail-reserve)
	if len(list) <= cols*avail {
		rowsFit = avail
	}
	from, to := v.window(len(list), cols, rowsFit)
	var lines []string
	for r := from; r < to; r += cols {
		var cells []string
		for i := r; i < min(to, r+cols); i++ {
			cells = append(cells, v.rowCell(s, list[i], i == v.cursorApp && focused, colW-2, mosaic))
		}
		lines = append(lines, "  "+strings.Join(cells, "  "))
	}
	if o := v.overflow(s, list, from, to); o != "" {
		lines = append(lines, o)
	}
	return lines
}

func (v *View) rowCell(s *theme.Styles, a apps.App, selected bool, w int, mosaic bool) string {
	hs, sym := healthStyle(s, a.Health)
	name := textsafe.Clean(a.Name)
	if slices.Contains(v.state.Pinned, a.ID) {
		name = "★" + name
	}
	var text string
	if mosaic {
		text = hs.Render(sym) + " " + s.Text.Render(pad(name, 12)) + " " + v.cells(s, a, w-16)
	} else {
		info := summary(a)
		if a.Reach != "" && a.Health == apps.OK {
			info = a.Reach
		}
		text = hs.Render(sym) + " " + s.Text.Render(pad(name, 12)) + " " + s.Muted.Render(pad(fmt.Sprint(len(a.Machines)), 3)) + " " + s.Muted.Render(pad(ansi.Truncate(textsafe.Clean(info), w-32, "…"), max(0, w-32))) + " " + s.Muted.Render(pad(rowUsage(a), 12))
	}
	if selected {
		return s.Selected.Render(pad(ansi.Strip(text), w))
	}
	return pad(text, w)
}

func rowUsage(a apps.App) string {
	if a.Up == 0 {
		return ""
	}
	return metrics.Bytes(a.MemUsed)
}

// cells draws one cell per machine, coloured by its health, as many as fit.
func (v *View) cells(s *theme.Styles, a apps.App, w int) string {
	var b strings.Builder
	used := 0
	for _, h := range a.Cells {
		if used+2 > w {
			b.WriteString(s.Muted.Render("…"))
			break
		}
		st, _ := healthStyle(s, h)
		glyph := "■"
		switch h {
		case apps.Starting:
			glyph = "◐"
		case apps.Down:
			glyph = "✖"
		case apps.Off:
			glyph = "□"
		}
		b.WriteString(st.Render(glyph) + " ")
		used += 2
	}
	return b.String()
}

func pad(t string, w int) string {
	n := ansi.StringWidth(t)
	if n > w {
		return ansi.Truncate(t, w, "…")
	}
	return t + strings.Repeat(" ", w-n)
}
