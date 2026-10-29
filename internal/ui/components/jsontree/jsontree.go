// Package jsontree shows a JSON document (an engine's inspect output) as a
// collapsible tree: arrow-key navigation, expand/collapse, search with
// next/previous match, and the jq-style path of the node under the cursor
// for copying. Object key order is preserved. Values whose key (or
// environment entry) looks secret are masked.
package jsontree

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/secret"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Limits for untrusted documents.
const (
	MaxNodes = 50_000
	maxDepth = 64
	maxQuery = 64
)

// Kind is a JSON value kind.
type Kind int

// Value kinds.
const (
	Object Kind = iota
	Array
	String
	Number
	Bool
	Null
)

// Node is one value in the tree.
type Node struct {
	Key      string // object key, "[i]" for array items, "" for the root
	Path     string // jq-style path, "." for the root
	Kind     Kind
	Value    string // display text of scalars (strings quoted, maybe masked)
	Children []*Node
	Parent   *Node
	Depth    int
	Expanded bool
}

func (n *Node) container() bool { return n.Kind == Object || n.Kind == Array }

// Model is the tree view state.
type Model struct {
	root      *Node
	rows      []*Node // visible nodes, in document order
	cursor    int
	offset    int
	height    int
	query     string
	matches   []*Node
	match     int
	typing    bool
	truncated bool
}

// ErrTooLarge reports a document with more than MaxNodes values; the tree
// holds the first MaxNodes.
var ErrTooLarge = errors.New("document truncated")

// Parse builds a tree from raw JSON. The root and its direct children are
// expanded. On ErrTooLarge the returned model is still usable.
func Parse(raw []byte) (*Model, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	p := &parser{dec: dec}
	root, err := p.value("", ".", nil, 0)
	if err != nil && !errors.Is(err, ErrTooLarge) {
		return nil, err
	}
	m := &Model{root: root, height: 10, match: -1, truncated: errors.Is(err, ErrTooLarge)}
	root.Expanded = true
	for _, c := range root.Children {
		if len(c.Children) <= 12 {
			c.Expanded = c.container()
		}
	}
	m.flatten()
	return m, err
}

type parser struct {
	dec   *json.Decoder
	nodes int
}

func (p *parser) value(key, path string, parent *Node, depth int) (*Node, error) {
	p.nodes++
	if p.nodes > MaxNodes {
		return nil, ErrTooLarge
	}
	if depth > maxDepth {
		return nil, errors.New("document nested too deeply")
	}
	tok, err := p.dec.Token()
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	n := &Node{Key: key, Path: path, Parent: parent, Depth: depth}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			n.Kind = Object
			for p.dec.More() {
				kt, err := p.dec.Token()
				if err != nil {
					return n, err
				}
				k, _ := kt.(string)
				c, err := p.value(textsafe.Clean(k), childPath(path, k), n, depth+1)
				if c != nil {
					n.Children = append(n.Children, c)
				}
				if err != nil {
					return n, err
				}
			}
		case '[':
			n.Kind = Array
			for i := 0; p.dec.More(); i++ {
				c, err := p.value(fmt.Sprintf("[%d]", i), fmt.Sprintf("%s[%d]", path, i), n, depth+1)
				if c != nil {
					n.Children = append(n.Children, c)
				}
				if err != nil {
					return n, err
				}
			}
		}
		if _, err := p.dec.Token(); err != nil { // closing delimiter
			return n, err
		}
	case string:
		n.Kind, n.Value = String, display(key, parent, t)
	case json.Number:
		n.Kind, n.Value = Number, t.String()
	case bool:
		n.Kind, n.Value = Bool, strconv.FormatBool(t)
	case nil:
		n.Kind, n.Value = Null, "null"
	}
	return n, nil
}

var ident = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func childPath(path, key string) string {
	base := strings.TrimSuffix(path, ".")
	if ident.MatchString(key) {
		return base + "." + key
	}
	if base == "" {
		base = "."
	}
	return base + "[" + strconv.Quote(key) + "]"
}

// display quotes a string value, masking secrets: values of secret-looking
// keys and secret "KEY=value" entries of arrays (Docker's Env).
func display(key string, parent *Node, v string) string {
	v = textsafe.Clean(v)
	switch {
	case parent != nil && parent.Kind == Object && secret.IsSecret(key) && v != "":
		return `"` + secret.Mask + `"`
	case parent != nil && parent.Kind == Array:
		if masked, ok := secret.Env(v); ok {
			return strconv.Quote(masked)
		}
	}
	return strconv.Quote(v)
}

// Truncated reports whether the document hit MaxNodes.
func (m *Model) Truncated() bool { return m.truncated }

// flatten rebuilds the visible rows, keeping the cursor on its node.
func (m *Model) flatten() {
	var cur *Node
	if m.cursor < len(m.rows) {
		cur = m.rows[m.cursor]
	}
	m.rows = m.rows[:0]
	var walk func(n *Node)
	walk = func(n *Node) {
		m.rows = append(m.rows, n)
		if n.Expanded {
			for _, c := range n.Children {
				walk(c)
			}
		}
	}
	walk(m.root)
	m.cursor = 0
	for i, n := range m.rows {
		if n == cur {
			m.cursor = i
			break
		}
	}
	m.scroll()
}

// Rows returns the visible nodes.
func (m *Model) Rows() []*Node { return m.rows }

// Cursor returns the node under the cursor.
func (m *Model) Cursor() *Node { return m.rows[m.cursor] }

// Move moves the cursor by delta rows.
func (m *Model) Move(delta int) {
	m.cursor = max(0, min(len(m.rows)-1, m.cursor+delta))
	m.scroll()
}

// Page moves by one screen.
func (m *Model) Page(down bool) {
	if down {
		m.Move(m.height - 1)
	} else {
		m.Move(-(m.height - 1))
	}
}

// Home jumps to the first row.
func (m *Model) Home() { m.Move(-len(m.rows)) }

// End jumps to the last row.
func (m *Model) End() { m.Move(len(m.rows)) }

// Expand opens the node under the cursor, or steps into its first child
// when it is already open.
func (m *Model) Expand() {
	n := m.Cursor()
	switch {
	case !n.container() || len(n.Children) == 0:
	case !n.Expanded:
		n.Expanded = true
		m.flatten()
	default:
		m.Move(1)
	}
}

// Collapse closes the node under the cursor, or moves to its parent.
func (m *Model) Collapse() {
	n := m.Cursor()
	if n.container() && n.Expanded && n.Parent != nil {
		n.Expanded = false
		m.flatten()
		return
	}
	if n.Parent != nil {
		m.focus(n.Parent)
	}
}

// Toggle opens or closes the node under the cursor.
func (m *Model) Toggle() {
	if n := m.Cursor(); n.container() && n.Parent != nil {
		n.Expanded = !n.Expanded
		m.flatten()
	}
}

// ExpandAll opens the node under the cursor and everything below it.
func (m *Model) ExpandAll() {
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.container() {
			n.Expanded = true
			for _, c := range n.Children {
				walk(c)
			}
		}
	}
	walk(m.Cursor())
	m.flatten()
}

// focus puts the cursor on n, expanding its ancestors.
func (m *Model) focus(n *Node) {
	for p := n.Parent; p != nil; p = p.Parent {
		p.Expanded = true
	}
	m.flatten()
	for i, r := range m.rows {
		if r == n {
			m.cursor = i
			break
		}
	}
	m.offset = max(0, m.cursor-m.height/2)
	m.scroll()
}

func (m *Model) scroll() {
	switch {
	case m.cursor < m.offset:
		m.offset = m.cursor
	case m.cursor >= m.offset+m.height:
		m.offset = m.cursor - m.height + 1
	}
	m.offset = max(0, min(m.offset, len(m.rows)-m.height))
}

// ----- search -----

// Typing reports whether the search prompt has the keyboard.
func (m *Model) Typing() bool { return m.typing }

// Query returns the search text.
func (m *Model) Query() string { return m.query }

// Matches returns the number of matching nodes and the current one (1-based, 0 = none).
func (m *Model) Matches() (int, int) { return len(m.matches), m.match + 1 }

// StartSearch opens the search prompt.
func (m *Model) StartSearch() { m.typing = true }

// Input edits the search prompt (see logview.Model.Input).
func (m *Model) Input(key, text string, confirm, cancel, clear bool) bool {
	switch {
	case confirm:
		m.typing = false
	case cancel:
		m.typing = false
		m.SetQuery("")
	case clear:
		m.SetQuery("")
	case key == "tab" || key == "shift+tab" || key == "ctrl+c":
		return false
	case key == "backspace":
		if m.query != "" {
			_, n := utf8.DecodeLastRuneInString(m.query)
			m.SetQuery(m.query[:len(m.query)-n])
		}
	case text != "" && utf8.RuneCountInString(m.query) < maxQuery && printable(text):
		m.SetQuery(m.query + text)
	}
	return true
}

func printable(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// SetQuery searches keys and scalar values (case-insensitive) and moves to
// the first match at or after the cursor.
func (m *Model) SetQuery(q string) {
	m.query, m.matches, m.match = q, nil, -1
	if q == "" {
		return
	}
	lq := strings.ToLower(q)
	var walk func(n *Node)
	walk = func(n *Node) {
		if strings.Contains(strings.ToLower(n.Key), lq) || strings.Contains(strings.ToLower(n.Value), lq) {
			m.matches = append(m.matches, n)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(m.root)
	if len(m.matches) > 0 {
		m.match = 0
		m.focus(m.matches[0])
	}
}

// NextMatch moves to the next (or previous) match, wrapping around.
func (m *Model) NextMatch(forward bool) bool {
	if len(m.matches) == 0 {
		return false
	}
	if forward {
		m.match = (m.match + 1) % len(m.matches)
	} else {
		m.match = (m.match - 1 + len(m.matches)) % len(m.matches)
	}
	m.focus(m.matches[m.match])
	return true
}

// ----- rendering -----

// Render draws the visible rows in width×height cells.
func (m *Model) Render(s *theme.Styles, width, height int) string {
	m.height = max(1, height)
	m.scroll()
	var lines []string
	for i := m.offset; i < len(m.rows) && len(lines) < height; i++ {
		lines = append(lines, m.renderRow(s, m.rows[i], width, i == m.cursor))
	}
	if m.truncated && len(lines) < height {
		lines = append(lines, s.LevelText(theme.LevelWarn).Render(fmt.Sprintf("… document truncated at %d values", MaxNodes)))
	}
	return layout.Fit(strings.Join(lines, "\n"), width, height)
}

func (m *Model) renderRow(s *theme.Styles, n *Node, width int, cursor bool) string {
	indent := strings.Repeat("  ", n.Depth)
	marker := "  "
	if n.container() && len(n.Children) > 0 {
		marker = "▸ "
		if n.Expanded {
			marker = "▾ "
		}
	}
	key := n.Key
	if n.Parent == nil {
		key = "(root)"
	}
	var val string
	switch n.Kind {
	case Object:
		val = s.Muted.Render(fmt.Sprintf("{…} %d %s", len(n.Children), plural(len(n.Children), "key")))
		switch {
		case len(n.Children) == 0:
			val = s.Muted.Render("{}")
		case n.Expanded:
			val = s.Muted.Render("{")
		}
	case Array:
		val = s.Muted.Render(fmt.Sprintf("[…] %d %s", len(n.Children), plural(len(n.Children), "item")))
		switch {
		case len(n.Children) == 0:
			val = s.Muted.Render("[]")
		case n.Expanded:
			val = s.Muted.Render("[")
		}
	case String:
		val = m.mark(s, s.JSONString, n.Value)
	case Number:
		val = m.mark(s, s.JSONNumber, n.Value)
	default:
		val = m.mark(s, s.JSONOther, n.Value)
	}
	keyStyle := s.JSONKey
	if n.Kind != Object && n.Parent != nil && n.Parent.Kind == Array {
		keyStyle = s.Muted
	}
	line := s.Muted.Render(indent+marker) + m.mark(s, keyStyle, key) + s.Muted.Render(": ") + val
	line = ansi.Truncate(line, width, "…")
	if cursor {
		plain := ansi.Strip(line)
		return s.CursorLine.Render(plain + strings.Repeat(" ", max(0, width-ansi.StringWidth(plain))))
	}
	return line
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// mark styles text, highlighting the search query.
func (m *Model) mark(s *theme.Styles, st interface{ Render(...string) string }, text string) string {
	if m.query == "" {
		return st.Render(text)
	}
	i := strings.Index(strings.ToLower(text), strings.ToLower(m.query))
	if i < 0 || len(strings.ToLower(text)) != len(text) {
		return st.Render(text)
	}
	j := i + len(m.query)
	return st.Render(text[:i]) + s.Match.Render(text[i:j]) + st.Render(text[j:])
}
