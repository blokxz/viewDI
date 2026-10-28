// Package app holds the root Bubble Tea model: the router for views 1–6, the
// focus manager, the modal stack, the toast queue and the banner slot.
//
// Update and View are pure: every side effect (saving state, timers) runs in a
// tea.Cmd and comes back as a message.
package app

import (
	"fmt"
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/ui/components/banner"
	"github.com/blokxz/viewDI/internal/ui/components/debugoverlay"
	"github.com/blokxz/viewDI/internal/ui/components/help"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/themepicker"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
	"github.com/blokxz/viewDI/internal/ui/views/placeholder"
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
}

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
}

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
	}
	if v := opts.State.LastView; v >= 1 && v <= len(m.views) {
		m.active = v - 1
	}
	return m
}

// Init implements tea.Model: startup warnings become toasts.
func (m Model) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.opts.Warnings))
	for _, w := range m.opts.Warnings {
		cmds = append(cmds, toast.Show(theme.LevelWarn, w.String()))
	}
	return tea.Batch(cmds...)
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.opts.Debug {
		m.stats.RecordMsg(msg)
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m.updateTopModal(msg)

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

func (m Model) instanceAction(action string) tea.Cmd {
	if m.opts.ReadOnly {
		return toast.Show(theme.LevelWarn, "Read-only mode: instance actions are disabled")
	}
	return toast.Show(theme.LevelInfo, fmt.Sprintf("No instance selected (%s arrives with Stage 1 data and Stage 4 actions)", action))
}

// notYet maps global actions without an implementation to the stage that delivers them.
var notYet = map[string]string{
	keys.ActOpen:         "Detail view arrives in Stage 3",
	keys.ActFilter:       "Filtering arrives in Stage 1",
	keys.ActEngine:       "Engine filter arrives in Stage 1",
	keys.ActProject:      "Incus project selector arrives in Stage 1",
	keys.ActReconnect:    "Engines arrive in Stage 1",
	keys.ActNew:          "Create forms arrive in Stage 4",
	keys.ActImport:       "Import arrives in Stage 4",
	keys.ActCheckUpdates: "Update checks arrive in Stage 6",
}

func (m Model) globalAction(action string) (tea.Model, tea.Cmd) {
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

// Layout returns the current layout mode.
func (m Model) Layout() layout.Mode { return layout.ModeFor(m.width, m.height) }
