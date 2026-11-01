package tree

import (
	"math"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// laneStep is the distance between lane columns; a lane area of n columns
// is laneStep*n+1 cells wide (column 0 is where the stubs start).
const laneStep = 3

// Traffic tiers set the dot's speed (half-cells per frame) from the summed
// network rates of a link's two ends.
const (
	tierLow  = 16 << 10 // bytes/s
	tierHigh = 1 << 20
)

var tierSpeed = [3]int64{1, 2, 4}

// lane is a declared link drawn beside the tree: a stub from each end's
// row to the lane's column, joined by a vertical run.
type lane struct {
	y1, y2  int  // visible row indexes, y1 < y2
	forward bool // the dot runs from y1 to y2 (A is the upper end)
	col     int
	active  bool // both ends running
	hot     bool // an end is under the cursor
	tier    int
	phase   int64
}

// x is the lane's column in the lane area.
func (l lane) x() int { return laneStep * (l.col + 1) }

// path is the cells the dot runs through, from A to B.
func (l lane) path() [][2]int {
	x := l.x()
	p := make([][2]int, 0, 2*(x+1)+l.y2-l.y1)
	for c := 0; c <= x; c++ {
		p = append(p, [2]int{l.y1, c})
	}
	for y := l.y1 + 1; y < l.y2; y++ {
		p = append(p, [2]int{y, x})
	}
	for c := x; c >= 0; c-- {
		p = append(p, [2]int{l.y2, c})
	}
	if !l.forward {
		slices.Reverse(p)
	}
	return p
}

// lanes lays out every link over the visible rows (tests).
func (v *View) lanes() ([]lane, int) { return v.lanesIn(0, math.MaxInt) }

// lanesIn lays out the links of the open labs over the visible rows. Links
// of a lab get greedy columns: two links whose row spans overlap never
// share one. Labs do not interleave (a lab's members are contiguous), so
// each lab starts again from column 0. Only lanes crossing the rows
// [off, off+n) get their state (running ends, traffic, cursor): the others
// are not drawn. It returns the lanes and the number of columns used.
func (v *View) lanesIn(off, n int) ([]lane, int) {
	if v.grouping != ByLab || v.snap == nil {
		return nil, 0
	}
	type end struct {
		row int
		key engine.Key
	}
	rows := v.t().Rows()
	pos := map[int]map[config.LabMember]end{}
	for i, r := range rows {
		it := data(r.Node)
		if (it.kind == kInstance || it.kind == kMissing) && it.lab != 0 {
			if pos[it.lab] == nil {
				pos[it.lab] = map[config.LabMember]end{}
			}
			pos[it.lab][it.member] = end{row: i, key: it.key}
		}
	}
	hotLab, hotMember, hot := v.cursorMember()
	var out []lane
	ncols := 0
	for _, l := range v.labs.Labs {
		p := pos[l.ID]
		if len(p) < 2 {
			continue
		}
		var ls []lane
		for k, link := range l.Links {
			a, okA := p[link.A]
			b, okB := p[link.B]
			if !okA || !okB {
				continue
			}
			ln := lane{y1: a.row, y2: b.row, forward: true, phase: int64(k*5 + l.ID*3)}
			if ln.y1 > ln.y2 {
				ln.y1, ln.y2, ln.forward = ln.y2, ln.y1, false
			}
			if ln.y2 >= off && ln.y1-off < n {
				ln.hot = hot && hotLab == l.ID && link.Has(hotMember)
				ln.active, ln.tier = v.linkState(a.key, b.key)
			}
			ls = append(ls, ln)
		}
		slices.SortStableFunc(ls, func(p, q lane) int {
			if p.y1 != q.y1 {
				return p.y1 - q.y1
			}
			return p.y2 - q.y2
		})
		var cols [][]lane
		for i := range ls {
			c := 0
			for c < len(cols) && slices.ContainsFunc(cols[c], func(o lane) bool { return ls[i].y1 <= o.y2 && ls[i].y2 >= o.y1 }) {
				c++
			}
			if c == len(cols) {
				cols = append(cols, nil)
			}
			ls[i].col = c
			cols[c] = append(cols[c], ls[i])
		}
		ncols = max(ncols, len(cols))
		out = append(out, ls...)
	}
	return out, ncols
}

// cursorMember is the lab member under the cursor (or owning the leaf
// under it), with its lab.
func (v *View) cursorMember() (int, config.LabMember, bool) {
	n, ok := v.t().Current()
	if !ok {
		return 0, config.LabMember{}, false
	}
	it := data(n)
	if it.kind == kPort || it.kind == kMount || it.kind == kAlso {
		if p := v.t().Parent(n.ID); p != nil {
			it = data(p)
		}
	}
	if (it.kind == kInstance || it.kind == kMissing) && it.lab != 0 {
		return it.lab, it.member, true
	}
	return 0, config.LabMember{}, false
}

// linkState reports whether both ends run, and the traffic tier.
func (v *View) linkState(a, b engine.Key) (bool, int) {
	rate := 0.0
	for _, k := range []engine.Key{a, b} {
		in, ok := v.snap.Lookup(k)
		if k == "" || !ok || in.State != engine.StateRunning {
			return false, 0
		}
		if p, ok := v.snap.Point(k); ok {
			rate += p.NetRx + p.NetTx
		}
	}
	switch {
	case rate >= tierHigh:
		return true, 2
	case rate >= tierLow:
		return true, 1
	}
	return true, 0
}

// Box-drawing pieces by the directions a cell connects to.
const (
	dUp = 1 << iota
	dDown
	dLeft
	dRight
)

var box = map[int]string{
	dLeft | dRight: "─", dLeft: "─", dRight: "─", dUp | dDown: "│",
	dLeft | dDown: "╮", dLeft | dUp: "╯", dRight | dDown: "╭", dRight | dUp: "╰",
	dLeft | dRight | dDown: "┬", dLeft | dRight | dUp: "┴",
	dUp | dDown | dLeft: "┤", dUp | dDown | dRight: "├", dLeft | dRight | dUp | dDown: "┼",
}

// laneLines draws the lane area for the screen rows [off, off+n): one
// string of width cells per row.
func (v *View) laneLines(s *theme.Styles, ls []lane, width, off, n int) []string {
	type cell struct {
		dirs, rank int
		dot        bool
	}
	grid := make([][]cell, n)
	for i := range grid {
		grid[i] = make([]cell, width)
	}
	mark := func(y, x, dirs, rank int) {
		if y < off || y >= off+n || x < 0 || x >= width {
			return
		}
		c := &grid[y-off][x]
		c.dirs |= dirs
		c.rank = max(c.rank, rank)
	}
	for _, l := range ls {
		rank := 1
		switch {
		case l.active && l.hot:
			rank = 4
		case l.active:
			rank = 3
		case l.hot:
			rank = 2
		}
		x := l.x()
		for _, y := range []int{l.y1, l.y2} {
			for c := 0; c < x; c++ {
				mark(y, c, dLeft|dRight, rank)
			}
		}
		mark(l.y1, x, dLeft|dDown, rank)
		mark(l.y2, x, dLeft|dUp, rank)
		for y := l.y1 + 1; y < l.y2; y++ {
			mark(y, x, dUp|dDown, rank)
		}
		if l.active && v.opts.Animation > 0 {
			p := l.path()
			i := (v.frame*tierSpeed[l.tier]/2 + l.phase) % int64(len(p))
			if y, x := p[i][0], p[i][1]; y >= off && y < off+n && x < width {
				grid[y-off][x].dot = true
			}
		}
	}
	styles := [5]lipgloss.Style{s.LinkIdle, s.LinkIdle, s.LinkIdleHot, s.LinkActive, s.LinkHot}
	dot := "●"
	if v.opts.ASCII {
		dot = "o"
	}
	out := make([]string, n)
	for i, row := range grid {
		// One Render per run of cells in the same style, not per cell.
		var b, run strings.Builder
		style := -1 // -1: none (blanks), 0: dot, 1-4: a rank
		flush := func() {
			switch {
			case run.Len() == 0:
			case style < 0:
				b.WriteString(run.String())
			case style == 0:
				b.WriteString(s.LinkDot.Render(run.String()))
			default:
				b.WriteString(styles[style].Render(run.String()))
			}
			run.Reset()
		}
		for _, c := range row {
			st, glyph := -1, " "
			switch {
			case c.dot:
				st, glyph = 0, dot
			case c.dirs != 0:
				st, glyph = max(c.rank, 1), box[c.dirs]
			}
			if st != style {
				flush()
				style = st
			}
			run.WriteString(glyph)
		}
		flush()
		out[i] = b.String()
	}
	return out
}

// laneWidth is the lane area's width for ncols columns, capped to a third
// of the panel; maxCols is how many columns fit.
func laneWidth(ncols, innerW int) (width, maxCols int) {
	maxCols = (innerW/3 - 1) / laneStep
	if ncols == 0 || maxCols < 1 {
		return 0, 0
	}
	ncols = min(ncols, maxCols)
	return laneStep*ncols + 1, maxCols
}

// SetVisible tells the view whether it is on screen; the app calls it
// after every update. It starts the link animation when it should run.
func (v *View) SetVisible(visible bool) tea.Cmd {
	v.visible = visible
	return v.animate()
}

// Animating reports whether the link animation's tick is running.
func (v *View) Animating() bool { return v.ticking }

// animating reports whether the animation should run: enabled, visible, in
// the lab grouping, with an active lane on screen.
func (v *View) animating() bool {
	if v.opts.Animation <= 0 || !v.visible || v.grouping != ByLab {
		return false
	}
	off := v.t().Window(v.page)
	ls, _ := v.lanesIn(off, v.page)
	for _, l := range ls {
		if l.active && l.y2 >= off && l.y1 < off+v.page {
			return true
		}
	}
	return false
}

// animate starts a tick chain when the animation should run and none is,
// and forgets the chain when it should stop (its next frame is ignored).
func (v *View) animate() tea.Cmd {
	if v.ticking && v.visible && v.grouping == ByLab {
		return nil // the next frame checks whether to go on: cheap per message
	}
	if !v.animating() {
		v.ticking = false
		return nil
	}
	v.ticking = true
	v.gen++
	return v.tick()
}

func (v *View) tick() tea.Cmd {
	gen := v.gen
	return tea.Tick(v.opts.Animation, func(t time.Time) tea.Msg { return frameMsg{gen: gen, at: t} })
}

// onFrame advances the dots: the frame number comes from the tick's time,
// so tests drive it with fixed times.
func (v *View) onFrame(msg frameMsg) tea.Cmd {
	if msg.gen != v.gen || !v.ticking {
		return nil // a stale chain
	}
	v.frame = msg.at.UnixMilli() / v.opts.Animation.Milliseconds()
	if !v.animating() {
		v.ticking = false
		return nil
	}
	return v.tick()
}
