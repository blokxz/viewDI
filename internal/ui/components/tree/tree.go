// Package tree is a navigable, virtualised tree: nodes with stable IDs, a
// flattened list of the visible nodes rebuilt only when the data, the
// expansion or the filter change, a cursor that follows a node ID across
// rebuilds, and rendering of just the rows on screen.
//
// The package knows nothing about engines: callers build the nodes and
// draw each node's label.
package tree

import (
	"slices"
)

// Node is one tree node. IDs must be unique and stable across rebuilds:
// the cursor and the expansion state follow them.
type Node struct {
	ID       string
	Children []*Node
	// DefaultOpen is the expansion a node has until the user toggles it.
	DefaultOpen bool
	// Data is the caller's payload.
	Data any
}

// Row is one visible node.
type Row struct {
	Node   *Node
	Depth  int
	Parent int // row index of the parent, -1 at the top level
	// Last records, for every level from 1 to Depth, whether the node's
	// ancestor at that level (the node itself last) is the last visible
	// child of its parent; the guides are drawn from it.
	Last []bool
	// Open reports whether the node's children are shown; HasKids whether
	// it has any (visible) children.
	Open, HasKids bool
	// Hidden counts children not shown because the node is closed.
	Hidden int
}

// Model is the tree state.
type Model struct {
	roots   []*Node
	parent  map[string]*Node // child ID → parent (nil at the top level)
	byID    map[string]*Node
	toggled map[string]bool // nodes whose expansion differs from DefaultOpen

	rows  []Row
	index map[string]int // node ID → row

	cursor   int
	cursorID string
	trail    []string // the cursor's ancestors, nearest last
	sibling  int      // the cursor's position among its siblings
	offset   int
	page     int

	filter  func(*Node) bool
	matches []string        // matching node IDs, in tree order
	matched map[string]bool // the same, as a set
	shown   map[string]bool // matches and their ancestors
}

// New returns an empty tree. toggled is the persisted set of nodes whose
// expansion differs from their default (it may be nil).
func New(toggled []string) *Model {
	m := &Model{toggled: map[string]bool{}, page: 10}
	for _, id := range toggled {
		m.toggled[id] = true
	}
	return m
}

// SetRoots replaces the nodes. The cursor stays on its node; if that node
// is gone it moves to the nearest remaining sibling, else to the nearest
// remaining ancestor.
func (m *Model) SetRoots(roots []*Node) {
	m.roots = roots
	m.parent = map[string]*Node{}
	m.byID = map[string]*Node{}
	var walk func(p *Node, ns []*Node)
	walk = func(p *Node, ns []*Node) {
		for _, n := range ns {
			m.parent[n.ID] = p
			m.byID[n.ID] = n
			walk(n, n.Children)
		}
	}
	walk(nil, roots)
	m.rebuild()
}

// Node returns the node with id, if it exists (shown or not).
func (m *Model) Node(id string) (*Node, bool) {
	n, ok := m.byID[id]
	return n, ok
}

// Parent returns a node's parent (nil at the top level).
func (m *Model) Parent(id string) *Node { return m.parent[id] }

// Ancestors returns the path from the top level down to n, n included.
func (m *Model) Ancestors(n *Node) []*Node {
	var out []*Node
	for ; n != nil; n = m.parent[n.ID] {
		out = append(out, n)
	}
	slices.Reverse(out)
	return out
}

// Rows returns the visible rows.
func (m *Model) Rows() []Row { return m.rows }

// Cursor returns the row index of the cursor.
func (m *Model) Cursor() int { return m.cursor }

// Current returns the node under the cursor.
func (m *Model) Current() (*Node, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil, false
	}
	return m.rows[m.cursor].Node, true
}

// Offset returns the first row on screen.
func (m *Model) Offset() int { return m.offset }

// SetPage sets how many rows fit on screen.
func (m *Model) SetPage(n int) {
	m.page = max(1, n)
	m.scroll()
}

// Toggled returns the nodes whose expansion differs from their default,
// sorted, for persistence.
func (m *Model) Toggled() []string {
	out := make([]string, 0, len(m.toggled))
	for id := range m.toggled {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// IsOpen reports whether a node is expanded (ignoring the filter).
func (m *Model) IsOpen(n *Node) bool { return n.DefaultOpen != m.toggled[n.ID] }

// SetOpen expands or collapses a node.
func (m *Model) SetOpen(n *Node, open bool) {
	if open == n.DefaultOpen {
		delete(m.toggled, n.ID)
	} else {
		m.toggled[n.ID] = true
	}
}

// Filtering reports whether a filter is applied.
func (m *Model) Filtering() bool { return m.filter != nil }

// Matches returns the IDs of the nodes matching the filter, in tree order.
func (m *Model) Matches() []string { return m.matches }

// IsMatch reports whether a node matches the filter.
func (m *Model) IsMatch(id string) bool {
	return m.matched[id]
}

// SetFilter shows only the nodes for which match is true, with their
// ancestors (expanded); nil clears it and restores the user's expansion.
// When the cursor's node is hidden by the filter it moves to the first
// match.
func (m *Model) SetFilter(match func(*Node) bool) {
	m.filter = match
	m.rebuild()
	if match != nil && len(m.matches) > 0 && !m.IsMatch(m.cursorID) {
		m.Select(m.matches[0])
	}
}

// rebuild flattens the visible nodes and puts the cursor back.
func (m *Model) rebuild() {
	m.matches, m.matched, m.shown = nil, nil, nil
	if m.filter != nil {
		m.shown, m.matched = map[string]bool{}, map[string]bool{}
		var mark func(ns []*Node) bool
		mark = func(ns []*Node) bool {
			found := false
			for _, n := range ns {
				hit := m.filter(n)
				if hit {
					m.matches = append(m.matches, n.ID)
					m.matched[n.ID] = true
				}
				if mark(n.Children) || hit {
					m.shown[n.ID] = true
					found = true
				}
			}
			return found
		}
		mark(m.roots)
	}
	m.rows = m.rows[:0]
	m.index = make(map[string]int, len(m.byID))
	var walk func(ns []*Node, depth, parent int, last []bool)
	walk = func(ns []*Node, depth, parent int, last []bool) {
		vis := m.visible(ns)
		for i, n := range vis {
			kids := m.visible(n.Children)
			open := len(kids) > 0 && m.open(n)
			l := last
			if depth > 0 {
				l = append(slices.Clip(last), i == len(vis)-1)
			}
			row := Row{Node: n, Depth: depth, Parent: parent, Last: l, Open: open, HasKids: len(kids) > 0}
			if !open {
				row.Hidden = len(kids)
			}
			m.index[n.ID] = len(m.rows)
			m.rows = append(m.rows, row)
			if open {
				walk(n.Children, depth+1, len(m.rows)-1, l)
			}
		}
	}
	walk(m.roots, 0, -1, nil)
	m.restoreCursor()
}

// visible filters a child list by the filter.
func (m *Model) visible(ns []*Node) []*Node {
	if m.filter == nil {
		return ns
	}
	out := make([]*Node, 0, len(ns))
	for _, n := range ns {
		if m.shown[n.ID] {
			out = append(out, n)
		}
	}
	return out
}

// open is the displayed expansion: while filtering, the ancestors of a
// match are open and a match's own children stay hidden unless they match.
func (m *Model) open(n *Node) bool {
	if m.filter != nil {
		return true // visible() already keeps only matches and their ancestors
	}
	return m.IsOpen(n)
}

// restoreCursor puts the cursor back on its node, a sibling or an ancestor.
func (m *Model) restoreCursor() {
	if len(m.rows) == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	if i, ok := m.index[m.cursorID]; ok {
		m.setCursor(i)
		return
	}
	// The node is gone or hidden: a sibling at the same place, then the
	// nearest ancestor still shown.
	for k := len(m.trail) - 1; k >= 0; k-- {
		pi, ok := m.index[m.trail[k]]
		if !ok {
			continue
		}
		if k == len(m.trail)-1 && m.rows[pi].Open {
			kids := m.childRows(pi)
			if len(kids) > 0 {
				m.setCursor(kids[min(m.sibling, len(kids)-1)])
				return
			}
		}
		m.setCursor(pi)
		return
	}
	if m.cursorID == "" {
		m.setCursor(0)
		return
	}
	m.setCursor(min(m.cursor, len(m.rows)-1))
}

// childRows returns the row indexes of a row's visible children.
func (m *Model) childRows(i int) []int {
	var out []int
	d := m.rows[i].Depth
	for j := i + 1; j < len(m.rows) && m.rows[j].Depth > d; j++ {
		if m.rows[j].Parent == i {
			out = append(out, j)
		}
	}
	return out
}

func (m *Model) setCursor(i int) {
	if len(m.rows) == 0 {
		return
	}
	i = max(0, min(i, len(m.rows)-1))
	m.cursor = i
	r := m.rows[i]
	m.cursorID = r.Node.ID
	m.trail = m.trail[:0]
	for p := m.parent[r.Node.ID]; p != nil; p = m.parent[p.ID] {
		m.trail = append(m.trail, p.ID)
	}
	slices.Reverse(m.trail)
	m.sibling = 0
	if r.Parent >= 0 {
		m.sibling = slices.Index(m.childRows(r.Parent), i)
	} else {
		for j := 0; j < i; j++ {
			if m.rows[j].Depth == 0 {
				m.sibling++
			}
		}
	}
	m.scroll()
}

// scroll keeps the cursor inside the visible window.
func (m *Model) scroll() {
	switch {
	case m.cursor < m.offset:
		m.offset = m.cursor
	case m.cursor >= m.offset+m.page:
		m.offset = m.cursor - m.page + 1
	}
	m.offset = max(0, min(m.offset, len(m.rows)-m.page))
}

// Select moves the cursor to a node, opening its ancestors. It reports
// false if the node does not exist or the filter hides it.
func (m *Model) Select(id string) bool {
	n, ok := m.byID[id]
	if !ok {
		return false
	}
	if _, shown := m.index[id]; !shown {
		if m.filter != nil {
			return false
		}
		changed := false
		for p := m.parent[n.ID]; p != nil; p = m.parent[p.ID] {
			if !m.IsOpen(p) {
				m.SetOpen(p, true)
				changed = true
			}
		}
		if changed {
			m.rebuild()
		}
	}
	i, ok := m.index[id]
	if ok {
		m.setCursor(i)
	}
	return ok
}

// Move moves the cursor by delta rows, clamped.
func (m *Model) Move(delta int) { m.setCursor(m.cursor + delta) }

// Home and End jump to the first and last row.
func (m *Model) Home() { m.setCursor(0) }

// End jumps to the last row.
func (m *Model) End() { m.setCursor(len(m.rows) - 1) }

// PageUp moves one screen up.
func (m *Model) PageUp() { m.Move(-m.page) }

// PageDown moves one screen down.
func (m *Model) PageDown() { m.Move(m.page) }

// Right expands the node, or moves to its first child if it is open. It
// reports whether the expansion changed.
func (m *Model) Right() bool {
	if m.cursor >= len(m.rows) {
		return false
	}
	r := m.rows[m.cursor]
	switch {
	case !r.HasKids:
		return false
	case !r.Open:
		m.SetOpen(r.Node, true)
		m.rebuild()
		return true
	}
	m.setCursor(m.cursor + 1)
	return false
}

// Left collapses the node, or moves to its parent if it is closed or a
// leaf. It reports whether the expansion changed.
func (m *Model) Left() bool {
	if m.cursor >= len(m.rows) {
		return false
	}
	r := m.rows[m.cursor]
	if r.Open && m.filter == nil {
		m.SetOpen(r.Node, false)
		m.rebuild()
		return true
	}
	if r.Parent >= 0 {
		m.setCursor(r.Parent)
	}
	return false
}

// Toggle opens or closes the node under the cursor. It reports whether the
// expansion changed.
func (m *Model) Toggle() bool {
	if m.cursor >= len(m.rows) || m.filter != nil {
		return false
	}
	r := m.rows[m.cursor]
	if !r.HasKids {
		return false
	}
	m.SetOpen(r.Node, !r.Open)
	m.rebuild()
	return true
}

// ExpandAll opens the node under the cursor and everything below it.
func (m *Model) ExpandAll() bool {
	n, ok := m.Current()
	if !ok || m.filter != nil {
		return false
	}
	var walk func(n *Node)
	walk = func(n *Node) {
		if len(n.Children) > 0 {
			m.SetOpen(n, true)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(n)
	m.rebuild()
	return true
}

// CollapseAll closes every node below the top level; the cursor moves to
// its top-level ancestor's child it was under.
func (m *Model) CollapseAll() bool {
	if m.filter != nil {
		return false
	}
	keep := ""
	if n, ok := m.Current(); ok {
		path := m.Ancestors(n)
		keep = path[min(1, len(path)-1)].ID
	}
	var walk func(ns []*Node, depth int)
	walk = func(ns []*Node, depth int) {
		for _, n := range ns {
			if len(n.Children) > 0 {
				m.SetOpen(n, depth == 0)
			}
			walk(n.Children, depth+1)
		}
	}
	walk(m.roots, 0)
	m.rebuild()
	if keep != "" {
		m.Select(keep)
	}
	return true
}

// NextSibling and PrevSibling move to the next or previous sibling,
// skipping subtrees.
func (m *Model) NextSibling() {
	if m.cursor >= len(m.rows) {
		return
	}
	r := m.rows[m.cursor]
	for j := m.cursor + 1; j < len(m.rows); j++ {
		if m.rows[j].Depth < r.Depth {
			return
		}
		if m.rows[j].Depth == r.Depth {
			m.setCursor(j)
			return
		}
	}
}

// PrevSibling moves to the previous sibling.
func (m *Model) PrevSibling() {
	if m.cursor >= len(m.rows) {
		return
	}
	r := m.rows[m.cursor]
	for j := m.cursor - 1; j >= 0; j-- {
		if m.rows[j].Depth < r.Depth {
			return
		}
		if m.rows[j].Depth == r.Depth {
			m.setCursor(j)
			return
		}
	}
}

// NextMatch and PrevMatch move between filter matches, wrapping.
func (m *Model) NextMatch() { m.stepMatch(1) }

// PrevMatch moves to the previous filter match.
func (m *Model) PrevMatch() { m.stepMatch(-1) }

func (m *Model) stepMatch(dir int) {
	if len(m.matches) == 0 {
		return
	}
	// The match after (or before) the cursor in row order.
	pos := -1
	for k, id := range m.matches {
		if i := m.index[id]; (dir > 0 && i > m.cursor) || (dir < 0 && i < m.cursor) {
			if dir > 0 {
				pos = k
				break
			}
			pos = k
		}
	}
	if pos < 0 {
		pos = 0
		if dir < 0 {
			pos = len(m.matches) - 1
		}
	}
	m.Select(m.matches[pos])
}
