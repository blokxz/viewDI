// Package palette is the command palette (ctrl+k): one prompt that finds
// machines, their actions, library entries, views and settings. It is fed
// by the app with items built from store snapshots and the catalog; it never
// reads a daemon. Every result shows its own key, so the palette also
// teaches the keys.
package palette

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Item is one thing the palette can do.
type Item struct {
	ID     string // returned in SelectedMsg
	Label  string // "Restart web-api"
	Group  string // "Machines", "Actions", "Views"…, shown muted
	Key    string // the direct key, "R"; empty when there is none
	Words  string // extra words that match but are not shown
	Search bool   // listed only once the query matches (per-machine actions)
}

// SelectedMsg reports the chosen item.
type SelectedMsg struct{ ID string }

// MaxResults caps the list; the query narrows it.
const MaxResults = 50

// Match ranks items for query. An empty query lists the items that are not
// Search-only, recent ones first. recent holds item IDs, most recent first;
// a recent item gets a bonus that never beats a clearly better match.
func Match(query string, items []Item, recent []string) []Item {
	q := []rune(strings.ToLower(strings.TrimSpace(query)))
	type scored struct {
		item  Item
		score int
		order int
	}
	var out []scored
	for i, it := range items {
		bonus := 0
		if r := slices.Index(recent, it.ID); r >= 0 {
			bonus = max(1, 12-r)
		}
		if len(q) == 0 {
			if !it.Search || bonus > 0 { // a verb you ran lately is worth listing
				out = append(out, scored{it, bonus, i})
			}
			continue
		}
		s, ok := score(q, strings.ToLower(it.Label), strings.ToLower(it.Group+" "+it.Words))
		if !ok {
			continue
		}
		out = append(out, scored{it, s + bonus, i})
	}
	slices.SortStableFunc(out, func(a, b scored) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return a.order - b.order
	})
	res := make([]Item, 0, min(len(out), MaxResults))
	for _, s := range out[:min(len(out), MaxResults)] {
		res = append(res, s.item)
	}
	return res
}

// score rates how well q matches label (and, weaker, extra). Every rune of q
// must appear in order; consecutive runs, word starts and a prefix score
// more. ok is false when q is not a subsequence of either.
func score(q []rune, label, extra string) (int, bool) {
	if s, ok := subseq(q, []rune(label)); ok {
		return 100 + s, true
	}
	if s, ok := subseq(q, []rune(label+" "+extra)); ok {
		return s, true
	}
	return 0, false
}

func subseq(q, s []rune) (int, bool) {
	score, qi, run := 0, 0, 0
	for i, r := range s {
		if qi >= len(q) {
			break
		}
		if r != q[qi] {
			run = 0
			continue
		}
		qi++
		run++
		score += 1 + run*2
		if i == 0 {
			score += 8
		} else if !unicode.IsLetter(s[i-1]) && !unicode.IsDigit(s[i-1]) {
			score += 5 // the start of a word
		}
	}
	if qi < len(q) {
		return 0, false
	}
	// A shorter text with the same match is the better answer.
	return score*4 - len(s)/8, true
}

// Model is the palette modal.
type Model struct {
	keys   *keys.Map
	items  []Item
	recent []string
	query  string
	res    []Item
	cursor int
}

// New opens the palette over items.
func New(km *keys.Map, items []Item, recent []string) *Model {
	m := &Model{keys: km, items: items, recent: recent}
	m.res = Match("", items, recent)
	return m
}

// Query returns the text typed so far (tests).
func (m *Model) Query() string { return m.query }

// Results returns the items shown (tests).
func (m *Model) Results() []Item { return m.res }

// Scope implements modal.Modal.
func (m *Model) Scope() keys.Scope { return keys.Palette }

func (m *Model) refresh() {
	m.res = Match(m.query, m.items, m.recent)
	m.cursor = 0
}

// Update implements modal.Modal.
func (m *Model) Update(msg tea.Msg) (modal.Modal, tea.Cmd) {
	if p, ok := msg.(tea.PasteMsg); ok {
		m.query += sanitize(p.Content)
		m.refresh()
		return m, nil
	}
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch m.keys.Action(kp, keys.Palette) {
	case keys.ActUp:
		if n := len(m.res); n > 0 {
			m.cursor = (m.cursor - 1 + n) % n
		}
		return m, nil
	case keys.ActDown:
		if n := len(m.res); n > 0 {
			m.cursor = (m.cursor + 1) % n
		}
		return m, nil
	case keys.ActConfirm:
		if len(m.res) == 0 {
			return m, nil
		}
		sel := SelectedMsg{ID: m.res[m.cursor].ID}
		return m, tea.Sequence(modal.Close, func() tea.Msg { return sel })
	case keys.ActCancel:
		return m, modal.Close
	case "erase":
		if m.query != "" {
			_, n := utf8.DecodeLastRuneInString(m.query)
			m.query = m.query[:len(m.query)-n]
			m.refresh()
		}
		return m, nil
	case "clear":
		m.query = ""
		m.refresh()
		return m, nil
	}
	if kp.Text != "" {
		m.query += sanitize(kp.Text)
		m.refresh()
	}
	return m, nil
}

// sanitize keeps printable text: a paste cannot smuggle controls in.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
}

// View implements modal.Modal.
func (m *Model) View(s *theme.Styles, width, height int) string {
	inner := min(max(40, width-12), 90)
	rows := max(3, min(len(m.res), height-12))
	start := max(0, min(m.cursor-rows/2, len(m.res)-rows))
	prompt := s.Accent.Render("› ") + s.Text.Render(m.query) + s.Muted.Render("▏")
	lines := []string{prompt, ""}
	for i := start; i < min(len(m.res), start+rows); i++ {
		it := m.res[i]
		keyW := 10
		labelW := inner - keyW - 14
		label := ansi.Truncate(it.Label, labelW, "…")
		label += strings.Repeat(" ", max(0, labelW-ansi.StringWidth(label)))
		grp := ansi.Truncate(it.Group, 12, "…")
		grp += strings.Repeat(" ", max(0, 12-ansi.StringWidth(grp)))
		keyText := ansi.Truncate(it.Key, keyW, "…")
		keyText = strings.Repeat(" ", max(0, keyW-ansi.StringWidth(keyText))) + keyText
		if i == m.cursor {
			lines = append(lines, s.Selected.Render("›"+label+" "+grp+keyText))
			continue
		}
		lines = append(lines, " "+s.Text.Render(label)+" "+s.Muted.Render(grp)+s.KeyHint.Render(keyText))
	}
	if len(m.res) == 0 {
		lines = append(lines, s.Muted.Render("nothing matches “"+m.query+"”"))
	}
	hint := s.Muted.Render("type to search · ↑/↓ move · enter run · esc close")
	return modal.Frame(s, "Command palette", strings.Join(lines, "\n")+"\n\n"+hint, width)
}
