// Package activity is the Activity view: one feed for everything that is
// happening or happened, instead of separate Jobs, Updates, alerts and
// events screens. NOW holds the alerts, the jobs in flight and the updates
// waiting for a decision; TODAY and EARLIER hold the history, including what
// happened on the daemons outside viewDI. Every line carries the action
// that fixes or cancels it; the actions are the ones the Jobs and Updates views had
// requests, which the app already carries out.
package activity

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/panel"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
)

// Chip is a filter of the feed.
type Chip int

// Chips, in display order.
const (
	ChipAll Chip = iota
	ChipAlerts
	ChipUpdates
	ChipJobs
	ChipHistory
)

var chipNames = [...]string{"Everything", "Alerts", "Updates", "Jobs", "History"}

func (c Chip) String() string { return chipNames[c] }

// has reports whether the chip shows an entry.
func (c Chip) has(e Entry) bool {
	switch c {
	case ChipAlerts:
		return e.Kind == KindAlert
	case ChipUpdates:
		return e.Kind == KindUpdate
	case ChipJobs:
		return e.Kind == KindJob
	case ChipHistory:
		return e.Kind == KindEvent || e.Kind == KindJob && e.Job.State.Final()
	}
	return true
}

// View is the Activity view.
type View struct {
	keys    *keys.Map
	snap    *store.Snapshot
	now     time.Time
	builtAt time.Time
	dirty   bool
	all     []Entry
	shown   []Entry
	chip    Chip
	cursor  int
	sel     string // ID of the entry under the cursor; survives rebuilds
	offset  int    // first visible display line
	page    int
}

var _ views.View = (*View)(nil)

// New returns the view.
func New(km *keys.Map) *View { return &View{keys: km, page: 20} }

// Title implements views.View.
func (v *View) Title() string { return "Activity" }

// Scope implements views.View.
func (v *View) Scope() keys.Scope { return keys.Activity }

// ShowsInstances implements views.View.
func (v *View) ShowsInstances() bool { return false }

// Panes implements views.View.
func (v *View) Panes() int { return 1 }

// Chip returns the active filter.
func (v *View) Chip() Chip { return v.chip }

// Entries returns the entries shown under the active filter.
func (v *View) Entries() []Entry { return v.shown }

// Selected returns the entry under the cursor.
func (v *View) Selected() (Entry, bool) {
	if v.cursor < 0 || v.cursor >= len(v.shown) {
		return Entry{}, false
	}
	return v.shown[v.cursor], true
}

// Update implements views.View.
func (v *View) Update(msg tea.Msg) (views.View, tea.Cmd) {
	switch msg := msg.(type) {
	case views.DataMsg:
		v.snap, v.dirty = msg.Snapshot, true
		if !msg.Now.IsZero() {
			v.now = msg.Now
		}
		if !v.now.IsZero() {
			v.rebuild(v.now)
		}
	case views.ResizeMsg:
		v.page = max(1, msg.Height-8)
	}
	return v, nil
}

// rebuild lists the feed again at now and keeps the cursor on the same entry.
func (v *View) rebuild(now time.Time) {
	v.all, v.builtAt, v.dirty = Build(v.snap, now), now, false
	v.refilter()
}

func (v *View) refilter() {
	v.shown = v.shown[:0]
	for _, e := range v.all {
		if v.chip.has(e) {
			v.shown = append(v.shown, e)
		}
	}
	v.cursor = 0
	for i, e := range v.shown {
		if e.ID == v.sel {
			v.cursor = i
		}
	}
	v.move(0)
}

func (v *View) move(delta int) {
	if len(v.shown) == 0 {
		v.cursor, v.sel, v.offset = 0, "", 0
		return
	}
	v.cursor = max(0, min(len(v.shown)-1, v.cursor+delta))
	v.sel = v.shown[v.cursor].ID
}

func (v *View) count(c Chip) int {
	n := 0
	for _, e := range v.all {
		if c.has(e) {
			n++
		}
	}
	return n
}

// HandleKey implements views.View.
func (v *View) HandleKey(msg tea.KeyPressMsg, _ int) (views.View, tea.Cmd, bool) {
	action := v.keys.Action(msg, keys.Activity)
	switch action {
	case "up":
		v.move(-1)
	case "down":
		v.move(1)
	case "home":
		v.move(-len(v.shown))
	case "end":
		v.move(len(v.shown))
	case "chip_prev", "chip_next":
		step := len(chipNames) - 1
		if action == "chip_next" {
			step = 1
		}
		v.chip = Chip((int(v.chip) + step) % len(chipNames))
		v.refilter()
	case "cancel":
		e, ok := v.Selected()
		if !ok || e.Job == nil || e.Job.State.Final() {
			return v, nil, true
		}
		return v, send(CancelMsg{ID: e.Job.ID}), true
	case "snooze":
		e, ok := v.Selected()
		if !ok || e.Engine == nil && e.Image == nil {
			return v, nil, true
		}
		return v, send(SnoozeMsg{Key: updateKey(e), Text: e.Title}), true
	case "ignore":
		e, ok := v.Selected()
		if !ok || e.Engine == nil && e.Image == nil {
			return v, nil, true
		}
		return v, send(IgnoreMsg{Key: updateKey(e), Version: updateVersion(e), Text: e.Title}), true
	case "upgrade":
		e, ok := v.Selected()
		if !ok || e.Kind != KindUpdate {
			return v, nil, true
		}
		m := UpgradeMsg{}
		if e.Engine != nil {
			st := *e.Engine
			m.Engine = &st
		}
		return v, send(m), true
	case ActPull, ActRefresh, ActRecreate:
		e, ok := v.Selected()
		if !ok || e.Kind != KindUpdate {
			return v, nil, true
		}
		m := ImageActionMsg{Action: action}
		if e.Image != nil {
			img := *e.Image
			m.Image = &img
		}
		return v, send(m), true
	default:
		return v, nil, false
	}
	return v, nil, true
}

func send(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }

func updateKey(e Entry) string {
	if e.Engine != nil {
		return e.Engine.Key()
	}
	return e.Image.Key()
}

func updateVersion(e Entry) string {
	if e.Engine != nil {
		return e.Engine.Available
	}
	return e.Image.Available
}

// HandleAction implements views.ActionHandler: enter reads the release notes
// of an engine update, or opens the machine the line is about.
func (v *View) HandleAction(action string) (views.View, tea.Cmd, bool) {
	if action != keys.ActOpen {
		return v, nil, false
	}
	e, ok := v.Selected()
	switch {
	case !ok:
	case e.Engine != nil && e.Engine.NotesURL != "":
		return v, send(OpenNotesMsg{URL: e.Engine.NotesURL}), true
	case e.Machine != "" && e.Kind != KindUpdate:
		return v, send(OpenMachineMsg{Key: e.Machine}), true
	}
	return v, nil, true
}

// Hints implements views.View: the verbs of the selected line.
func (v *View) Hints(km *keys.Map) []key.Binding {
	out := []key.Binding{km.Binding(keys.Activity, "chip_next")}
	e, ok := v.Selected()
	if !ok {
		return out
	}
	open := km.Binding(keys.Global, keys.ActOpen)
	switch {
	case e.Job != nil && !e.Job.State.Final():
		out = append(out, km.Binding(keys.Activity, "cancel"))
	case e.Engine != nil:
		open.SetHelp(open.Help().Key, "release notes")
		out = append(out, open, km.Binding(keys.Activity, "upgrade"), km.Binding(keys.Activity, "snooze"), km.Binding(keys.Activity, "ignore"))
	case e.Image != nil:
		out = append(out, km.Binding(keys.Activity, ActPull), km.Binding(keys.Activity, ActRefresh),
			km.Binding(keys.Activity, ActRecreate), km.Binding(keys.Activity, "snooze"), km.Binding(keys.Activity, "ignore"))
	case e.Machine != "":
		open.SetHelp(open.Help().Key, "open the machine")
		out = append(out, open)
	}
	return append(out, km.Binding(keys.Global, keys.ActCheckUpdates))
}

// line is one display line: a section header or an entry.
type line struct {
	header string
	idx    int
}

func (v *View) lines() []line {
	var out []line
	last := Section(-1)
	for i, e := range v.shown {
		if e.Section != last {
			out = append(out, line{header: e.Section.String(), idx: -1})
			last = e.Section
		}
		out = append(out, line{idx: i})
	}
	return out
}

// Render implements views.View.
func (v *View) Render(ctx views.Context) string {
	v.now = ctx.Now
	if v.dirty || v.builtAt.IsZero() || !ctx.Now.Truncate(time.Minute).Equal(v.builtAt.Truncate(time.Minute)) {
		v.rebuild(ctx.Now)
	}
	s := ctx.Styles
	inner := ctx.Width - 4

	footer := v.footer(s, inner)
	head := []string{v.chips(s, inner), ""}
	body := ctx.Height - 2 - len(head) - len(footer)
	if len(v.shown) == 0 {
		for _, l := range strings.Split(v.empty(), "\n") {
			head = append(head, s.Muted.Render(ansi.Truncate(l, inner, "…")))
		}
		return v.frame(ctx, head)
	}
	v.page = max(1, body)

	rows := v.lines()
	at := 0
	for i, l := range rows {
		if l.idx == v.cursor {
			at = i
		}
	}
	if at > 0 && rows[at-1].header != "" {
		at-- // keep the section header with its first entry
	}
	switch {
	case at < v.offset:
		v.offset = at
	case at >= v.offset+v.page:
		v.offset = at - v.page + 1
	}
	v.offset = max(0, min(v.offset, max(0, len(rows)-v.page)))

	lines := head
	end := min(len(rows), v.offset+v.page)
	for _, l := range rows[v.offset:end] {
		if l.header != "" {
			lines = append(lines, s.Muted.Render(l.header))
			continue
		}
		lines = append(lines, v.renderEntry(s, v.shown[l.idx], l.idx == v.cursor, inner, ctx.Now))
	}
	for len(lines) < len(head)+v.page {
		lines = append(lines, "")
	}
	return v.frame(ctx, append(lines, footer...))
}

func (v *View) frame(ctx views.Context, lines []string) string {
	inner := ctx.Width - 4
	return panel.Render(ctx.Styles, v.title(), layout.Fit(strings.Join(lines, "\n"), inner, ctx.Height-2), ctx.Width, ctx.Height, ctx.Focus == 0)
}

// title counts what is in the way: alerts, running jobs, waiting updates.
func (v *View) title() string {
	var alertsN, running, updates int
	for _, e := range v.all {
		switch {
		case e.Kind == KindAlert:
			alertsN++
		case e.Kind == KindJob && !e.Job.State.Final():
			running++
		case e.Kind == KindUpdate && e.Hidden == "":
			updates++
		}
	}
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{{alertsN, "alert"}, {running, "running"}, {updates, "update"}} {
		switch {
		case p.n == 0:
		case p.what == "running":
			parts = append(parts, fmt.Sprintf("%d running", p.n))
		default:
			parts = append(parts, plural(p.n, p.what))
		}
	}
	if len(parts) == 0 {
		return "Activity"
	}
	return "Activity · " + strings.Join(parts, " · ")
}

func plural(n int, what string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func (v *View) empty() string {
	if v.snap != nil && !v.snap.Updates.Enabled && (v.chip == ChipUpdates || v.chip == ChipAll) {
		return "Nothing is happening.\nUpdate checks are off (updates.enabled = false in config.toml): viewDI makes no network calls."
	}
	switch v.chip {
	case ChipAlerts:
		return "No alert: everything is within its limits."
	case ChipUpdates:
		if v.snap != nil && v.snap.Updates.Report.EnginesAt.IsZero() && v.snap.Updates.Report.ImagesAt.IsZero() {
			return "No check yet: the first one runs 30 s after startup. Press U to check now."
		}
		return "No update waiting. U checks now."
	case ChipJobs:
		return "No jobs yet. Start, stop or restart a machine, create one (n) or import a file (i)."
	case ChipHistory:
		return "Nothing has happened yet."
	}
	return "Nothing is happening. Alerts, jobs, updates and what happens on your engines show up here."
}

// chips is the filter row: the active one is highlighted.
func (v *View) chips(s *theme.Styles, inner int) string {
	var parts []string
	for c := ChipAll; int(c) < len(chipNames); c++ {
		text := c.String()
		if c != ChipAll {
			text += fmt.Sprintf(" %d", v.count(c))
		}
		if c == v.chip {
			parts = append(parts, s.Accent.Bold(true).Render("["+text+"]"))
		} else {
			parts = append(parts, s.Muted.Render(" "+text+" "))
		}
	}
	return ansi.Truncate(strings.Join(parts, ""), inner, "…")
}

// glyph is the icon of an entry.
func glyph(e Entry) string {
	switch e.Kind {
	case KindAlert:
		return "!"
	case KindUpdate:
		if e.Engine == nil && e.Image == nil {
			return "!" // the check itself failed
		}
		return "⬆"
	case KindEvent:
		return "·"
	}
	switch e.Job.State {
	case store.JobQueued:
		return "◌"
	case store.JobRunning:
		return "●"
	case store.JobSucceeded:
		return "✓"
	case store.JobFailed:
		return "✖"
	}
	return "⊘"
}

const (
	titleW  = 24
	sourceW = 14
	whenW   = 7
)

func (v *View) renderEntry(s *theme.Styles, e Entry, selected bool, inner int, now time.Time) string {
	wide := inner >= 72
	textW := inner - 2 - 2 - titleW - 1 - 1 - whenW
	if wide {
		textW -= sourceW + 1
	}
	text := e.Text
	if e.Job != nil && e.Job.State == store.JobRunning {
		text = progressText(e.Job.Progress, textW)
	}
	if e.Hidden != "" {
		text = e.Hidden + " · " + text
	}
	text = ansi.Truncate(text, max(4, textW), "…")
	title := pad(ansi.Truncate(e.Title, titleW, "…"), titleW)
	when := padLeft(since(e.When, now), whenW)
	source := ""
	if wide {
		source = " " + pad(e.Source, sourceW)
	}
	plain := glyph(e) + " " + title + " " + pad(text, max(4, textW)) + source + " " + when
	if selected {
		return s.Selected.Render(pad("▶ "+plain, inner))
	}
	lvl := e.Level
	textStyle := s.Muted
	if e.Hidden != "" {
		return s.Muted.Render(ansi.Truncate("  "+plain, inner, "…"))
	}
	if e.Kind != KindEvent && e.Level != theme.LevelOK && e.Level != theme.LevelInfo {
		textStyle = s.LevelText(lvl)
	}
	return "  " + s.LevelText(lvl).Render(glyph(e)) + " " + s.Text.Render(title) + " " + textStyle.Render(pad(text, max(4, textW))) +
		s.Muted.Render(source+" "+when)
}

// footer says more about the selected line: the error in full, the upgrade
// command, the machines an image update touches.
func (v *View) footer(s *theme.Styles, inner int) []string {
	e, ok := v.Selected()
	if !ok {
		return nil
	}
	var detail []string
	add := func(label, val string) {
		if val != "" {
			detail = append(detail, s.Muted.Render(pad(label, 10))+s.Text.Render(ansi.Truncate(textsafe.Clean(val), inner-10, "…")))
		}
	}
	switch {
	case e.Job != nil && e.Job.State == store.JobFailed:
		add("failed", e.Job.Err)
	case e.Engine != nil:
		add("installed", e.Engine.Installed)
		add("upgrade", e.Engine.Hint)
		add("notes", e.Engine.NotesURL)
	case e.Image != nil:
		add("used by", strings.Join(e.Image.Names, ", "))
	case e.Alert != nil:
		add("since", e.Alert.Since.In(time.Local).Format("15:04:05"))
	}
	if len(detail) == 0 {
		return []string{""}
	}
	return append([]string{""}, detail[:min(2, len(detail))]...)
}

// since is "now", "5m", "3h" or the date, how long ago t was.
func since(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour && sameDay(t, now):
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case sameDay(t, now):
		return t.In(time.Local).Format("15:04")
	}
	return t.In(time.Local).Format("Jan 2")
}

// progressText is "pulling ▕█████░░░░░▏ 45%", or the phase alone when the
// total is unknown.
func progressText(p engine.Progress, w int) string {
	if p.Phase == "" {
		p.Phase = "working"
	}
	if p.Total <= 0 {
		return p.Phase + "…"
	}
	frac := float64(min(max(p.Current, 0), p.Total)) / float64(p.Total)
	text := fmt.Sprintf(" %3d%%", int(frac*100))
	barW := min(20, w-ansi.StringWidth(p.Phase)-len(text)-3)
	if barW < 4 {
		return p.Phase + text
	}
	full := int(frac * float64(barW))
	return p.Phase + " ▕" + strings.Repeat("█", full) + strings.Repeat("░", barW-full) + "▏" + text
}

func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

func padLeft(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return strings.Repeat(" ", max(0, w-ansi.StringWidth(s))) + s
}
