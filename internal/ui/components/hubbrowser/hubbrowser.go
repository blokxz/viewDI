// Package hubbrowser is the Docker Hub browser (S in the Library): three tabs,
// a search, the most downloaded official images and the user's favourites.
// It never touches the network: it asks the app for results with messages
// (SearchMsg, LoadMsg) and shows what comes back (ResultsMsg). Favourites are
// plain image names the app keeps in state.json.
package hubbrowser

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Tab is one of the browser's lists.
type Tab int

// The tabs, in order.
const (
	TabSearch Tab = iota
	TabTop
	TabFavourites
	numTabs
)

var tabNames = [numTabs]string{"Search", "Most downloaded", "Favourites"}

// MaxFavourites caps the favourites the browser keeps.
const MaxFavourites = 100

// MaxQuery caps the characters of a query.
const MaxQuery = 100

// Item is one image in a list. Every text field is already safe to print.
type Item struct {
	Ref         string
	Description string
	Stars       uint64
	Pulls       uint64
	Official    bool
}

// Config says what the browser can do.
type Config struct {
	Favourites []string // image names, in the order they were added
	SearchNote string   // when not empty the Search tab says this instead of working
	TopNote    string   // the same for the Most downloaded tab
}

// SearchMsg asks the app to search for Query.
type SearchMsg struct{ Query string }

// LoadMsg asks the app for the items of a tab (the most downloaded).
type LoadMsg struct{ Tab Tab }

// SelectedMsg reports the chosen image.
type SelectedMsg struct{ Ref string }

// FavouritesMsg reports the new list of favourites for the app to save.
type FavouritesMsg struct{ Refs []string }

// ResultsMsg is the app's answer to SearchMsg or LoadMsg. Err, when set,
// replaces the list.
type ResultsMsg struct {
	Tab   Tab
	Query string // the query it answers (Search tab)
	Items []Item
	Err   string
}

type list struct {
	items   []Item
	cursor  int
	loading bool
	loaded  bool
	err     string
}

// Model is the browser modal.
type Model struct {
	keys     *keys.Map
	cfg      Config
	tab      Tab
	lists    [numTabs]list
	query    string
	searched string // the query the Search list answers
	favs     []string
	known    map[string]Item
	note     string
}

// New opens the browser on the Search tab.
func New(km *keys.Map, cfg Config) *Model {
	m := &Model{keys: km, cfg: cfg, known: map[string]Item{}}
	m.favs = append(m.favs, cfg.Favourites...)
	if len(m.favs) > MaxFavourites {
		m.favs = m.favs[:MaxFavourites]
	}
	return m
}

// Scope implements modal.Modal.
func (m *Model) Scope() keys.Scope { return keys.HubBrowse }

// Tab returns the active tab (tests).
func (m *Model) Tab() Tab { return m.tab }

// Query returns the text typed in the Search tab (tests).
func (m *Model) Query() string { return m.query }

// Favourites returns the favourites (tests).
func (m *Model) Favourites() []string { return append([]string(nil), m.favs...) }

// Items returns the items shown in the active tab (tests).
func (m *Model) Items() []Item { return m.shown() }

func (m *Model) isFav(ref string) bool {
	for _, f := range m.favs {
		if f == ref {
			return true
		}
	}
	return false
}

// shown is the list of the active tab.
func (m *Model) shown() []Item {
	if m.tab == TabFavourites {
		out := make([]Item, 0, len(m.favs))
		for _, f := range m.favs {
			if it, ok := m.known[f]; ok {
				out = append(out, it)
			} else {
				out = append(out, Item{Ref: f})
			}
		}
		return out
	}
	return m.lists[m.tab].items
}

func (m *Model) cursor() *int { return &m.lists[m.tab].cursor }

func (m *Model) clampCursor() {
	n := len(m.shown())
	c := m.cursor()
	*c = max(0, min(*c, n-1))
}

// enter switches to tab t and asks for its items the first time.
func (m *Model) enter(t Tab) tea.Cmd {
	m.tab = (t%numTabs + numTabs) % numTabs
	m.note = ""
	m.clampCursor()
	if m.tab == TabTop && m.cfg.TopNote == "" && !m.lists[TabTop].loaded && !m.lists[TabTop].loading {
		return m.load()
	}
	return nil
}

func (m *Model) load() tea.Cmd {
	m.lists[TabTop].loading, m.lists[TabTop].err = true, ""
	return func() tea.Msg { return LoadMsg{Tab: TabTop} }
}

// Update implements modal.Modal.
func (m *Model) Update(msg tea.Msg) (modal.Modal, tea.Cmd) {
	switch msg := msg.(type) {
	case ResultsMsg:
		m.results(msg)
		return m, nil
	case tea.PasteMsg:
		if m.tab == TabSearch {
			m.query = clip(m.query + sanitize(msg.Content))
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) results(r ResultsMsg) {
	if r.Tab < 0 || r.Tab >= numTabs || r.Tab == TabFavourites {
		return
	}
	l := &m.lists[r.Tab]
	if r.Tab == TabSearch && r.Query != strings.TrimSpace(m.searched) {
		return // an answer to an older search
	}
	l.loading, l.loaded, l.err = false, true, r.Err
	l.items, l.cursor = nil, 0
	if r.Err != "" {
		return
	}
	l.items = r.Items
	for _, it := range r.Items {
		m.known[it.Ref] = it
	}
}

func (m *Model) key(kp tea.KeyPressMsg) (modal.Modal, tea.Cmd) {
	switch m.keys.Action(kp, keys.HubBrowse) {
	case keys.ActUp:
		if n := len(m.shown()); n > 0 {
			c := m.cursor()
			*c = (*c - 1 + n) % n
		}
		return m, nil
	case keys.ActDown:
		if n := len(m.shown()); n > 0 {
			c := m.cursor()
			*c = (*c + 1) % n
		}
		return m, nil
	case "tab":
		return m, m.enter(m.tab + 1)
	case "backtab":
		return m, m.enter(m.tab - 1)
	case keys.ActConfirm:
		return m, m.confirm()
	case "favourite":
		m.toggleFavourite()
		return m, m.favouritesChanged()
	case "reload":
		switch m.tab {
		case TabTop:
			if m.cfg.TopNote == "" && !m.lists[TabTop].loading {
				return m, m.load()
			}
		case TabSearch:
			return m, m.search()
		}
		return m, nil
	case keys.ActCancel:
		return m, modal.Close
	case "erase":
		if m.tab == TabSearch && m.query != "" {
			_, n := utf8.DecodeLastRuneInString(m.query)
			m.query = m.query[:len(m.query)-n]
		}
		return m, nil
	case "clear":
		if m.tab == TabSearch {
			m.query = ""
		}
		return m, nil
	}
	if kp.Text != "" && m.tab == TabSearch {
		m.query = clip(m.query + sanitize(kp.Text))
	}
	return m, nil
}

// confirm is enter: on the Search tab a changed query searches, anything
// else chooses the highlighted image.
func (m *Model) confirm() tea.Cmd {
	if m.tab == TabSearch && strings.TrimSpace(m.query) != strings.TrimSpace(m.searched) {
		return m.search()
	}
	if m.lists[m.tab].loading {
		return nil
	}
	items := m.shown()
	if len(items) == 0 {
		if m.tab == TabSearch {
			return m.search()
		}
		return nil
	}
	sel := SelectedMsg{Ref: items[*m.cursor()].Ref}
	return tea.Sequence(modal.Close, func() tea.Msg { return sel })
}

func (m *Model) search() tea.Cmd {
	q := strings.TrimSpace(m.query)
	if q == "" || m.cfg.SearchNote != "" {
		return nil
	}
	m.searched = q
	m.lists[TabSearch] = list{loading: true}
	return func() tea.Msg { return SearchMsg{Query: q} }
}

func (m *Model) toggleFavourite() {
	items := m.shown()
	if len(items) == 0 {
		return
	}
	ref := items[*m.cursor()].Ref
	for i, f := range m.favs {
		if f == ref {
			m.favs = append(m.favs[:i], m.favs[i+1:]...)
			m.note = ref + " removed from the favourites"
			m.clampCursor()
			return
		}
	}
	if len(m.favs) >= MaxFavourites {
		m.note = fmt.Sprintf("at most %d favourites", MaxFavourites)
		return
	}
	m.favs = append(m.favs, ref)
	m.note = ref + " is a favourite"
}

func (m *Model) favouritesChanged() tea.Cmd {
	refs := append([]string(nil), m.favs...)
	return func() tea.Msg { return FavouritesMsg{Refs: refs} }
}

func clip(s string) string {
	r := []rune(s)
	if len(r) > MaxQuery {
		return string(r[:MaxQuery])
	}
	return s
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

// Compact writes n as 1.2K, 3.4M, 5.6B.
func Compact(n uint64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// meta is what is known about an image besides its name: "official · ★ 21.4K · ↓ 13.5B".
func meta(it Item) string {
	var parts []string
	if it.Official {
		parts = append(parts, "official")
	}
	if it.Stars > 0 {
		parts = append(parts, "★ "+Compact(it.Stars))
	}
	if it.Pulls > 0 {
		parts = append(parts, "↓ "+Compact(it.Pulls))
	}
	return strings.Join(parts, " · ")
}

// View implements modal.Modal.
func (m *Model) View(s *theme.Styles, width, height int) string {
	inner := min(max(50, width-12), 110)
	var tabs []string
	for t := Tab(0); t < numTabs; t++ {
		name := tabNames[t]
		if t == TabFavourites {
			name += fmt.Sprintf(" %d", len(m.favs))
		}
		if t == m.tab {
			tabs = append(tabs, s.Selected.Render(" "+name+" "))
		} else {
			tabs = append(tabs, s.Muted.Render(" "+name+" "))
		}
	}
	lines := []string{strings.Join(tabs, " ")}
	if m.tab == TabSearch {
		lines = append(lines, "", s.Accent.Render("› ")+s.Text.Render(m.query)+s.Muted.Render("▏"))
	}
	lines = append(lines, "")

	items := m.shown()
	l := m.lists[m.tab]
	switch {
	case m.tab == TabSearch && m.cfg.SearchNote != "":
		lines = append(lines, s.Muted.Render(m.cfg.SearchNote))
	case m.tab == TabTop && m.cfg.TopNote != "":
		lines = append(lines, s.Muted.Render(m.cfg.TopNote))
	case l.loading && m.tab != TabFavourites:
		lines = append(lines, s.Muted.Render("asking Docker Hub…"))
	case l.err != "" && m.tab != TabFavourites:
		lines = append(lines, s.LevelText(theme.LevelWarn).Render(ansi.Truncate(l.err, inner, "…")))
	case len(items) == 0:
		lines = append(lines, s.Muted.Render(empty(m.tab, l.loaded)))
	default:
		rows := max(3, min(len(items), height-16))
		cur := l.cursor
		start := max(0, min(cur-rows/2, len(items)-rows))
		refW := 0
		for _, it := range items {
			refW = max(refW, ansi.StringWidth(it.Ref))
		}
		refW = min(refW, 40)
		metaW := 0
		for _, it := range items {
			metaW = max(metaW, ansi.StringWidth(meta(it)))
		}
		for i := start; i < min(len(items), start+rows); i++ {
			it := items[i]
			mark := " "
			if m.isFav(it.Ref) {
				mark = "♥"
			}
			ref := ansi.Truncate(it.Ref, refW, "…")
			ref += strings.Repeat(" ", max(0, refW-ansi.StringWidth(ref)))
			md := meta(it)
			md += strings.Repeat(" ", max(0, metaW-ansi.StringWidth(md)))
			rest := ansi.Truncate(strings.TrimRight(md+"  "+it.Description, " "), max(10, inner-refW-4), "…")
			if i == cur {
				lines = append(lines, s.Selected.Render("›"+ref+" ")+s.Accent.Render(mark)+" "+s.Muted.Render(rest))
				continue
			}
			lines = append(lines, " "+s.Text.Render(ref)+" "+s.Accent.Render(mark)+" "+s.Muted.Render(rest))
		}
	}
	if m.note != "" {
		lines = append(lines, "", s.Muted.Render(m.note))
	}
	hint := "tab switch · ↑/↓ move · enter choose · ctrl+f favourite · esc close"
	if m.tab == TabSearch {
		hint = "type, enter searches · tab switch · ↑/↓ move · ctrl+f favourite · esc close"
	}
	return modal.Frame(s, "Docker Hub", strings.Join(lines, "\n")+"\n\n"+s.Muted.Render(hint), width)
}

func empty(t Tab, loaded bool) string {
	switch t {
	case TabSearch:
		if loaded {
			return "nothing found"
		}
		return "type a name and press enter: grafana, redis, immich…"
	case TabTop:
		return "nothing to show"
	}
	return "no favourites yet: ctrl+f on an image adds it"
}
