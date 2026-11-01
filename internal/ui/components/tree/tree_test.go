package tree

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/theme"
)

// n builds a node; groups (with children) are open by default.
func n(id string, kids ...*Node) *Node {
	return &Node{ID: id, Children: kids, DefaultOpen: len(kids) > 0 && !strings.HasPrefix(id, "c:")}
}

// sample:
//
//	root
//	├── a
//	│   ├── a1
//	│   └── a2
//	├── c:b  (closed by default)
//	│   └── b1
//	└── c
//	    └── c1
//	        └── c1x
func sample() []*Node {
	return []*Node{n("root",
		n("a", n("a1"), n("a2")),
		n("c:b", n("b1")),
		n("c", n("c1", n("c1x"))),
	)}
}

func ids(m *Model) []string {
	var out []string
	for _, r := range m.Rows() {
		out = append(out, r.Node.ID)
	}
	return out
}

func cur(m *Model) string {
	nd, _ := m.Current()
	if nd == nil {
		return ""
	}
	return nd.ID
}

func TestFlattenHonoursDefaults(t *testing.T) {
	m := New(nil)
	m.SetRoots(sample())
	want := []string{"root", "a", "a1", "a2", "c:b", "c", "c1", "c1x"}
	if got := ids(m); !slices.Equal(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	if r := m.Rows()[4]; r.Open || !r.HasKids || r.Hidden != 1 {
		t.Fatalf("closed group row %+v", r)
	}
}

func TestNavigation(t *testing.T) {
	tests := []struct {
		name string
		do   func(m *Model)
		want string
		rows int
	}{
		{"down", func(m *Model) { m.Move(2) }, "a1", 8},
		{"clamped", func(m *Model) { m.Move(99) }, "c1x", 8},
		{"end then home", func(m *Model) { m.End(); m.Home() }, "root", 8},
		{"right opens a closed node", func(m *Model) { m.Select("c:b"); m.Right() }, "c:b", 9},
		{"right on an open node enters it", func(m *Model) { m.Select("a"); m.Right() }, "a1", 8},
		{"left closes an open node", func(m *Model) { m.Select("a"); m.Left() }, "a", 6},
		{"left on a leaf goes to the parent", func(m *Model) { m.Select("a2"); m.Left() }, "a", 8},
		{"next sibling skips the subtree", func(m *Model) { m.Select("a"); m.NextSibling() }, "c:b", 8},
		{"prev sibling", func(m *Model) { m.Select("c"); m.PrevSibling(); m.PrevSibling() }, "a", 8},
		{"no sibling past the last", func(m *Model) { m.Select("a2"); m.NextSibling() }, "a2", 8},
		{"toggle", func(m *Model) { m.Select("c"); m.Toggle() }, "c", 6},
		{"expand all below", func(m *Model) { m.Select("c"); m.ExpandAll() }, "c", 8},
		{"collapse all keeps the top level", func(m *Model) { m.Select("a2"); m.CollapseAll() }, "a", 4},
		{"select opens ancestors", func(m *Model) { m.Select("c1x") }, "c1x", 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(nil)
			m.SetRoots(sample())
			tt.do(m)
			if cur(m) != tt.want || len(m.Rows()) != tt.rows {
				t.Fatalf("cursor %q rows %v; want %q with %d rows", cur(m), ids(m), tt.want, tt.rows)
			}
		})
	}
}

func TestToggledPersists(t *testing.T) {
	m := New(nil)
	m.SetRoots(sample())
	m.Select("a")
	m.Left() // a closed (default open)
	m.Select("c:b")
	m.Right() // b opened (default closed)
	got := m.Toggled()
	if !slices.Equal(got, []string{"a", "c:b"}) {
		t.Fatalf("toggled %v", got)
	}
	m2 := New(got)
	m2.SetRoots(sample())
	if want := []string{"root", "a", "c:b", "b1", "c", "c1", "c1x"}; !slices.Equal(ids(m2), want) {
		t.Fatalf("restored rows %v, want %v", ids(m2), want)
	}
	m2.Select("a")
	m2.Right() // back to its default: no longer toggled
	if slices.Contains(m2.Toggled(), "a") {
		t.Fatalf("toggled %v", m2.Toggled())
	}
}

func TestCursorSurvivesRebuilds(t *testing.T) {
	m := New(nil)
	m.SetRoots(sample())
	m.Select("a2")
	// Same IDs, new node values (a refresh): the cursor stays.
	m.SetRoots(sample())
	if cur(m) != "a2" {
		t.Fatalf("cursor %q after a refresh", cur(m))
	}
	// A node inserted above moves the row, not the cursor's node.
	m.SetRoots([]*Node{n("root", n("a", n("a0"), n("a1"), n("a2")), n("c", n("c1")))})
	if cur(m) != "a2" || m.Cursor() != 4 {
		t.Fatalf("cursor %q at %d", cur(m), m.Cursor())
	}
	// The cursor's node disappears: the sibling at its place takes it.
	m.SetRoots([]*Node{n("root", n("a", n("a0"), n("a1")), n("c", n("c1")))})
	if cur(m) != "a1" {
		t.Fatalf("cursor %q after its node went", cur(m))
	}
	// The whole group disappears: the nearest ancestor.
	m.SetRoots([]*Node{n("root", n("c", n("c1")))})
	if cur(m) != "root" {
		t.Fatalf("cursor %q after its group went", cur(m))
	}
	m.SetRoots(nil)
	if _, ok := m.Current(); ok || len(m.Rows()) != 0 {
		t.Fatal("empty tree has a cursor")
	}
}

func TestFilterKeepsAncestors(t *testing.T) {
	m := New(nil)
	m.SetRoots(sample())
	m.Select("a1")
	m.SetFilter(func(nd *Node) bool { return strings.HasSuffix(nd.ID, "1") || nd.ID == "c1x" })
	// b1 lives under a closed group: the filter shows it anyway.
	if want := []string{"root", "a", "a1", "c:b", "b1", "c", "c1", "c1x"}; !slices.Equal(ids(m), want) {
		t.Fatalf("filtered rows %v, want %v", ids(m), want)
	}
	if got := m.Matches(); !slices.Equal(got, []string{"a1", "b1", "c1", "c1x"}) || !m.IsMatch("b1") || m.IsMatch("a") {
		t.Fatalf("matches %v", got)
	}
	m.NextMatch()
	m.NextMatch()
	if cur(m) != "c1" {
		t.Fatalf("next match %q", cur(m))
	}
	m.PrevMatch()
	m.PrevMatch()
	m.PrevMatch() // wraps
	if cur(m) != "c1x" {
		t.Fatalf("prev match %q", cur(m))
	}
	if m.Toggle() || m.ExpandAll() || m.CollapseAll() {
		t.Fatal("expansion changed while filtering")
	}
	m.SetFilter(nil)
	if want := []string{"root", "a", "a1", "a2", "c:b", "c", "c1", "c1x"}; !slices.Equal(ids(m), want) {
		t.Fatalf("rows after the filter %v, want %v", ids(m), want)
	}
	if cur(m) != "c1x" {
		t.Fatalf("cursor %q after the filter", cur(m))
	}
}

func TestFilterMovesCursorToFirstMatch(t *testing.T) {
	m := New(nil)
	m.SetRoots(sample())
	m.Select("a2")
	m.SetFilter(func(nd *Node) bool { return nd.ID == "c1" })
	if cur(m) != "c1" {
		t.Fatalf("cursor %q", cur(m))
	}
	m.SetFilter(func(*Node) bool { return false })
	if len(m.Rows()) != 0 || len(m.Matches()) != 0 {
		t.Fatalf("rows %v without matches", ids(m))
	}
}

func TestScrollKeepsCursorVisible(t *testing.T) {
	var kids []*Node
	for i := range 50 {
		kids = append(kids, n(string(rune('A'+i%26))+strings.Repeat("x", i/26)))
	}
	m := New(nil)
	m.SetRoots([]*Node{{ID: "root", Children: kids, DefaultOpen: true}})
	m.SetPage(10)
	m.Move(25)
	if o := m.Offset(); 25 < o || 25 >= o+10 {
		t.Fatalf("offset %d for cursor 25", o)
	}
	m.PageDown()
	m.PageDown()
	if m.Cursor() != 45 {
		t.Fatalf("cursor %d", m.Cursor())
	}
	m.End()
	m.PageUp()
	if m.Cursor() != 40 {
		t.Fatalf("cursor %d", m.Cursor())
	}
}

func TestRenderGuides(t *testing.T) {
	m := New(nil)
	m.SetRoots(sample())
	m.Select("c1")
	s := theme.NewStyles(theme.Fallback(), 1)
	label := func(r Row) Label {
		l := Label{Left: []Seg{{Text: r.Node.ID, Style: s.Text}}}
		if r.Node.ID == "root" {
			l.Right = []Seg{{Text: "7", Style: s.Muted}}
		}
		return l
	}
	lines := m.Render(s, UnicodeGlyphs, 24, 20, label)
	var plain []string
	for _, l := range lines {
		if w := ansi.StringWidth(l); w != 24 {
			t.Fatalf("line %q is %d wide", ansi.Strip(l), w)
		}
		plain = append(plain, strings.TrimRight(ansi.Strip(l), " "))
	}
	want := []string{
		" root                  7",
		" ├── a",
		" │   ├── a1",
		" │   └── a2",
		" ├── c:b",
		" └── c",
		"▌    └── c1",
		"         └── c1x",
	}
	if !slices.Equal(plain, want) {
		t.Fatalf("render:\n%s\nwant:\n%s", strings.Join(plain, "\n"), strings.Join(want, "\n"))
	}
	ascii := m.Render(s, ASCIIGlyphs, 24, 3, label)
	if len(ascii) != 3 || !strings.Contains(ansi.Strip(ascii[2]), ">    `-- c1") || strings.Contains(ansi.Strip(ascii[0]), "root") {
		t.Fatalf("ascii window %q", ascii)
	}
}

func TestRenderTruncatesLongLabels(t *testing.T) {
	m := New(nil)
	m.SetRoots([]*Node{{ID: "a-very-long-node-name-indeed"}})
	s := theme.NewStyles(theme.Fallback(), 1)
	for _, cursor := range []bool{true, false} {
		if !cursor {
			m.SetRoots([]*Node{{ID: "x"}, {ID: "a-very-long-node-name-indeed"}})
			m.Home()
		}
		for _, l := range m.Render(s, UnicodeGlyphs, 12, 5, func(r Row) Label {
			return Label{Left: []Seg{{Text: r.Node.ID, Style: s.Text}}, Right: []Seg{{Text: "99", Style: s.Muted}}}
		}) {
			if w := ansi.StringWidth(l); w != 12 {
				t.Fatalf("line %q is %d wide", ansi.Strip(l), w)
			}
		}
	}
}

func BenchmarkRebuild1000(b *testing.B) {
	var groups []*Node
	for g := range 40 {
		var kids []*Node
		for i := range 25 {
			kids = append(kids, &Node{ID: string(rune('a'+g%26)) + string(rune('0'+g/26)) + "/" + string(rune('A'+i)), Children: []*Node{{ID: "leaf"}}})
		}
		groups = append(groups, &Node{ID: string(rune('a'+g%26)) + string(rune('0'+g/26)), Children: kids, DefaultOpen: true})
	}
	roots := []*Node{{ID: "root", Children: groups, DefaultOpen: true}}
	m := New(nil)
	b.ReportAllocs()
	for b.Loop() {
		m.SetRoots(roots)
	}
}
