// Package updates is the Updates view (6): engine and image updates found
// by the update checker, with their release notes, upgrade hints and the
// user's snooze and ignore decisions. The view only reads the store
// snapshot; snoozing, ignoring, opening notes and checking are requests
// the app carries out.
package updates

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/panel"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
	upd "github.com/blokxz/viewDI/internal/updates"
)

// SnoozeMsg asks the app to snooze (or unsnooze) an update.
type SnoozeMsg struct{ Key, Text string }

// IgnoreMsg asks the app to ignore (or stop ignoring) one version.
type IgnoreMsg struct{ Key, Version, Text string }

// OpenNotesMsg asks the app to open release notes in the browser.
type OpenNotesMsg struct{ URL string }

// row is one line of the list: an engine or an image.
type row struct {
	key, version, text string
	engine             *upd.EngineStatus
	image              *upd.ImageUpdate
}

// Sort columns.
var sorts = []string{"engine", "name", "status"}

// View is the Updates view.
type View struct {
	keys    *keys.Map
	data    store.Updates
	rows    []row // engines first, then images
	nEng    int
	cursor  int
	selKey  string
	offset  int
	page    int
	sort    int
	reverse bool
	visible map[string]bool // keys of the visible updates (store snapshot)
	now     time.Time
}

var _ views.View = (*View)(nil)

// New returns the view.
func New(km *keys.Map) *View { return &View{keys: km, page: 10} }

// Title implements views.View.
func (v *View) Title() string { return "Updates" }

// Scope implements views.View.
func (v *View) Scope() keys.Scope { return keys.Updates }

// ShowsInstances implements views.View.
func (v *View) ShowsInstances() bool { return false }

// Panes implements views.View.
func (v *View) Panes() int { return 1 }

// Update implements views.View.
func (v *View) Update(msg tea.Msg) (views.View, tea.Cmd) {
	switch msg := msg.(type) {
	case views.DataMsg:
		v.data = msg.Snapshot.Updates
		v.build()
	case views.ResizeMsg:
		v.page = max(1, msg.Height-12)
		v.move(0)
	}
	return v, nil
}

// build lists the rows and keeps the cursor on the same update.
func (v *View) build() {
	r := v.data.Report
	v.visible = map[string]bool{}
	for _, e := range v.data.Engines {
		v.visible[e.Key()] = true
	}
	for _, img := range v.data.Images {
		v.visible[img.Key()] = true
	}
	v.rows = v.rows[:0]
	for i := range r.Engines {
		e := &r.Engines[i]
		v.rows = append(v.rows, row{key: e.Key(), version: e.Available, text: engineText(*e), engine: e})
	}
	v.nEng = len(v.rows)
	for i := range r.Images {
		img := &r.Images[i]
		v.rows = append(v.rows, row{key: img.Key(), version: img.Available, text: textsafe.Clean(img.Check.Ref), image: img})
	}
	v.sortRows(v.rows[:v.nEng])
	v.sortRows(v.rows[v.nEng:])
	v.cursor = 0
	for i, rw := range v.rows {
		if rw.key == v.selKey {
			v.cursor = i
		}
	}
	v.move(0)
}

func engineText(e upd.EngineStatus) string {
	if e.HasUpdate() {
		return upd.EngineText(e)
	}
	return e.Engine.Label()
}

func (v *View) sortRows(rs []row) {
	slices.SortStableFunc(rs, func(a, b row) int {
		var c int
		switch sorts[v.sort] {
		case "name":
			c = cmp.Compare(a.text, b.text)
		case "status":
			c = cmp.Compare(v.rank(b), v.rank(a))
		}
		c = cmp.Or(c, cmp.Compare(engineOf(a), engineOf(b)), cmp.Compare(a.key, b.key))
		if v.reverse {
			return -c
		}
		return c
	})
}

func engineOf(r row) string {
	if r.engine != nil {
		return string(r.engine.Engine)
	}
	return string(r.image.Check.Engine)
}

// rank orders by status: visible security, visible, hidden, up to date.
func (v *View) rank(r row) int {
	hidden := !v.visible[r.key]
	switch {
	case r.engine != nil && !r.engine.HasUpdate():
		return 0
	case hidden:
		return 1
	case r.engine != nil && r.engine.Level == upd.LevelSecurity:
		return 3
	}
	return 2
}

func (v *View) move(delta int) {
	if len(v.rows) == 0 {
		v.cursor, v.offset = 0, 0
		return
	}
	v.cursor = max(0, min(len(v.rows)-1, v.cursor+delta))
	v.selKey = v.rows[v.cursor].key
	switch {
	case v.cursor < v.offset:
		v.offset = v.cursor
	case v.cursor >= v.offset+v.page:
		v.offset = v.cursor - v.page + 1
	}
}

// Selected returns the row under the cursor.
func (v *View) selected() (row, bool) {
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return row{}, false
	}
	return v.rows[v.cursor], true
}

// HandleKey implements views.View.
func (v *View) HandleKey(msg tea.KeyPressMsg, _ int) (views.View, tea.Cmd, bool) {
	switch v.keys.Action(msg, keys.Updates) {
	case "up":
		v.move(-1)
	case "down":
		v.move(1)
	case "home":
		v.move(-len(v.rows))
	case "end":
		v.move(len(v.rows))
	case "sort":
		v.sort = (v.sort + 1) % len(sorts)
		v.build()
	case "sort_reverse":
		v.reverse = !v.reverse
		v.build()
	case "snooze":
		r, ok := v.selected()
		if !ok || (r.engine != nil && !r.engine.HasUpdate()) {
			return v, nil, true
		}
		m := SnoozeMsg{Key: r.key, Text: r.text}
		return v, func() tea.Msg { return m }, true
	case "ignore":
		r, ok := v.selected()
		if !ok || (r.engine != nil && !r.engine.HasUpdate()) {
			return v, nil, true
		}
		m := IgnoreMsg{Key: r.key, Version: r.version, Text: r.text}
		return v, func() tea.Msg { return m }, true
	default:
		return v, nil, false
	}
	return v, nil, true
}

// HandleAction implements views.ActionHandler: enter opens the release
// notes of the engine under the cursor.
func (v *View) HandleAction(action string) (views.View, tea.Cmd, bool) {
	if action != keys.ActOpen {
		return v, nil, false
	}
	r, ok := v.selected()
	if !ok || r.engine == nil || r.engine.NotesURL == "" {
		return v, nil, true
	}
	m := OpenNotesMsg{URL: r.engine.NotesURL}
	return v, func() tea.Msg { return m }, true
}

// Hints implements views.View.
func (v *View) Hints(km *keys.Map) []key.Binding {
	notes := km.Binding(keys.Global, keys.ActOpen)
	notes.SetHelp(notes.Help().Key, "release notes")
	return []key.Binding{
		notes, km.Binding(keys.Updates, "snooze"), km.Binding(keys.Updates, "ignore"),
		km.Binding(keys.Global, keys.ActCheckUpdates), km.Binding(keys.Updates, "sort"),
	}
}

// Render implements views.View.
func (v *View) Render(ctx views.Context) string {
	v.now = ctx.Now
	s := ctx.Styles
	inner := ctx.Width - 4
	r := v.data.Report
	var lines []string

	switch {
	case !v.data.Enabled:
		lines = append(lines, s.Muted.Render("Update checks are off (updates.enabled = false in config.toml): viewDI makes no network calls."))
		return v.frame(ctx, "Updates · off", lines)
	case r.EnginesAt.IsZero() && r.ImagesAt.IsZero() && !r.Running && r.Failed == "":
		lines = append(lines, s.Muted.Render("No check yet: the first one runs 30 s after startup. Press U to check now."))
		return v.frame(ctx, "Updates", lines)
	}
	lines = append(lines, v.status(s))
	if r.Failed != "" {
		lines = append(lines, s.LevelText(theme.LevelWarn).Render(ansi.Truncate("⚠ last check failed: "+textsafe.Clean(r.Failed), inner, "…")))
	}

	lines = append(lines, "", s.TableHeader.Render(pad("  ENGINE", 18)+" "+pad("INSTALLED", 10)+" "+pad("AVAILABLE", 12)+" "+pad("LEVEL", 9)+" "+pad("CHANNEL", 8)+" "+pad("SOURCE", 7)+" "+pad("RELEASED", 10)+" "+"STATUS"))
	if v.nEng == 0 {
		lines = append(lines, s.Muted.Render("  not checked yet"))
	}
	imgHeader := s.TableHeader.Render(pad("  ENGINE", 8) + " " + pad("IMAGE", 33) + " " + pad("INSTANCES", 22) + " " + pad("CURRENT → AVAILABLE", 31) + " " + "STATUS")
	end := min(len(v.rows), v.offset+v.page)
	shownImgHeader := false
	for i := v.offset; i < end; i++ {
		if i >= v.nEng && !shownImgHeader {
			lines = append(lines, "", imgHeader)
			shownImgHeader = true
		}
		lines = append(lines, v.renderRow(s, i, inner))
	}
	if !shownImgHeader && end >= v.nEng {
		lines = append(lines, "", imgHeader)
		shownImgHeader = true
	}
	if shownImgHeader && len(r.Images) == 0 {
		switch {
		case r.ImagesAt.IsZero():
			lines = append(lines, s.Muted.Render("  not checked yet"))
		default:
			lines = append(lines, s.Muted.Render(fmt.Sprintf("  all %d image sources are up to date", r.ImagesChecked-r.ImageErrors)))
		}
	}
	lines = append(lines, "")
	lines = append(lines, v.footer(s, inner)...)
	title := "Updates"
	if n := v.data.Count(); n > 0 {
		title = fmt.Sprintf("Updates · %d available", n)
	}
	return v.frame(ctx, title, lines)
}

func (v *View) frame(ctx views.Context, title string, lines []string) string {
	inner := ctx.Width - 4
	return panel.Render(ctx.Styles, title, layout.Fit(strings.Join(lines, "\n"), inner, ctx.Height-2), ctx.Width, ctx.Height, ctx.Focus == 0)
}

// status is the first line: when each check ran and what it covered.
func (v *View) status(s *theme.Styles) string {
	r := v.data.Report
	at := func(t time.Time) string {
		if t.IsZero() {
			return "never"
		}
		if t.YearDay() == v.now.YearDay() && t.Year() == v.now.Year() {
			return t.In(time.Local).Format("15:04")
		}
		return t.In(time.Local).Format("Jan 2 15:04")
	}
	text := "engines checked " + at(r.EnginesAt) + " · images checked " + at(r.ImagesAt)
	if r.ImagesChecked > 0 {
		text += fmt.Sprintf(" (%d sources)", r.ImagesChecked)
	}
	if r.Running {
		return s.Muted.Render(text+" · ") + s.LevelText(theme.LevelInfo).Render("checking…")
	}
	return s.Muted.Render(text + " · U checks now")
}

// statusCell is the decision column of a row.
func (v *View) statusCell(r row) (string, bool) {
	snoozed, ignored := v.data.Decisions.Hidden(r.key, r.version, v.now)
	switch {
	case ignored:
		return "ignored", true
	case snoozed:
		return "snoozed until " + v.data.Decisions.Snoozed[r.key].In(time.Local).Format("Jan 2"), true
	}
	return "", false
}

func (v *View) renderRow(s *theme.Styles, i, inner int) string {
	r := v.rows[i]
	status, hidden := v.statusCell(r)
	var text string
	lvl := theme.LevelInfo
	if e := r.engine; e != nil {
		avail, level, released := "—", "", ""
		switch {
		case e.Err != "" && e.Installed == "":
			status = "check failed"
			lvl = theme.LevelWarn
		case e.HasUpdate():
			avail, level = e.Available, e.Level.String()
			if e.Level == upd.LevelSecurity {
				lvl = theme.LevelErr
			} else {
				lvl = theme.LevelWarn
			}
		default:
			if status == "" {
				status = "up to date"
			}
			lvl = theme.LevelOK
		}
		if !e.Released.IsZero() && e.HasUpdate() {
			released = e.Released.Format("2006-01-02")
		}
		mark := "  "
		if e.HasUpdate() && !hidden {
			mark = "⬆ "
		}
		text = pad(mark+e.Engine.Label(), 18) + " " + pad(textsafe.Clean(e.Installed), 10) + " " + pad(textsafe.Clean(avail), 12) + " " + pad(level, 9) + " " + pad(e.Channel, 8) + " " + pad(e.Source, 7) + " " + pad(released, 10) + " " + status
	} else {
		img := r.image
		cur, next := short(img.Current), short(img.Available)
		if img.Serial != "" && img.NewSerial != "" && img.Serial != img.NewSerial {
			cur, next = img.Serial, img.NewSerial
		}
		if status == "" && img.Compose {
			status = "compose"
		}
		mark := "⬆ "
		if hidden {
			mark = "  "
		} else {
			lvl = theme.LevelWarn
		}
		names := textsafe.Clean(strings.Join(img.Names, ", "))
		text = pad(mark+img.Check.Engine.Short(), 8) + " " + pad(r.text, 33) + " " + pad(names, 22) + " " + pad(cur+" → "+next, 31) + " " + status
	}
	if i == v.cursor {
		return s.Selected.Render(pad(text, inner))
	}
	text = ansi.Truncate(text, inner, "…")
	if hidden {
		return s.Muted.Render(text)
	}
	return s.LevelText(lvl).Render(ansi.Truncate(text, 2, "")) + s.Text.Render(ansi.TruncateLeft(text, 2, ""))
}

// footer details the selected row: the upgrade hint and release notes of
// an engine, the full digests and instances of an image.
func (v *View) footer(s *theme.Styles, inner int) []string {
	r, ok := v.selected()
	if !ok {
		return nil
	}
	line := func(label, val string) string {
		return s.Muted.Render(pad(label, 10)) + s.Text.Render(ansi.Truncate(textsafe.Clean(val), inner-10, "…"))
	}
	var out []string
	if e := r.engine; e != nil {
		if e.Err != "" {
			out = append(out, s.LevelText(theme.LevelWarn).Render(ansi.Truncate("check failed: "+textsafe.Clean(e.Err), inner, "…")))
		}
		if e.HasUpdate() && e.Hint != "" {
			out = append(out, line("upgrade", e.Hint))
		}
		if e.NotesURL != "" {
			out = append(out, line("notes", e.NotesURL+"  (enter)"))
		}
		if e.Engine == "incus" && e.Channel == "LTS" && e.HasUpdate() {
			out = append(out, s.Muted.Render("LTS channel: only 6.0.x releases are offered (incus_channel overrides it)"))
		}
		return out
	}
	img := r.image
	out = append(out, line("current", img.Current), line("available", img.Available), line("used by", strings.Join(img.Names, ", ")))
	if img.Check.Engine == "incus" {
		out = append(out, s.Muted.Render("new instances use the new build; existing ones keep theirs (incus rebuild would wipe the root disk)"))
	}
	return out
}

// short abbreviates a digest or fingerprint to 12 hex digits.
func short(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		d = d[:12]
	}
	return textsafe.Clean(d)
}

func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}
