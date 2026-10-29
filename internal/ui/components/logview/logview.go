// Package logview is the Logs tab's viewer: a bounded ring of log lines
// with follow/pause, a regex filter with highlighted matches and match
// navigation, a stdout/stderr selector, wrapping and timestamps.
//
// Lines arrive already sanitised (internal/textsafe). The visible-line
// index is maintained incrementally: appending tests only the new lines,
// so a flood of lines costs O(new) per batch, and rendering touches only
// the lines on screen.
package logview

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// DefaultCapacity is the number of lines kept (config [logs] buffer_lines).
const DefaultCapacity = 10_000

// maxFilter caps the filter prompt.
const maxFilter = 128

// Line is one stored line; Seq numbers lines in arrival order.
type Line struct {
	Seq uint64
	engine.LogLine
}

// Model is the viewer state.
type Model struct {
	buf   []Line // ring
	start int    // index of the oldest line in buf
	n     int    // lines held
	next  uint64 // Seq of the next line

	dropped int // lines the stream dropped before they reached the viewer

	paused, wrap, stamps bool
	source               engine.LogSource // 0 = every source

	filter    string
	re        *regexp.Regexp
	filterErr string
	typing    bool

	index    []uint64 // Seqs of the lines that pass the source and filter
	follow   bool     // the view sticks to the newest line
	bottom   int      // position in index of the last visible line, when not following
	newCount int      // lines added while paused or scrolled up
	match    int      // current match in index (-1 = none)
	pendingG bool

	height int // rows of the last render, for paging
}

// New returns an empty viewer that keeps capacity lines.
func New(capacity int) *Model {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Model{buf: make([]Line, capacity), follow: true, stamps: true, match: -1, height: 10}
}

// Len is the number of lines held.
func (m *Model) Len() int { return m.n }

// Visible is the number of lines passing the source and filter.
func (m *Model) Visible() int { return len(m.index) }

// Dropped is the number of lines lost upstream.
func (m *Model) Dropped() int { return m.dropped }

// Paused reports whether new lines are held back from the view.
func (m *Model) Paused() bool { return m.paused }

// Following reports whether the view sticks to the newest line.
func (m *Model) Following() bool { return m.follow }

// NewCount is the number of lines that arrived while paused or scrolled up.
func (m *Model) NewCount() int { return m.newCount }

// Filter returns the filter text and its compile error, if any.
func (m *Model) Filter() (string, string) { return m.filter, m.filterErr }

// Source returns the source selection (0 = all).
func (m *Model) Source() engine.LogSource { return m.source }

// Wrap reports whether long lines wrap.
func (m *Model) Wrap() bool { return m.wrap }

// Timestamps reports whether timestamps are shown.
func (m *Model) Timestamps() bool { return m.stamps }

// Typing reports whether the filter prompt has the keyboard.
func (m *Model) Typing() bool { return m.typing }

// line returns the stored line with seq, if still held.
func (m *Model) line(seq uint64) (Line, bool) {
	first := m.next - uint64(m.n)
	if seq < first || seq >= m.next {
		return Line{}, false
	}
	return m.buf[(m.start+int(seq-first))%len(m.buf)], true
}

// Append adds lines (oldest first) and counts dropped ones.
func (m *Model) Append(lines []engine.LogLine, dropped int) {
	m.dropped += dropped
	if len(lines) > len(m.buf) { // older lines would be evicted at once
		m.next += uint64(len(lines) - len(m.buf))
		lines = lines[len(lines)-len(m.buf):]
	}
	added := 0
	for _, l := range lines {
		ln := Line{Seq: m.next, LogLine: l}
		m.next++
		if m.n < len(m.buf) {
			m.buf[(m.start+m.n)%len(m.buf)] = ln
			m.n++
		} else {
			m.buf[m.start] = ln
			m.start = (m.start + 1) % len(m.buf)
		}
		if m.passes(ln) {
			m.index = append(m.index, ln.Seq)
			added++
		}
	}
	m.evict()
	if !m.follow {
		m.newCount += added
	}
}

// evict drops index entries of lines that left the ring.
func (m *Model) evict() {
	first := m.next - uint64(m.n)
	k := 0
	for k < len(m.index) && m.index[k] < first {
		k++
	}
	if k == 0 {
		return
	}
	m.index = append(m.index[:0], m.index[k:]...)
	m.bottom = max(0, m.bottom-k)
	if m.match >= 0 {
		m.match -= k
		if m.match < 0 {
			m.match = -1
		}
	}
}

func (m *Model) passes(l Line) bool {
	if m.source != 0 && l.Source != m.source {
		return false
	}
	return m.re == nil || m.re.MatchString(l.Text)
}

// rebuild recomputes the index after a source or filter change, keeping the
// view near the line it showed.
func (m *Model) rebuild() {
	anchor := uint64(0)
	if !m.follow && m.bottom < len(m.index) {
		anchor = m.index[m.bottom]
	}
	m.index = m.index[:0]
	first := m.next - uint64(m.n)
	for i := range m.n {
		if l := m.buf[(m.start+i)%len(m.buf)]; m.passes(l) {
			m.index = append(m.index, first+uint64(i))
		}
	}
	m.match = -1
	if !m.follow {
		m.bottom = m.position(anchor)
	}
}

// position is the index position of the last line at or before seq.
func (m *Model) position(seq uint64) int {
	lo, hi := 0, len(m.index)
	for lo < hi {
		mid := (lo + hi) / 2
		if m.index[mid] <= seq {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return max(0, lo-1)
}

// SetFilter compiles pattern (smart case: case-insensitive unless it has
// an upper-case letter). An invalid pattern shows its error and filters
// nothing.
func (m *Model) SetFilter(pattern string) {
	m.filter, m.filterErr, m.re = pattern, "", nil
	if pattern != "" {
		expr := pattern
		if !strings.ContainsFunc(pattern, unicode.IsUpper) {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			m.filterErr = strings.TrimPrefix(err.Error(), "error parsing regexp: ")
		} else {
			m.re = re
		}
	}
	m.rebuild()
}

// StartFilter opens the filter prompt.
func (m *Model) StartFilter() { m.typing = true }

// Input edits the filter prompt: text, backspace, ctrl+u; enter keeps the
// filter and esc clears it. It reports false for keys that keep their
// meaning (tab, ctrl+c).
func (m *Model) Input(key, text string, confirm, cancel, clear bool) bool {
	switch {
	case confirm:
		m.typing = false
	case cancel:
		m.typing = false
		m.SetFilter("")
	case clear:
		m.SetFilter("")
	case key == "tab" || key == "shift+tab" || key == "ctrl+c":
		return false
	case key == "backspace":
		if m.filter != "" {
			_, n := utf8.DecodeLastRuneInString(m.filter)
			m.SetFilter(m.filter[:len(m.filter)-n])
		}
	case text != "" && utf8.RuneCountInString(m.filter) < maxFilter && !strings.ContainsFunc(text, unicode.IsControl):
		m.SetFilter(m.filter + text)
	}
	return true
}

// TogglePause pauses or resumes following.
func (m *Model) TogglePause() {
	m.paused = !m.paused
	if m.paused {
		m.freeze()
		return
	}
	m.End()
}

// freeze stops following at the current bottom line.
func (m *Model) freeze() {
	if m.follow {
		m.follow = false
		m.bottom = max(0, len(m.index)-1)
	}
}

// ToggleWrap switches line wrapping.
func (m *Model) ToggleWrap() { m.wrap = !m.wrap }

// ToggleTimestamps shows or hides timestamps.
func (m *Model) ToggleTimestamps() { m.stamps = !m.stamps }

// CycleSource cycles all → stdout → stderr → system → all.
func (m *Model) CycleSource() {
	m.source = (m.source + 1) % (engine.System + 1)
	m.rebuild()
}

// Scroll moves the view by delta lines (negative = up, towards older).
func (m *Model) Scroll(delta int) {
	if len(m.index) == 0 {
		return
	}
	m.freeze()
	m.bottom = max(0, min(len(m.index)-1, m.bottom+delta))
	if m.bottom >= len(m.index)-1 && !m.paused {
		m.End()
	}
}

// Page scrolls by one screen.
func (m *Model) Page(down bool) {
	step := max(1, m.height-1)
	if !down {
		step = -step
	}
	m.Scroll(step)
}

// Top shows the oldest lines.
func (m *Model) Top() {
	if len(m.index) == 0 {
		return
	}
	m.freeze()
	m.bottom = min(len(m.index)-1, max(0, m.height-1))
}

// End follows the newest line again and resumes a pause.
func (m *Model) End() {
	m.paused, m.follow, m.newCount = false, true, 0
}

// G handles the two-key "gg" sequence: it reports true when the second g
// arrived (the caller jumps to the top).
func (m *Model) G() bool {
	if m.pendingG {
		m.pendingG = false
		return true
	}
	m.pendingG = true
	return false
}

// ResetG cancels a pending "g".
func (m *Model) ResetG() { m.pendingG = false }

// NextMatch moves to the next (or previous) line matching the filter and
// stops following there.
func (m *Model) NextMatch(forward bool) bool {
	if m.re == nil || len(m.index) == 0 {
		return false
	}
	cur := m.match
	if cur < 0 {
		cur = len(m.index)
		if !m.follow {
			cur = m.bottom + 1
		}
	}
	if forward {
		cur++
	} else {
		cur--
	}
	m.match = max(0, min(len(m.index)-1, cur))
	m.freeze()
	if m.match > m.bottom || m.match <= m.bottom-m.height {
		m.bottom = min(len(m.index)-1, m.match+m.height/2)
	}
	return true
}

// Status is the one-line summary shown above the lines.
func (m *Model) Status(s *theme.Styles) string {
	var parts []string
	switch {
	case m.paused:
		parts = append(parts, s.LevelText(theme.LevelWarn).Render(fmt.Sprintf("⏸ PAUSED (+%d new)", m.newCount)))
	case !m.follow:
		parts = append(parts, s.LevelText(theme.LevelInfo).Render(fmt.Sprintf("↑ SCROLLED (+%d new) G end", m.newCount)))
	default:
		parts = append(parts, s.LevelText(theme.LevelOK).Render("● LIVE"))
	}
	switch {
	case m.typing:
		parts = append(parts, s.Accent.Render("filter: /"+m.filter+"_"))
	case m.filter != "":
		parts = append(parts, s.Text.Render("filter: /"+m.filter))
	}
	if m.filterErr != "" {
		parts = append(parts, s.LevelText(theme.LevelErr).Render(m.filterErr))
	}
	if m.source != 0 {
		parts = append(parts, s.Text.Render("stream: "+m.source.String()))
	}
	count := fmt.Sprintf("%d lines", m.n)
	if len(m.index) != m.n {
		count = fmt.Sprintf("%d/%d lines", len(m.index), m.n)
	}
	parts = append(parts, s.Muted.Render(count))
	if m.dropped > 0 {
		parts = append(parts, s.LevelText(theme.LevelWarn).Render(fmt.Sprintf("dropped %d", m.dropped)))
	}
	return strings.Join(parts, s.Muted.Render(" · "))
}

const stampLayout = "15:04:05.000"

// Render draws the visible lines in width×height cells.
func (m *Model) Render(s *theme.Styles, width, height int) string {
	m.height = max(1, height)
	if width <= 0 || height <= 0 {
		return ""
	}
	if len(m.index) == 0 {
		msg := "No log lines yet."
		if m.n > 0 {
			msg = "No line matches."
		}
		return layout.Fit(s.Muted.Render(msg), width, height)
	}
	bottom := len(m.index) - 1
	if !m.follow {
		bottom = min(m.bottom, bottom)
	}
	var rows []string
	for i := bottom; i >= 0 && len(rows) < height; i-- {
		l, ok := m.line(m.index[i])
		if !ok {
			continue
		}
		r := m.renderLine(s, l, width, i == m.match)
		for j := len(r) - 1; j >= 0 && len(rows) < height; j-- {
			rows = append(rows, r[j])
		}
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return layout.Fit(strings.Join(rows, "\n"), width, height)
}

// renderLine returns the rows of one line (several when wrapping).
func (m *Model) renderLine(s *theme.Styles, l Line, width int, current bool) []string {
	var prefix strings.Builder
	prefixW := 0
	if m.stamps {
		stamp := strings.Repeat(" ", len(stampLayout))
		if !l.Time.IsZero() {
			stamp = l.Time.In(time.Local).Format(stampLayout)
		}
		prefix.WriteString(s.Muted.Render(stamp + " "))
		prefixW += len(stampLayout) + 1
	}
	src := s.LogStdout
	switch l.Source {
	case engine.Stderr:
		src = s.LogStderr
	case engine.System:
		src = s.LogSystem
	}
	prefix.WriteString(src.Render(fmt.Sprintf("%-3s", l.Source)) + " ")
	prefixW += 4
	textW := max(1, width-prefixW)
	text := l.Text
	if l.Truncated {
		text += " …[truncated]"
	}
	base := s.Text
	if current {
		base = s.CursorLine
	}
	if !m.wrap {
		return []string{prefix.String() + m.highlight(s, base, ansi.Truncate(text, textW, "…"))}
	}
	var out []string
	for i, chunk := range wrapRunes(text, textW) {
		p := prefix.String()
		if i > 0 {
			p = strings.Repeat(" ", prefixW)
		}
		out = append(out, p+m.highlight(s, base, chunk))
	}
	return out
}

// highlight styles the filter matches inside text.
func (m *Model) highlight(s *theme.Styles, base lipgloss.Style, text string) string {
	if m.re == nil {
		return base.Render(text)
	}
	locs := m.re.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return base.Render(text)
	}
	var b strings.Builder
	prev := 0
	for _, loc := range locs {
		if loc[0] == loc[1] {
			continue
		}
		b.WriteString(base.Render(text[prev:loc[0]]))
		b.WriteString(s.Match.Render(text[loc[0]:loc[1]]))
		prev = loc[1]
	}
	b.WriteString(base.Render(text[prev:]))
	return b.String()
}

// wrapRunes cuts text into chunks of at most width cells.
func wrapRunes(text string, width int) []string {
	if ansi.StringWidth(text) <= width {
		return []string{text}
	}
	var out []string
	var cur strings.Builder
	w := 0
	for _, r := range text {
		rw := max(ansi.StringWidth(string(r)), 0)
		if w+rw > width && w > 0 {
			out = append(out, cur.String())
			cur.Reset()
			w = 0
		}
		cur.WriteRune(r)
		w += rw
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
