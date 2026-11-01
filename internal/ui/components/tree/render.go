package tree

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Seg is a piece of a label with its style.
type Seg struct {
	Text  string
	Style lipgloss.Style
}

// Label is what a caller draws for a node: left-aligned segments and
// optional right-aligned ones (counts, badges).
type Label struct {
	Left, Right []Seg
}

// Glyphs are the guide pieces and the cursor marker.
type Glyphs struct {
	Branch, Last, Pipe, Space, Cursor string
}

// Unicode and ASCII guides.
var (
	UnicodeGlyphs = Glyphs{Branch: "├── ", Last: "└── ", Pipe: "│   ", Space: "    ", Cursor: "▌"}
	ASCIIGlyphs   = Glyphs{Branch: "|-- ", Last: "`-- ", Pipe: "|   ", Space: "    ", Cursor: ">"}
)

// Prefix returns the guide string of a row.
func (g Glyphs) Prefix(r Row) string {
	if r.Depth == 0 {
		return ""
	}
	var b strings.Builder
	for _, last := range r.Last[:len(r.Last)-1] {
		if last {
			b.WriteString(g.Space)
		} else {
			b.WriteString(g.Pipe)
		}
	}
	if r.Last[len(r.Last)-1] {
		b.WriteString(g.Last)
	} else {
		b.WriteString(g.Branch)
	}
	return b.String()
}

// Render draws the rows on screen, each exactly width cells wide: a marker
// column, the guides, and the label. The cursor row is drawn in the
// CursorLine style across the whole width (its label without colours, so
// the background is unbroken). rows is the number of lines available.
func (m *Model) Render(s *theme.Styles, g Glyphs, width, rows int, label func(Row) Label) []string {
	offset := m.offset
	if m.cursor >= offset+rows { // the screen shrank since the last scroll
		offset = m.cursor - rows + 1
	}
	if m.cursor < offset {
		offset = m.cursor
	}
	offset = max(0, offset)
	out := make([]string, 0, rows)
	for i := offset; i < len(m.rows) && i < offset+rows; i++ {
		out = append(out, m.line(s, g, m.rows[i], i == m.cursor, width, label))
	}
	return out
}

func (m *Model) line(s *theme.Styles, g Glyphs, r Row, cursor bool, width int, label func(Row) Label) string {
	if width <= 1 {
		return strings.Repeat(" ", max(0, width))
	}
	lb := label(r)
	w := width - 1 // the marker column
	prefix := g.Prefix(r)
	if pw, room := ansi.StringWidth(prefix), max(0, w-8); pw > room { // very deep: keep room for the label
		prefix = ansi.TruncateLeft(prefix, pw-room, "…")
	}
	left, right := plain(lb.Left), plain(lb.Right)
	lw := ansi.StringWidth(prefix) + ansi.StringWidth(left)
	rw := ansi.StringWidth(right)
	showRight := rw > 0 && lw+1+rw <= w
	if cursor {
		text := prefix + left
		if showRight {
			text += strings.Repeat(" ", w-lw-rw) + right
		}
		return s.TreeCursor.Render(g.Cursor) + s.CursorLine.Render(fit(text, w))
	}
	var b strings.Builder
	b.WriteString(" ")
	b.WriteString(s.TreeGuide.Render(prefix))
	budget := w - ansi.StringWidth(prefix)
	if showRight {
		budget = w - lw - rw + ansi.StringWidth(left) // left fits by construction
	}
	used := 0
	for _, sg := range lb.Left {
		if used >= budget {
			break
		}
		t := sg.Text
		if tw := ansi.StringWidth(t); used+tw > budget {
			t = ansi.Truncate(t, budget-used, "…")
		}
		b.WriteString(sg.Style.Render(t))
		used += ansi.StringWidth(t)
	}
	pad := w - ansi.StringWidth(prefix) - used
	if showRight {
		pad -= rw
	}
	b.WriteString(strings.Repeat(" ", max(0, pad)))
	if showRight {
		for _, sg := range lb.Right {
			b.WriteString(sg.Style.Render(sg.Text))
		}
	}
	return b.String()
}

func plain(segs []Seg) string {
	var b strings.Builder
	for _, sg := range segs {
		b.WriteString(sg.Text)
	}
	return b.String()
}

// fit truncates or pads s to exactly w cells.
func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	if n := w - ansi.StringWidth(s); n > 0 {
		s += strings.Repeat(" ", n)
	}
	return s
}
