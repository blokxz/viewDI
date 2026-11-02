package theme

import "charm.land/lipgloss/v2"

// newMonoStyles builds the style cache of a monochrome theme (NO_COLOR or
// [ui] monochrome). No style carries a colour: meaning comes from bold,
// faint, reverse and underline, from border shapes (rounded, thick, double)
// and from the glyphs the views already print next to every state.
//
// The distinctions that colour used to carry:
//
//	selection, active tab, buttons ...... reverse
//	muted / secondary text .............. faint
//	warnings ............................ bold
//	errors .............................. bold + underline
//	update badge ........................ bold + underline
//	focused panel ....................... thick border
//	error panel ......................... double border
func newMonoStyles(t *Theme, version uint64) *Styles {
	plain := lipgloss.NewStyle()
	bold := plain.Bold(true)
	faint := plain.Faint(true)
	rev := plain.Reverse(true)
	revBold := rev.Bold(true)
	alert := bold.Underline(true)

	rounded, thick, double := lipgloss.RoundedBorder(), lipgloss.ThickBorder(), lipgloss.DoubleBorder()
	box := func(b lipgloss.Border) lipgloss.Style { return plain.Border(b).Padding(0, 1) }

	s := &Styles{
		Theme:   t,
		Version: version,
		Text:    plain,
		Muted:   faint,
		Bold:    bold,
		Accent:  bold,

		AppTitle:    bold,
		TabActive:   revBold.Padding(0, 1),
		TabInactive: faint.Padding(0, 1),
		TabKey:      bold,
		Badge:       alert,
		EngineName:  map[string]lipgloss.Style{"docker": bold, "incus": bold},
		Separator:   faint,

		Panel:        box(rounded),
		PanelFocused: box(thick),
		PanelTitle:   bold,
		PanelError:   box(double),

		TableHeader: faint.Bold(true),

		Match:      revBold,
		CursorLine: rev,
		LogStdout:  plain,
		LogStderr:  alert,
		LogSystem:  faint,
		JSONKey:    bold,
		JSONString: plain,
		JSONNumber: plain,
		JSONOther:  faint,

		TreeGuide:  faint,
		TreeGroup:  bold,
		TreeCursor: revBold,
		TreeLab:    bold,

		LinkIdle:    faint,
		LinkIdleHot: plain,
		LinkActive:  bold,
		LinkHot:     alert,
		LinkDot:     revBold,

		ChartCPU:   bold,
		ChartMem:   plain,
		ChartNetRx: plain,
		ChartNetTx: faint,
		ChartDisk:  faint.Bold(true),
		GaugeEmpty: faint,

		StatusBar: plain,
		KeyHint:   bold,
		KeyDesc:   faint,
		Indicator: revBold.Padding(0, 1),

		Modal:        box(thick).Padding(1, 2),
		ModalTitle:   bold,
		Selected:     revBold,
		Button:       revBold.Padding(0, 1),
		ButtonDanger: revBold.Underline(true).Padding(0, 1),
	}

	s.levelFg = [4]lipgloss.Style{LevelInfo: plain, LevelOK: plain, LevelWarn: bold, LevelErr: alert}
	borders := [4]lipgloss.Border{rounded, rounded, thick, double}
	for lvl := range s.toast {
		s.toast[lvl] = box(borders[lvl])
	}
	s.banner = [4]lipgloss.Style{
		LevelInfo: rev.Padding(0, 1),
		LevelOK:   revBold.Padding(0, 1),
		LevelWarn: rev.Underline(true).Padding(0, 1),
		LevelErr:  revBold.Underline(true).Padding(0, 1),
	}
	return s
}
