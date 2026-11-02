// Package library is the Library view (7): the catalog of well-known Docker
// images and Incus system images, with the selected entry's defaults on the
// right. It reads the catalog and a store snapshot only; running an entry is
// the app's business (RunMsg).
package library

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/catalog"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/panel"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
)

// Label keys an instance created from an entry carries (the Incus one is
// the user.* key, which the adapters expose without the prefix).
const (
	LabelDocker = "io.viewdi.library"
	LabelIncus  = "viewdi.library"
)

// maxFilter caps the filter prompt.
const maxFilter = 64

// RunMsg asks the app to run an entry through the create form.
type RunMsg struct{ Entry catalog.Entry }

// SearchMsg asks the app to search Docker Hub for an image to run.
type SearchMsg struct{}

// UnusedMsg asks the app to list the local images no instance uses.
type UnusedMsg struct{}

// LocalImagesMsg delivers the references of the images each engine has
// locally, for the "local" marker. The app loads them from the daemons.
type LocalImagesMsg struct {
	Engine engine.ID
	Refs   []string
}

// Panes of the view.
const (
	paneEntries = iota // focused first: the list is what you came for
	paneCategories
	paneDetail
)

const allCategories = "All"

// View is the Library view.
type View struct {
	keys    *keys.Map
	cat     *catalog.Catalog
	snap    *store.Snapshot
	local   map[engine.ID]map[string]bool
	engine  engine.ID // "" = both
	filter  string
	typing  bool
	catIdx  int // 0 = All
	cats    []string
	rows    []catalog.Entry
	cursor  int
	selID   string
	offset  int
	page    int
	scroll  int // first line of the detail pane
	running map[string]int
}

var (
	_ views.View          = (*View)(nil)
	_ views.ActionHandler = (*View)(nil)
	_ views.Inputter      = (*View)(nil)
)

// New returns the view over a catalog (nil shows an empty library).
func New(km *keys.Map, cat *catalog.Catalog) *View {
	if cat == nil {
		cat = catalog.New()
	}
	v := &View{keys: km, cat: cat, page: 15, local: map[engine.ID]map[string]bool{}}
	v.cats = append([]string{allCategories}, cat.Categories()...)
	v.rebuild()
	return v
}

// Title implements views.View.
func (v *View) Title() string { return "Library" }

// Scope implements views.View.
func (v *View) Scope() keys.Scope { return keys.Library }

// ShowsInstances implements views.View.
func (v *View) ShowsInstances() bool { return false }

// Panes implements views.View.
func (v *View) Panes() int { return 3 }

// Inputting implements views.Inputter.
func (v *View) Inputting() bool { return v.typing }

// Selected returns the entry under the cursor.
func (v *View) Selected() (catalog.Entry, bool) {
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return catalog.Entry{}, false
	}
	return v.rows[v.cursor], true
}

// Rows returns the visible entries.
func (v *View) Rows() []catalog.Entry { return v.rows }

// Filter returns the filter text.
func (v *View) Filter() string { return v.filter }

// Update implements views.View.
func (v *View) Update(msg tea.Msg) (views.View, tea.Cmd) {
	switch msg := msg.(type) {
	case views.DataMsg:
		v.snap = msg.Snapshot
		v.countRunning()
	case LocalImagesMsg:
		set := map[string]bool{}
		for _, r := range msg.Refs {
			set[r] = true
		}
		v.local[msg.Engine] = set
	case views.ResizeMsg:
		v.page = max(1, msg.Height-5)
		v.keepVisible()
	}
	return v, nil
}

// countRunning counts the running instances created from each entry.
func (v *View) countRunning() {
	v.running = map[string]int{}
	if v.snap == nil {
		return
	}
	for _, in := range v.snap.Instances {
		if in.State != engine.StateRunning {
			continue
		}
		id := in.Labels[LabelDocker]
		if id == "" {
			id = in.Labels[LabelIncus]
		}
		if id != "" {
			v.running[id]++
		}
	}
}

func (v *View) isLocal(e catalog.Entry) bool {
	set := v.local[e.Engine]
	return set[e.Image] || set[strings.TrimPrefix(e.Image, "images:")]
}

// rebuild recomputes the visible entries, keeping the cursor on the same one.
func (v *View) rebuild() {
	q := strings.ToLower(v.filter)
	cat := ""
	if v.catIdx > 0 && v.catIdx < len(v.cats) {
		cat = v.cats[v.catIdx]
	}
	v.rows = v.rows[:0]
	for _, e := range v.cat.Entries() {
		switch {
		case v.engine != "" && e.Engine != v.engine:
		case cat != "" && e.Category != cat:
		case q != "" && !matches(q, e):
		default:
			v.rows = append(v.rows, e)
		}
	}
	if i := slices.IndexFunc(v.rows, func(e catalog.Entry) bool { return e.ID == v.selID }); i >= 0 {
		v.cursor = i
	} else {
		v.cursor = max(0, min(v.cursor, len(v.rows)-1))
	}
	if len(v.rows) > 0 {
		v.selID = v.rows[v.cursor].ID
	}
	v.scroll = 0
	v.keepVisible()
}

func matches(q string, e catalog.Entry) bool {
	for _, s := range []string{e.Name, e.Image, e.Category, e.Description, e.ID} {
		if strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}

func (v *View) keepVisible() {
	switch {
	case v.cursor < v.offset:
		v.offset = v.cursor
	case v.cursor >= v.offset+v.page:
		v.offset = v.cursor - v.page + 1
	}
	v.offset = max(0, min(v.offset, len(v.rows)-v.page))
}

func (v *View) move(delta int) {
	if len(v.rows) == 0 {
		return
	}
	v.cursor = max(0, min(len(v.rows)-1, v.cursor+delta))
	v.selID = v.rows[v.cursor].ID
	v.scroll = 0
	v.keepVisible()
}

func (v *View) moveCategory(delta int) {
	v.catIdx = max(0, min(len(v.cats)-1, v.catIdx+delta))
	v.rebuild()
}

// HandleAction implements views.ActionHandler: "/" opens the filter prompt,
// "e" cycles the engine filter, esc clears the filter, enter runs the entry.
func (v *View) HandleAction(action string) (views.View, tea.Cmd, bool) {
	switch action {
	case keys.ActFilter:
		v.typing = true
		return v, nil, true
	case keys.ActEngine:
		switch v.engine {
		case "":
			v.engine = engine.Docker
		case engine.Docker:
			v.engine = engine.Incus
		default:
			v.engine = ""
		}
		v.rebuild()
		return v, nil, true
	case keys.ActBack:
		if v.filter != "" {
			v.filter = ""
			v.rebuild()
			return v, nil, true
		}
	case keys.ActOpen:
		e, ok := v.Selected()
		if !ok {
			return v, nil, true
		}
		return v, func() tea.Msg { return RunMsg{Entry: e} }, true
	}
	return v, nil, false
}

// HandleKey implements views.View.
func (v *View) HandleKey(msg tea.KeyPressMsg, focus int) (views.View, tea.Cmd, bool) {
	if v.typing {
		return v.handleInput(msg)
	}
	act := v.keys.Action(msg, keys.Library)
	if focus == paneDetail {
		switch act {
		case "up":
			v.scroll = max(0, v.scroll-1)
		case "down":
			v.scroll++
		case "page_up":
			v.scroll = max(0, v.scroll-v.page)
		case "page_down":
			v.scroll += v.page
		case "home":
			v.scroll = 0
		default:
			return v, nil, false
		}
		return v, nil, true
	}
	switch act {
	case "search":
		return v, func() tea.Msg { return SearchMsg{} }, true
	case "unused":
		return v, func() tea.Msg { return UnusedMsg{} }, true
	}
	step := func(delta int) {
		if focus == paneCategories {
			v.moveCategory(delta)
			return
		}
		v.move(delta)
	}
	switch act {
	case "up":
		step(-1)
	case "down":
		step(1)
	case "page_up":
		step(-v.page)
	case "page_down":
		step(v.page)
	case "home":
		step(-len(v.rows) - len(v.cats))
	case "end":
		step(len(v.rows) + len(v.cats))
	default:
		return v, nil, false
	}
	return v, nil, true
}

// handleInput edits the filter prompt like the Table's.
func (v *View) handleInput(msg tea.KeyPressMsg) (views.View, tea.Cmd, bool) {
	switch v.keys.Action(msg, keys.Input) {
	case keys.ActConfirm:
		v.typing = false
		return v, nil, true
	case keys.ActCancel:
		v.typing, v.filter = false, ""
		v.rebuild()
		return v, nil, true
	case "clear":
		v.filter = ""
		v.rebuild()
		return v, nil, true
	}
	switch msg.String() {
	case "tab", "shift+tab", "ctrl+c":
		return v, nil, false
	case "backspace":
		if v.filter != "" {
			_, n := utf8.DecodeLastRuneInString(v.filter)
			v.filter = v.filter[:len(v.filter)-n]
			v.rebuild()
		}
		return v, nil, true
	}
	if t := msg.Text; t != "" && utf8.RuneCountInString(v.filter) < maxFilter && !strings.ContainsFunc(t, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		v.filter += t
		v.rebuild()
	}
	return v, nil, true
}

// Hints implements views.View.
func (v *View) Hints(km *keys.Map) []key.Binding {
	if v.typing {
		return km.Bindings(keys.Input)
	}
	run := km.Binding(keys.Global, keys.ActOpen)
	run.SetHelp(run.Help().Key, "run")
	return []key.Binding{run, km.Binding(keys.Global, keys.ActFilter), km.Binding(keys.Global, keys.ActEngine), km.Binding(keys.Library, "search"), km.Binding(keys.Library, "unused"), km.Binding(keys.Library, "up"), km.Binding(keys.Library, "down")}
}

// Render implements views.View.
func (v *View) Render(ctx views.Context) string {
	title := fmt.Sprintf("Library · %d of %d", len(v.rows), len(v.cat.Entries()))
	if v.engine != "" {
		title += " · " + v.engine.Label()
	}
	if v.typing || v.filter != "" {
		cursor := ""
		if v.typing {
			cursor = "█"
		}
		title += " · /" + textsafe.Clean(v.filter) + cursor
	}
	if ctx.Mode == layout.Wide {
		catW, detW := 24, 44
		entW := ctx.Width - catW - detW
		return lipgloss.JoinHorizontal(lipgloss.Top,
			v.catPane(ctx, catW),
			v.entryPane(ctx, entW, title),
			v.detailPane(ctx, detW),
		)
	}
	entH := max(5, ctx.Height*2/5)
	top := v.entryPane(ctx, ctx.Width, title, entH)
	bottom := v.detailPane(ctx, ctx.Width, ctx.Height-entH)
	return top + "\n" + bottom
}

func (v *View) catPane(ctx views.Context, w int) string {
	s := ctx.Styles
	inner := w - 4
	var lines []string
	for i, c := range v.cats {
		n := 0
		for _, e := range v.cat.Entries() {
			if c == allCategories || e.Category == c {
				n++
			}
		}
		count := strconv.Itoa(n)
		text := ansi.Truncate(textsafe.Clean(c), inner-2-len(count)-1, "…")
		line := text + strings.Repeat(" ", max(1, inner-2-ansi.StringWidth(text)-len(count))) + count
		if i == v.catIdx {
			lines = append(lines, s.Selected.Render(pad("▶ "+line, inner)))
		} else {
			lines = append(lines, s.Text.Render("  "+line))
		}
	}
	return panel.Render(s, "Categories", layout.Fit(strings.Join(lines, "\n"), inner, ctx.Height-2), w, ctx.Height, ctx.Focus == paneCategories)
}

func (v *View) entryPane(ctx views.Context, w int, title string, height ...int) string {
	s := ctx.Styles
	h := ctx.Height
	if len(height) > 0 {
		h = height[0]
	}
	inner := w - 4
	page := max(1, h-3)
	v.page = page
	var lines []string
	if len(v.rows) == 0 {
		msg := "The library is empty."
		if len(v.cat.Entries()) > 0 {
			msg = "No entry matches. Esc clears the filter."
		}
		lines = append(lines, s.Muted.Render(msg))
	}
	nameW := 24
	imgW := max(10, inner-2-nameW-1-8-1)
	lines = append([]string{s.TableHeader.Render(pad("  NAME", nameW+2) + " " + pad("IMAGE", imgW) + " " + "STATE")}, lines...)
	end := min(len(v.rows), v.offset+page)
	for i := v.offset; i < end; i++ {
		e := v.rows[i]
		mark := v.marker(e)
		cells := pad(textsafe.Clean(e.Name), nameW) + " " + pad(textsafe.Clean(e.Image), imgW) + " " + mark
		if i == v.cursor {
			lines = append(lines, s.Selected.Render(pad("▶ "+cells, inner)))
			continue
		}
		lines = append(lines, "  "+s.Text.Render(pad(textsafe.Clean(e.Name), nameW))+" "+s.Muted.Render(pad(textsafe.Clean(e.Image), imgW))+" "+s.LevelText(theme.LevelOK).Render(mark))
	}
	return panel.Render(s, title, layout.Fit(strings.Join(lines, "\n"), inner, h-2), w, h, ctx.Focus == paneEntries)
}

// marker is the state column: how many instances run from the entry and
// whether its image is already local.
func (v *View) marker(e catalog.Entry) string {
	var parts []string
	if n := v.running[e.ID]; n > 0 {
		parts = append(parts, fmt.Sprintf("● %d up", n))
	}
	if v.isLocal(e) {
		parts = append(parts, "⤓ local")
	}
	return strings.Join(parts, " ")
}

func (v *View) detailPane(ctx views.Context, w int, height ...int) string {
	s := ctx.Styles
	h := ctx.Height
	if len(height) > 0 {
		h = height[0]
	}
	inner := w - 4
	e, ok := v.Selected()
	if !ok {
		return panel.Render(s, "Details", layout.Fit(s.Muted.Render("Nothing selected."), inner, h-2), w, h, ctx.Focus == paneDetail)
	}
	lines := v.detailLines(s, e, inner)
	start := max(0, min(v.scroll, max(0, len(lines)-(h-2))))
	v.scroll = start
	return panel.Render(s, textsafe.Clean(e.Name), layout.Fit(strings.Join(lines[start:], "\n"), inner, h-2), w, h, ctx.Focus == paneDetail)
}

func (v *View) detailLines(s *theme.Styles, e catalog.Entry, w int) []string {
	var out []string
	add := func(label, text string) {
		if text != "" {
			out = append(out, s.Muted.Render(pad(label, 10))+" "+s.Text.Render(textsafe.Clean(text)))
		}
	}
	src := "built-in"
	if e.Source == catalog.User {
		src = "yours"
	}
	add("engine", e.Engine.Label()+" · "+src)
	add("image", e.Image)
	if e.Description != "" {
		out = append(out, "", s.Text.Render(textsafe.Clean(e.Description)))
	}
	out = append(out, "")
	if e.Engine == engine.Docker {
		var ports []string
		for _, p := range e.Ports {
			host := p.Host
			if host == 0 {
				host = p.Container
			}
			ports = append(ports, fmt.Sprintf("%d→%d%s", host, p.Container, protoSuffix(p.Protocol)))
		}
		add("ports", strings.Join(ports, "  "))
		for _, ev := range e.Env {
			val := ev.Value
			if ev.Generate != "" {
				val = "(generated)"
			}
			if ev.Description != "" && val == "" {
				val = ev.Description
			}
			add("env", ev.Name+"="+val)
		}
		for _, vol := range e.Volumes {
			add("volume", vol.Name+" → "+vol.Path)
		}
		add("restart", e.Restart)
	} else {
		add("types", strings.Join(e.Kinds, ", ")+" (default "+e.DefaultKind+")")
		if e.CPU > 0 {
			add("cpu", strconv.Itoa(e.CPU))
		}
		add("memory", e.Memory)
		add("profiles", strings.Join(e.Profiles, ", "))
		if e.CloudInit != "" {
			lines := strings.Count(strings.TrimSpace(e.CloudInit), "\n") + 1
			add("cloud-init", fmt.Sprintf("preset of %d lines, applied on first boot", lines))
		}
	}
	if m := v.marker(e); m != "" {
		add("state", m)
	}
	if e.Note != "" {
		out = append(out, "", s.Muted.Render("after creating:"))
		hostPorts := map[uint16]uint16{}
		for _, p := range e.Ports {
			hostPorts[p.Container] = cmpOr(p.Host, p.Container)
		}
		for _, l := range strings.Split(strings.TrimSpace(catalog.RenderNote(e.Note, e.ID, hostPorts)), "\n") {
			out = append(out, s.Text.Render(textsafe.Clean(l)))
		}
	}
	add("docs", e.Docs)
	return wrap(out, w)
}

func protoSuffix(p string) string {
	if p == "udp" {
		return "/udp"
	}
	return ""
}

// wrap cuts every line to w cells (the pane scrolls vertically, not sideways).
func wrap(lines []string, w int) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, ansi.Truncate(l, w, "…"))
	}
	return out
}

func pad(s string, w int) string {
	n := ansi.StringWidth(s)
	if n > w {
		return ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-n)
}

func cmpOr(a, b uint16) uint16 {
	if a != 0 {
		return a
	}
	return b
}
