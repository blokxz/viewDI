// Package app holds the root Bubble Tea model: the router for views 1–6, the
// focus manager, the modal stack, the toast queue and the banner slot.
//
// Update and View are pure: every side effect (saving state, timers) runs in a
// tea.Cmd and comes back as a message.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/alerts"
	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/components/banner"
	"github.com/blokxz/viewDI/internal/ui/components/debugoverlay"
	"github.com/blokxz/viewDI/internal/ui/components/help"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/picker"
	"github.com/blokxz/viewDI/internal/ui/components/themepicker"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
	"github.com/blokxz/viewDI/internal/ui/views/admin"
	"github.com/blokxz/viewDI/internal/ui/views/placeholder"
	"github.com/blokxz/viewDI/internal/ui/views/resources"
	"github.com/blokxz/viewDI/internal/ui/views/table"
)

// Options configures the root model. main builds it from flags and files.
type Options struct {
	Themes   *theme.Registry
	Theme    *theme.Theme
	Keys     *keys.Map
	State    config.State
	Warnings []config.Warning // startup warnings, shown as toasts

	Mock     string // fake-engine scenario, "" for real daemons
	ReadOnly bool
	Debug    bool // enables the ctrl+d overlay

	// TerminalColors asks the terminal to adopt the theme's background and
	// foreground (OSC 10/11). main disables it when NO_COLOR is set.
	TerminalColors bool

	// SaveState persists state.json; it runs inside a tea.Cmd. Nil disables
	// persistence (tests).
	SaveState func(config.State) error
	// Logger receives diagnostics; nil discards them.
	Logger *slog.Logger
	// ToastTTL overrides toast.DefaultTTL (tests).
	ToastTTL time.Duration

	// Source delivers engine changes; nil runs without engines (tests).
	Source Source
	// Context ends the change pump; main cancels it after the program exits.
	Context context.Context
	// Now is the render clock (golden tests pin it). Default time.Now.
	Now func() time.Time
	// Tick schedules the next ClockTickMsg; nil uses a timer bound to
	// Context. Tests that run commands synchronously replace it.
	Tick func(time.Duration) tea.Cmd
	// Refresh is the metrics interval while a metrics view is visible
	// (default config.DefaultRefresh); +/- change it at runtime.
	Refresh time.Duration
	// Alerts sets the alert thresholds; zero uses the defaults.
	Alerts alerts.Config
}

// IdleRefresh is the sampling interval while no metrics view is visible or
// the terminal is unfocused (docs/02-architecture.md §4.5).
const IdleRefresh = 5 * time.Second

// refreshSteps are the intervals +/- move through.
var refreshSteps = []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second}

// StateSavedMsg reports the result of a state.json write.
type StateSavedMsg struct{ Err error }

// Model is the root model.
type Model struct {
	opts   Options
	keys   *keys.Map
	log    *slog.Logger
	styles *theme.Styles

	views  []views.View
	active int // index into views
	focus  int // focused pane in the active view

	width, height int
	focused       bool // terminal focus (focus reporting)

	modals []modal.Modal
	toasts toast.Queue
	banner banner.Banner

	state          config.State
	saveFailedOnce bool

	stats     *debugoverlay.Stats
	showDebug bool

	store *store.Store
	now   time.Time

	refresh      time.Duration // interval while a metrics view is visible
	sentInterval time.Duration // last interval sent to the source
}

// pickerProject identifies the Incus project picker.
const pickerProject = "incus-project"

// New builds the root model.
func New(opts Options) Model {
	if opts.Keys == nil {
		opts.Keys = keys.MustDefault()
	}
	if opts.Theme == nil {
		opts.Theme = theme.Fallback()
	}
	if opts.ToastTTL <= 0 {
		opts.ToastTTL = toast.DefaultTTL
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Tick == nil {
		opts.Tick = clockTick(opts.Context)
	}
	m := Model{
		opts:    opts,
		keys:    opts.Keys,
		log:     log,
		styles:  theme.NewStyles(opts.Theme, 1),
		views:   placeholder.All(),
		toasts:  toast.New(opts.ToastTTL, toast.DefaultVisible),
		state:   opts.State,
		stats:   debugoverlay.New(),
		focused: true,
		store:   store.New(engine.IDs...),
		now:     opts.Now(),
		refresh: opts.Refresh,
	}
	if m.refresh <= 0 {
		m.refresh = config.DefaultRefresh
	}
	if opts.Alerts != (alerts.Config{}) {
		m.store.ConfigureAlerts(opts.Alerts)
	}
	m.views[0] = admin.New(m.keys)
	m.views[1] = resources.New(m.keys)
	m.views[2] = table.New(m.keys, opts.State.Table)
	if v := opts.State.LastView; v >= 1 && v <= len(m.views) {
		m.active = v - 1
	}
	return m
}

// Init implements tea.Model: startup warnings become toasts, and the change
// pump and render clock start when engines are wired.
func (m Model) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.opts.Warnings)+2)
	for _, w := range m.opts.Warnings {
		cmds = append(cmds, toast.Show(theme.LevelWarn, w.String()))
	}
	if m.opts.Source != nil {
		cmds = append(cmds, pump(m.opts.Context, m.opts.Source.Changes(), m.opts.Source.Metrics()), m.opts.Tick(m.tickEvery()))
	}
	return tea.Batch(cmds...)
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	mm := model.(Model)
	if c := mm.syncInterval(); c != nil {
		return mm, tea.Batch(cmd, c)
	}
	return mm, cmd
}

// syncInterval tells the source when the wanted sampling interval changed:
// fast while a metrics view is visible and the terminal focused, slow
// otherwise. It updates m.sentInterval through the returned model's copy.
func (m *Model) syncInterval() tea.Cmd {
	src := m.opts.Source
	if src == nil {
		return nil
	}
	want := IdleRefresh
	if m.focused && m.active <= 2 { // Admin, Resources, Table show metrics
		want = m.refresh
	}
	if want == m.sentInterval {
		return nil
	}
	m.sentInterval = want
	return func() tea.Msg { src.SetInterval(want); return nil }
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.opts.Debug {
		m.stats.RecordMsg(msg)
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.broadcast(m.resizeMsg())
		return m.updateTopModal(msg)

	case ChangesMsg:
		return m.applyChanges(msg.Changes)

	case ClockTickMsg:
		m.now = time.Time(msg)
		if m.store.Evaluate(m.now) {
			m.broadcast(views.DataMsg{Snapshot: m.store.Snapshot()})
		}
		return m, m.opts.Tick(m.tickEvery())

	case resources.RefreshStepMsg:
		return m.stepRefresh(msg.Faster)

	case table.StateChangedMsg:
		m.state.Table = msg.State
		return m, m.saveState()

	case picker.SelectedMsg:
		if msg.Kind != pickerProject {
			return m, nil
		}
		return m.selectProject(msg.ID)

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.FocusMsg:
		m.focused = true
		return m, nil
	case tea.BlurMsg:
		m.focused = false
		return m, nil

	case modal.OpenMsg:
		m.modals = append(m.modals, msg.Modal)
		return m, nil
	case modal.CloseMsg:
		if len(m.modals) > 0 {
			m.modals = m.modals[:len(m.modals)-1]
		}
		return m, nil

	case themepicker.PreviewMsg:
		m.applyTheme(msg.ID)
		return m, nil
	case themepicker.SelectedMsg:
		if !m.applyTheme(msg.ID) {
			return m, nil
		}
		m.state.Theme = msg.ID
		return m, tea.Batch(m.saveState(), toast.Show(theme.LevelOK, "Theme: "+m.styles.Theme.Name))

	case toast.ShowMsg, toast.ExpiredMsg:
		var cmd tea.Cmd
		m.toasts, cmd = m.toasts.Update(msg)
		return m, cmd

	case banner.ShowMsg:
		m.banner = banner.Banner(msg)
		return m, nil

	case StateSavedMsg:
		if msg.Err == nil {
			return m, nil
		}
		m.log.Warn("state not saved", "err", msg.Err)
		if m.saveFailedOnce { // F16: toast once, then continue without persisting
			return m, nil
		}
		m.saveFailedOnce = true
		return m, toast.Show(theme.LevelWarn, "Cannot save UI state: "+msg.Err.Error())

	case debugoverlay.TickMsg:
		if !m.showDebug {
			return m, nil
		}
		m.stats.Roll(time.Time(msg))
		return m, debugoverlay.Tick()
	}

	var cmd tea.Cmd
	m.views[m.active], cmd = m.views[m.active].Update(msg)
	return m, cmd
}

// applyChanges applies a pump batch, refreshes every view when the store
// changed, announces recoveries, and re-arms the pump.
func (m Model) applyChanges(changes []store.Change) (tea.Model, tea.Cmd) {
	before := m.store.Snapshot()
	cmds := []tea.Cmd{}
	if m.opts.Source != nil {
		cmds = append(cmds, pump(m.opts.Context, m.opts.Source.Changes(), m.opts.Source.Metrics()))
	}
	applied := m.store.Apply(changes...)
	evaluated := m.store.Evaluate(m.now)
	if !applied && !evaluated {
		return m, tea.Batch(cmds...)
	}
	after := m.store.Snapshot()
	for _, e := range after.Engines {
		prev := before.Engine(e.ID).Health.State
		if e.Health.State == store.StateConnected && (prev == store.StateDegraded || prev == store.StateUnavailable) {
			cmds = append(cmds, toast.Show(theme.LevelOK, e.ID.Label()+" reconnected"))
		}
	}
	m.broadcast(views.DataMsg{Snapshot: after})
	m.broadcast(m.resizeMsg())
	return m, tea.Batch(cmds...)
}

// broadcast sends msg to every view, visible or not.
func (m *Model) broadcast(msg tea.Msg) {
	for i, v := range m.views {
		m.views[i], _ = v.Update(msg)
	}
}

func (m Model) resizeMsg() views.ResizeMsg {
	top := 2
	if m.bannerContent().Visible() {
		top++
	}
	return views.ResizeMsg{Width: m.width, Height: max(0, m.height-top-1)}
}

// tickEvery is the render clock period: every second while a countdown is
// on screen, otherwise slow enough to only refresh uptimes.
func (m Model) tickEvery() time.Duration {
	for _, e := range m.store.Snapshot().Engines {
		if e.Health.State == store.StateDegraded || e.Health.State == store.StateUnavailable {
			return time.Second
		}
	}
	return 15 * time.Second
}

// stepRefresh moves the metrics interval one step (+ faster, - slower).
func (m Model) stepRefresh(faster bool) (tea.Model, tea.Cmd) {
	i := 0
	for i < len(refreshSteps)-1 && refreshSteps[i] < m.refresh {
		i++
	}
	if faster && i > 0 {
		i--
	} else if !faster && i < len(refreshSteps)-1 {
		i++
	}
	m.refresh = refreshSteps[i]
	return m, toast.Show(theme.LevelInfo, "Refresh every "+m.refresh.String())
}

// Refresh returns the metrics interval used while a metrics view is visible.
func (m Model) Refresh() time.Duration { return m.refresh }

func (m Model) selectProject(id string) (tea.Model, tea.Cmd) {
	if id == m.state.IncusProject {
		return m, nil
	}
	m.state.IncusProject = id
	label := id
	if label == "" {
		label = "all projects"
	}
	cmds := []tea.Cmd{m.saveState(), toast.Show(theme.LevelInfo, "Incus project: "+label)}
	if src := m.opts.Source; src != nil {
		cmds = append(cmds, func() tea.Msg { src.SetProject(id); return nil })
	}
	return m, tea.Batch(cmds...)
}

func (m Model) projectPicker() tea.Cmd {
	snap := m.store.Snapshot()
	inc := snap.Engine(engine.Incus)
	if !inc.InfoKnown {
		return toast.Show(theme.LevelInfo, "Incus projects are not known yet")
	}
	count := map[string]int{}
	for _, in := range snap.Instances {
		if in.Engine == engine.Incus {
			count[in.Project]++
		}
	}
	items := []picker.Item{{ID: "", Label: "all projects"}}
	if !inc.Info.Capabilities.AllProjects {
		items = nil
	}
	for _, p := range inc.Info.Projects {
		it := picker.Item{ID: p, Label: p}
		if n, ok := count[p]; ok {
			it.Detail = fmt.Sprintf("%d instances", n)
		}
		items = append(items, it)
	}
	return modal.Open(picker.New(m.keys, pickerProject, "Incus project", items, m.state.IncusProject))
}

func (m Model) updateTopModal(msg tea.Msg) (tea.Model, tea.Cmd) {
	if len(m.modals) == 0 {
		return m, nil
	}
	top := len(m.modals) - 1
	var cmd tea.Cmd
	m.modals[top], cmd = m.modals[top].Update(msg)
	return m, cmd
}

// handleKey resolves a key through the four layers: modal → view (component
// and view scopes) → instance actions → global.
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" { // never captured, not even by modals
		return m.quit()
	}
	if len(m.modals) > 0 {
		return m.updateTopModal(msg)
	}

	view := m.views[m.active]
	v, cmd, handled := view.HandleKey(msg, m.focus)
	m.views[m.active] = v
	if handled {
		return m, cmd
	}

	if view.ShowsInstances() {
		if a := m.keys.Action(msg, keys.Instance); a != "" {
			return m, m.instanceAction(a)
		}
	}
	return m.globalAction(m.keys.Action(msg, keys.Global))
}

// selected returns the instance under the active view's cursor.
func (m Model) selected() (engine.Instance, bool) {
	if sel, ok := m.views[m.active].(views.Selector); ok {
		return sel.Selected()
	}
	return engine.Instance{}, false
}

func (m Model) instanceAction(action string) tea.Cmd {
	if m.opts.ReadOnly {
		return toast.Show(theme.LevelWarn, "Read-only mode: instance actions are disabled")
	}
	in, ok := m.selected()
	if !ok {
		return toast.Show(theme.LevelInfo, "No instance selected")
	}
	if caps := m.store.Snapshot().Engine(in.Engine).Info.Capabilities; caps.Restricted {
		return toast.Show(theme.LevelWarn, fmt.Sprintf("%s: %s — actions are disabled", in.Engine.Label(), caps.Reason))
	}
	b := m.keys.Binding(keys.Instance, action)
	return toast.Show(theme.LevelInfo, fmt.Sprintf("%s %s: lifecycle actions arrive in Stage 4", b.Help().Desc, in.Name))
}

// notYet maps global actions without an implementation to the stage that delivers them.
var notYet = map[string]string{
	keys.ActFilter:       "Filtering is available in the Table view (3)",
	keys.ActEngine:       "The engine filter is available in the Table view (3)",
	keys.ActNew:          "Create forms arrive in Stage 4",
	keys.ActImport:       "Import arrives in Stage 4",
	keys.ActCheckUpdates: "Update checks arrive in Stage 6",
}

func (m Model) globalAction(action string) (tea.Model, tea.Cmd) {
	if h, ok := m.views[m.active].(views.ActionHandler); ok && action != "" {
		v, cmd, handled := h.HandleAction(action)
		m.views[m.active] = v
		if handled {
			return m, cmd
		}
	}
	for i, a := range keys.ViewActions {
		if action == a {
			m.switchView(i)
			return m, nil
		}
	}
	switch action {
	case "":
		return m, nil
	case keys.ActQuit:
		return m.quit()
	case keys.ActNextPane, keys.ActPrevPane:
		n := m.views[m.active].Panes()
		step := 1
		if action == keys.ActPrevPane {
			step = n - 1
		}
		m.focus = (m.focus + step) % n
		return m, nil
	case keys.ActTheme:
		return m, modal.Open(themepicker.New(m.keys, m.opts.Themes.Themes(), m.styles.Theme.ID))
	case keys.ActHelp:
		return m, modal.Open(help.New(m.keys, m.styles, m.width, m.height, m.helpScopes()...))
	case keys.ActDebug:
		if !m.opts.Debug {
			return m, toast.Show(theme.LevelInfo, "Debug overlay requires --debug")
		}
		m.showDebug = !m.showDebug
		if m.showDebug {
			m.stats.Roll(time.Now())
			return m, debugoverlay.Tick()
		}
		return m, nil
	case keys.ActSuspend:
		return m, tea.Suspend
	case keys.ActBack:
		return m, nil
	case keys.ActOpen:
		if in, ok := m.selected(); ok {
			return m, toast.Show(theme.LevelInfo, "Detail view for "+in.Name+" arrives in Stage 3")
		}
		return m, nil
	case keys.ActProject:
		return m, m.projectPicker()
	case keys.ActReconnect:
		src := m.opts.Source
		if src == nil {
			return m, toast.Show(theme.LevelInfo, "No engines to reconnect")
		}
		return m, tea.Batch(func() tea.Msg { src.Reconnect(); return nil }, toast.Show(theme.LevelInfo, "Reconnecting / resyncing engines…"))
	case keys.ActNew, keys.ActImport:
		if m.opts.ReadOnly {
			return m, toast.Show(theme.LevelWarn, "Read-only mode: creating and importing are disabled")
		}
	}
	if text, ok := notYet[action]; ok {
		return m, toast.Show(theme.LevelInfo, text)
	}
	return m, nil
}

func (m *Model) switchView(i int) {
	if i == m.active {
		return
	}
	m.active, m.focus = i, 0
	m.state.LastView = i + 1
}

func (m Model) helpScopes() []keys.Scope {
	scopes := []keys.Scope{keys.Global}
	v := m.views[m.active]
	if v.ShowsInstances() {
		scopes = append(scopes, keys.Instance)
	}
	if s := v.Scope(); s != "" {
		scopes = append(scopes, s)
	}
	return scopes
}

// applyTheme switches the style cache to theme id. It reports false (with a
// toast queued through the log) if the theme is unknown.
func (m *Model) applyTheme(id string) bool {
	t, ok := m.opts.Themes.Get(id)
	if !ok {
		m.log.Warn("unknown theme", "id", id)
		return false
	}
	if t == m.styles.Theme {
		return true
	}
	m.styles = theme.NewStyles(t, m.styles.Version+1)
	return true
}

func (m Model) saveState() tea.Cmd {
	save := m.opts.SaveState
	if save == nil {
		return nil
	}
	st := m.state
	return func() tea.Msg { return StateSavedMsg{Err: save(st)} }
}

func (m Model) quit() (tea.Model, tea.Cmd) {
	save := m.opts.SaveState
	if save == nil {
		return m, tea.Quit
	}
	st, log := m.state, m.log
	return m, tea.Sequence(func() tea.Msg {
		if err := save(st); err != nil {
			log.Warn("state not saved on exit", "err", err)
		}
		return nil
	}, tea.Quit)
}

// Accessors used by tests and the trace tooling.

// ActiveView returns the 1-based active view number.
func (m Model) ActiveView() int { return m.active + 1 }

// Focus returns the focused pane index.
func (m Model) Focus() int { return m.focus }

// ThemeID returns the active theme ID.
func (m Model) ThemeID() string { return m.styles.Theme.ID }

// ModalDepth returns the number of open modals.
func (m Model) ModalDepth() int { return len(m.modals) }

// State returns the state that would be persisted.
func (m Model) State() config.State { return m.state }

// Toasts returns the visible toasts.
func (m Model) Toasts() []toast.Toast { return m.toasts.Items() }

// Snapshot returns the current store snapshot.
func (m Model) Snapshot() *store.Snapshot { return m.store.Snapshot() }

// ActiveViewModel returns the active view (tests).
func (m Model) ActiveViewModel() views.View { return m.views[m.active] }

// Layout returns the current layout mode.
func (m Model) Layout() layout.Mode { return layout.ModeFor(m.width, m.height) }
