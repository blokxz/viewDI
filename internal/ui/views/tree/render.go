package tree

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/panel"
	"github.com/blokxz/viewDI/internal/ui/components/tree"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
)

// icons are the glyphs drawn before node names.
type icons struct {
	glyphs                                     tree.Glyphs
	docker, incus, group, other, port, mount   string
	also, multi, sep, collapsed, ruler, crumbs string
}

var (
	unicodeIcons = icons{
		glyphs: tree.UnicodeGlyphs, docker: "▣", incus: "▦", group: "◆", other: "◇", port: "⇢", mount: "▤",
		also: "⇄", multi: "⇄", sep: "·", collapsed: "▸", ruler: "─", crumbs: " › ",
	}
	asciiIcons = icons{
		glyphs: tree.ASCIIGlyphs, docker: "[D]", incus: "[I]", group: "#", other: "#", port: "->", mount: "=",
		also: "<>", multi: "<>", sep: "-", collapsed: "+", ruler: "-", crumbs: " > ",
	}
	// toASCII maps every other glyph the view draws (states, arrows,
	// sparklines, panel borders) to one ASCII character of the same width.
	toASCII = strings.NewReplacer(
		"●", "*", "▲", "!", "◐", "~", "⏸", "=", "↻", "~", "○", "o", "■", "-", "✖", "x", "…", "~",
		"·", "-", "→", "-", "↓", "v", "↑", "^", "▶", ">", "›", ">",
		"▁", "_", "▂", ".", "▃", "-", "▄", ":", "▅", "=", "▆", "+", "▇", "*", "█", "#",
		"─", "-", "│", "|", "╭", "+", "╮", "+", "╰", "+", "╯", "+",
	)
)

// Layout: the inspector sits beside the tree from wideWidth columns, below
// it (inspectorRows lines inside its border) on narrower screens.
const (
	wideWidth     = 100
	inspectorRows = 6
)

// split returns the tree and inspector panel sizes for a body.
func split(width, height int) (treeW, treeH, inspW, inspH int, beside bool) {
	if width >= wideWidth {
		treeW = width * 55 / 100
		return treeW, height, width - treeW, height, true
	}
	inspH = inspectorRows + 2
	if height < inspH+6 { // too short for both: tree only
		return width, height, 0, 0, false
	}
	return width, height - inspH, width, inspH, false
}

// treeRows is the number of rows the tree panel shows.
func treeRows(width, height int) int {
	_, h, _, _, _ := split(width, height)
	return h - 2
}

// Render implements views.View.
func (v *View) Render(ctx views.Context) string {
	s := ctx.Styles
	treeW, treeH, inspW, inspH, beside := split(ctx.Width, ctx.Height)
	t := v.t()
	innerW, rows := treeW-4, treeH-2
	var lines []string
	if v.typing || v.filter != "" {
		lines = append(lines, v.promptLine(s, innerW))
		rows--
	}
	if len(t.Rows()) == 0 {
		lines = append(lines, s.Muted.Render("No node matches the filter."))
	}
	lines = append(lines, t.Render(s, v.icons.glyphs, innerW, max(0, rows), func(r tree.Row) tree.Label { return v.label(s, r) })...)
	left := panel.Render(s, v.breadcrumb(), strings.Join(lines, "\n"), treeW, treeH, ctx.Focus == 0)
	if inspW == 0 {
		if v.opts.ASCII {
			return toASCII.Replace(left)
		}
		return left
	}
	right := panel.Render(s, "Inspector", v.inspector(s, inspW-4, inspH-2, !beside, ctx), inspW, inspH, false)
	out := left + "\n" + right
	if beside {
		out = joinHorizontal(left, right)
	}
	if v.opts.ASCII {
		out = toASCII.Replace(out)
	}
	return out
}

func joinHorizontal(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	var out strings.Builder
	for i := range max(len(al), len(bl)) {
		if i > 0 {
			out.WriteByte('\n')
		}
		if i < len(al) {
			out.WriteString(al[i])
		}
		if i < len(bl) {
			out.WriteString(bl[i])
		}
	}
	return out.String()
}

func (v *View) promptLine(s *theme.Styles, w int) string {
	n := len(v.t().Matches())
	count := fmt.Sprintf("  (%d match%s)", n, plural(n, "es"))
	if v.typing {
		return ansi.Truncate(s.Accent.Render("/")+s.Text.Render(textsafe.Clean(v.filter))+s.Selected.Render(" ")+s.Muted.Render(count+" · enter keep · esc clear"), w, "…")
	}
	return ansi.Truncate(s.Muted.Render("filter: ")+s.Text.Render(textsafe.Clean(v.filter))+s.Muted.Render(count+" · ctrl+n/p next · esc clear"), w, "…")
}

func plural(n int, suffix string) string {
	if n == 1 {
		return ""
	}
	return suffix
}

// breadcrumb is the cursor's path, e.g. "Docker › web_default › web-api".
func (v *View) breadcrumb() string {
	t := v.t()
	n, ok := t.Current()
	if !ok {
		return "Tree"
	}
	var parts []string
	for _, a := range t.Ancestors(n) {
		if p := v.crumb(a); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "viewDI"
	}
	return strings.Join(parts, v.icons.crumbs)
}

func (v *View) crumb(n *tree.Node) string {
	it := data(n)
	switch it.kind {
	case kEngine:
		return it.engine.Label()
	case kNetwork, kProject, kCompose, kOther:
		return textsafe.Clean(it.name)
	case kInstance:
		if in, ok := v.snap.Lookup(it.key); ok {
			return textsafe.Clean(in.Name)
		}
	case kPort:
		return fmt.Sprintf("%d/%s", it.port.ContainerPort, it.port.Protocol)
	case kMount:
		return textsafe.Clean(it.mount.Destination)
	case kAlso:
		return "also on " + textsafe.Clean(it.also)
	}
	return ""
}

// stateGlyph splits a views.StateCell text ("● running") into its glyph
// (ASCII-mapped when needed) and its words.
func (v *View) stateGlyph(text string) (string, string) {
	g, words, _ := strings.Cut(text, " ")
	return g, words
}

// label draws one row.
func (v *View) label(s *theme.Styles, r tree.Row) tree.Label {
	it := data(r.Node)
	var l tree.Label
	switch it.kind {
	case kRoot:
		l.Left = []tree.Seg{{Text: "viewDI", Style: s.Bold}}
		if v.snap != nil {
			l.Right = []tree.Seg{{Text: fmt.Sprintf("%d machines", len(v.snap.Instances)), Style: s.Muted}}
		}
	case kEngine:
		l = v.engineLabel(s, it.engine)
	case kNetwork, kProject, kCompose, kOther:
		icon, name := v.icons.group, it.name
		style := s.TreeGroup
		if it.kind == kOther {
			icon, style = v.icons.other, s.Muted
		}
		l.Left = []tree.Seg{{Text: icon + " ", Style: style}, {Text: textsafe.Clean(name), Style: style}}
		switch it.kind {
		case kNetwork:
			if len(it.net.Subnets) > 0 {
				l.Left = append(l.Left, tree.Seg{Text: "  " + textsafe.Clean(strings.Join(it.net.Subnets, ", ")), Style: s.Muted})
			}
			if it.engine == engine.Incus && it.net.Driver != "" {
				m := "unmanaged"
				if it.net.Managed {
					m = "managed"
				}
				l.Left = append(l.Left, tree.Seg{Text: "  " + m, Style: s.Muted})
			}
		case kProject:
			l.Left[1].Text = "project " + l.Left[1].Text
		}
		if !r.Open && r.Hidden > 0 {
			l.Left = append(l.Left, tree.Seg{Text: fmt.Sprintf("  %s %d", v.icons.collapsed, r.Hidden), Style: s.Muted})
		}
	case kInstance:
		l = v.instanceLabel(s, it)
	case kPort:
		p := it.port
		text := fmt.Sprintf("%d/%s", p.ContainerPort, p.Protocol)
		if p.HostPort != 0 {
			text = fmt.Sprintf("%s → %s", hostPort(p), text)
		}
		l.Left = []tree.Seg{{Text: v.icons.port + " ", Style: s.ChartNetRx}, {Text: text, Style: s.Text}}
	case kMount:
		m := it.mount
		l.Left = []tree.Seg{{Text: v.icons.mount + " ", Style: s.ChartDisk}, {Text: textsafe.Clean(m.Source) + " → " + textsafe.Clean(m.Destination), Style: s.Text}}
		if m.ReadOnly {
			l.Left = append(l.Left, tree.Seg{Text: "  ro", Style: s.Muted})
		}
	case kAlso:
		l.Left = []tree.Seg{{Text: v.icons.also + " ", Style: s.TreeGroup}, {Text: "also on: ", Style: s.Muted}, {Text: textsafe.Clean(it.also), Style: s.TreeGroup}}
	}
	return l
}

func hostPort(p engine.Port) string {
	ip := p.HostIP
	if strings.Contains(ip, ":") {
		ip = "[" + ip + "]"
	}
	if ip == "" {
		return fmt.Sprint(p.HostPort)
	}
	return fmt.Sprintf("%s:%d", ip, p.HostPort)
}

func (v *View) engineLabel(s *theme.Styles, id engine.ID) tree.Label {
	ev := v.snap.Engine(id)
	icon := v.icons.docker
	if id == engine.Incus {
		icon = v.icons.incus
	}
	style := s.EngineName[string(id)]
	l := tree.Label{Left: []tree.Seg{{Text: icon + " ", Style: style}, {Text: id.Label(), Style: style}}}
	if ev.Info.Version != "" {
		l.Left = append(l.Left, tree.Seg{Text: " " + textsafe.Clean(ev.Info.Version), Style: s.Muted})
	}
	if id == engine.Incus {
		p := "all projects"
		if ev.Project != "" {
			p = "project " + ev.Project
		}
		l.Left = append(l.Left, tree.Seg{Text: " " + v.icons.sep + " " + textsafe.Clean(p), Style: s.Muted})
	}
	switch ev.Health.State {
	case store.StateUnavailable:
		l.Right = []tree.Seg{{Text: "✖ unavailable", Style: s.LevelText(theme.LevelErr)}}
	case store.StateDegraded:
		l.Right = []tree.Seg{{Text: "reconnecting…", Style: s.LevelText(theme.LevelWarn)}}
	default:
		if !ev.InfoKnown {
			l.Right = []tree.Seg{{Text: "connecting…", Style: s.Muted}}
			break
		}
		c := ev.Counts
		text := fmt.Sprintf("%d ▶%d ■%d", c.Total, c.Running, c.Stopped)
		if v.opts.ASCII {
			text = fmt.Sprintf("%d up %d down %d", c.Total, c.Running, c.Stopped)
		}
		if ev.Stale {
			text = "stale · " + text
		}
		l.Right = []tree.Seg{{Text: text, Style: s.Muted}}
	}
	return l
}

func (v *View) instanceLabel(s *theme.Styles, it item) tree.Label {
	in, ok := v.snap.Lookup(it.key)
	if !ok {
		return tree.Label{Left: []tree.Seg{{Text: "? " + string(it.key), Style: s.Muted}}}
	}
	text, level, muted := views.DisplayState(v.snap, in)
	glyph, words := v.stateGlyph(text)
	if strings.HasPrefix(text, "… ") { // in flight: the current state's glyph, then "… stopping"
		base, _, _ := views.StateCell(in)
		glyph, _ = v.stateGlyph(base)
		words = text
	}
	gs := s.LevelText(level)
	if muted {
		gs = s.Muted
	}
	stale := v.snap.Engine(in.Engine).Stale
	name := s.Text
	if stale {
		gs, name = s.Muted, s.Muted
	}
	l := tree.Label{Left: []tree.Seg{{Text: glyph + " ", Style: gs}, {Text: textsafe.Clean(in.Name), Style: name}}}
	if in.Kind == engine.KindVM {
		l.Left = append(l.Left, tree.Seg{Text: "  vm", Style: s.Muted})
	}
	if in.Engine == engine.Incus && in.Project != "" && in.Project != "default" && v.grouping != ByProject {
		l.Left = append(l.Left, tree.Seg{Text: "  [" + textsafe.Clean(in.Project) + "]", Style: s.Muted})
	}
	if it.secondary {
		l.Left = append(l.Left, tree.Seg{Text: "  " + v.icons.multi, Style: s.TreeGroup})
	}
	if words != "running" {
		l.Left = append(l.Left, tree.Seg{Text: "  " + words, Style: gs})
	}
	return l
}

// fitLines joins lines into exactly w×h cells.
func fitLines(lines []string, w, h int) string { return layout.Fit(strings.Join(lines, "\n"), w, h) }
