package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Level classifies toasts, banners and alerts.
type Level int

// Levels, in increasing severity.
const (
	LevelInfo Level = iota
	LevelOK
	LevelWarn
	LevelErr
)

// Styles is the style cache for one theme. It is built once per theme change
// (NewStyles) and shared read-only by every component; nothing builds styles
// per frame.
type Styles struct {
	Theme   *Theme
	Version uint64 // increments on every theme change; part of render cache keys

	Text   lipgloss.Style
	Muted  lipgloss.Style
	Bold   lipgloss.Style
	Accent lipgloss.Style

	// Header and tabs.
	AppTitle    lipgloss.Style
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
	TabKey      lipgloss.Style
	Badge       lipgloss.Style
	EngineName  map[string]lipgloss.Style // "docker", "incus"
	Separator   lipgloss.Style

	// Panels.
	Panel        lipgloss.Style // unfocused bordered panel
	PanelFocused lipgloss.Style
	PanelTitle   lipgloss.Style
	PanelError   lipgloss.Style // unavailable-engine cards

	// Tables.
	TableHeader lipgloss.Style

	// Detail view: search hits, cursor lines, log sources, JSON syntax.
	Match      lipgloss.Style
	CursorLine lipgloss.Style
	LogStdout  lipgloss.Style
	LogStderr  lipgloss.Style
	LogSystem  lipgloss.Style
	JSONKey    lipgloss.Style
	JSONString lipgloss.Style
	JSONNumber lipgloss.Style
	JSONOther  lipgloss.Style // true, false, null

	// Charts (sparklines, gauges).
	ChartCPU   lipgloss.Style
	ChartMem   lipgloss.Style
	ChartNetRx lipgloss.Style
	ChartNetTx lipgloss.Style
	ChartDisk  lipgloss.Style
	GaugeEmpty lipgloss.Style

	// Status / key-hint bar.
	StatusBar lipgloss.Style
	KeyHint   lipgloss.Style
	KeyDesc   lipgloss.Style
	Indicator lipgloss.Style // MOCK / READ-ONLY markers

	// Overlays.
	Modal        lipgloss.Style
	ModalTitle   lipgloss.Style
	Selected     lipgloss.Style
	Button       lipgloss.Style
	ButtonDanger lipgloss.Style

	levelFg [4]lipgloss.Style
	toast   [4]lipgloss.Style
	banner  [4]lipgloss.Style
}

// NewStyles builds the style cache for t. version should increase on every call
// made for a theme change.
func NewStyles(t *Theme, version uint64) *Styles {
	c := t.Color
	fg := func(tok Token) lipgloss.Style { return lipgloss.NewStyle().Foreground(c(tok)) }
	border := lipgloss.RoundedBorder()

	s := &Styles{
		Theme:   t,
		Version: version,
		Text:    fg(Fg),
		Muted:   fg(FgMuted),
		Bold:    fg(Fg).Bold(true),
		Accent:  fg(Accent).Bold(true),

		AppTitle:    fg(Accent).Bold(true),
		TabActive:   lipgloss.NewStyle().Foreground(c(SelectionFg)).Background(c(SelectionBg)).Bold(true).Padding(0, 1),
		TabInactive: fg(FgMuted).Padding(0, 1),
		TabKey:      fg(Accent),
		Badge:       fg(Update).Bold(true),
		EngineName: map[string]lipgloss.Style{
			"docker": fg(EngineDocker).Bold(true),
			"incus":  fg(EngineIncus).Bold(true),
		},
		Separator: fg(Border),

		Panel:        lipgloss.NewStyle().Border(border).BorderForeground(c(Border)).Padding(0, 1),
		PanelFocused: lipgloss.NewStyle().Border(border).BorderForeground(c(BorderFocus)).Padding(0, 1),
		PanelTitle:   fg(Accent).Bold(true),
		PanelError:   lipgloss.NewStyle().Border(border).BorderForeground(c(Err)).Padding(0, 1),

		TableHeader: fg(FgMuted).Bold(true),

		Match:      lipgloss.NewStyle().Foreground(c(Bg)).Background(c(Warn)).Bold(true),
		CursorLine: lipgloss.NewStyle().Foreground(c(Fg)).Background(c(TreeCursorBg)),
		LogStdout:  fg(FgMuted),
		LogStderr:  fg(Err).Bold(true),
		LogSystem:  fg(Info),
		JSONKey:    fg(Accent),
		JSONString: fg(OK),
		JSONNumber: fg(AccentAlt),
		JSONOther:  fg(Info),

		ChartCPU:   fg(ChartCPU),
		ChartMem:   fg(ChartMem),
		ChartNetRx: fg(ChartNetRx),
		ChartNetTx: fg(ChartNetTx),
		ChartDisk:  fg(ChartDisk),
		GaugeEmpty: fg(Border),

		StatusBar: lipgloss.NewStyle().Foreground(c(FgMuted)).Background(c(BgAlt)),
		KeyHint:   lipgloss.NewStyle().Foreground(c(Accent)).Background(c(BgAlt)).Bold(true),
		KeyDesc:   lipgloss.NewStyle().Foreground(c(FgMuted)).Background(c(BgAlt)),
		Indicator: lipgloss.NewStyle().Foreground(c(Bg)).Background(c(Warn)).Bold(true).Padding(0, 1),

		Modal:        lipgloss.NewStyle().Border(border).BorderForeground(c(BorderFocus)).Background(c(BgAlt)).Foreground(c(Fg)).Padding(1, 2),
		ModalTitle:   lipgloss.NewStyle().Foreground(c(Accent)).Background(c(BgAlt)).Bold(true),
		Selected:     lipgloss.NewStyle().Foreground(c(SelectionFg)).Background(c(SelectionBg)).Bold(true),
		Button:       lipgloss.NewStyle().Foreground(c(Bg)).Background(c(Accent)).Bold(true).Padding(0, 1),
		ButtonDanger: lipgloss.NewStyle().Foreground(c(Bg)).Background(c(Err)).Bold(true).Padding(0, 1),
	}

	for lvl, tok := range [4]Token{Info, OK, Warn, Err} {
		s.levelFg[lvl] = fg(tok).Bold(true)
		s.toast[lvl] = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).BorderForeground(c(tok)).
			Foreground(c(Fg)).Background(c(BgAlt)).Padding(0, 1)
		s.banner[lvl] = lipgloss.NewStyle().Foreground(c(Bg)).Background(c(tok)).Bold(true).Padding(0, 1)
	}
	return s
}

// LevelText styles inline text (icons, labels) in a level's color.
func (s *Styles) LevelText(l Level) lipgloss.Style { return s.levelFg[clamp(l)] }

// Toast is the bordered toast box style for a level.
func (s *Styles) Toast(l Level) lipgloss.Style { return s.toast[clamp(l)] }

// Banner is the full-width banner style for a level.
func (s *Styles) Banner(l Level) lipgloss.Style { return s.banner[clamp(l)] }

// TerminalColors returns the colors the program asks the terminal to use as
// its default background and foreground (OSC 11 / OSC 10), so the theme's
// background fills the whole screen without per-cell painting.
func (s *Styles) TerminalColors() (bg, fg color.Color) {
	return s.Theme.Color(Bg), s.Theme.Color(Fg)
}

func clamp(l Level) Level {
	if l < LevelInfo || l > LevelErr {
		return LevelInfo
	}
	return l
}
