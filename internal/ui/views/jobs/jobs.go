// Package jobs is the Jobs view (5): lifecycle operations in flight and
// their history, newest first, with cancellation of queued and running jobs.
package jobs

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/components/panel"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
)

// CancelMsg asks the app to cancel a job.
type CancelMsg struct{ ID uint64 }

// View is the Jobs view.
type View struct {
	keys   *keys.Map
	jobs   []store.Job // newest first
	cursor int
	sel    uint64 // job under the cursor once the user moved; survives updates
	pinned bool   // the user moved: stay on sel instead of the newest job
	offset int
	page   int
}

var _ views.View = (*View)(nil)

// New returns the view.
func New(km *keys.Map) *View { return &View{keys: km, page: 20} }

// Title implements views.View.
func (v *View) Title() string { return "Jobs" }

// Scope implements views.View.
func (v *View) Scope() keys.Scope { return keys.Jobs }

// ShowsInstances implements views.View.
func (v *View) ShowsInstances() bool { return false }

// Panes implements views.View.
func (v *View) Panes() int { return 1 }

// Selected returns the job under the cursor.
func (v *View) Selected() (store.Job, bool) {
	if v.cursor < 0 || v.cursor >= len(v.jobs) {
		return store.Job{}, false
	}
	return v.jobs[v.cursor], true
}

// Update implements views.View.
func (v *View) Update(msg tea.Msg) (views.View, tea.Cmd) {
	switch msg := msg.(type) {
	case views.DataMsg:
		v.jobs = msg.Snapshot.Jobs()
		v.cursor = 0
		for i, j := range v.jobs {
			if v.pinned && j.ID == v.sel {
				v.cursor = i
				break
			}
		}
		v.move(0)
	case views.ResizeMsg:
		v.page = max(1, msg.Height-5)
	}
	return v, nil
}

func (v *View) move(delta int) {
	if len(v.jobs) == 0 {
		v.cursor, v.sel = 0, 0
		return
	}
	v.cursor = max(0, min(len(v.jobs)-1, v.cursor+delta))
	v.sel = v.jobs[v.cursor].ID
	switch {
	case v.cursor < v.offset:
		v.offset = v.cursor
	case v.cursor >= v.offset+v.page:
		v.offset = v.cursor - v.page + 1
	}
}

// HandleKey implements views.View.
func (v *View) HandleKey(msg tea.KeyPressMsg, _ int) (views.View, tea.Cmd, bool) {
	switch v.keys.Action(msg, keys.Jobs) {
	case "up":
		v.move(-1)
		v.pinned = v.cursor > 0
	case "down":
		v.move(1)
		v.pinned = true
	case "home":
		v.move(-len(v.jobs))
		v.pinned = false
	case "end":
		v.move(len(v.jobs))
		v.pinned = true
	case "cancel":
		j, ok := v.Selected()
		if !ok || j.State.Final() {
			return v, nil, true
		}
		id := j.ID
		return v, func() tea.Msg { return CancelMsg{ID: id} }, true
	default:
		return v, nil, false
	}
	return v, nil, true
}

// Hints implements views.View.
func (v *View) Hints(km *keys.Map) []key.Binding {
	return []key.Binding{km.Binding(keys.Jobs, "up"), km.Binding(keys.Jobs, "down"), km.Binding(keys.Jobs, "cancel")}
}

func stateCell(j store.Job) (string, theme.Level) {
	switch j.State {
	case store.JobQueued:
		return "◌ queued", theme.LevelInfo
	case store.JobRunning:
		return "● running", theme.LevelWarn
	case store.JobSucceeded:
		return "✓ done", theme.LevelOK
	case store.JobFailed:
		return "✖ failed", theme.LevelErr
	}
	return "⊘ canceled", theme.LevelInfo
}

func duration(j store.Job, now time.Time) string {
	start, end := j.Started, j.Ended
	if start.IsZero() {
		return "—"
	}
	if end.IsZero() {
		end = now
	}
	d := end.Sub(start)
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Round(100 * time.Millisecond).String()
}

// Render implements views.View.
func (v *View) Render(ctx views.Context) string {
	s := ctx.Styles
	inner := ctx.Width - 4
	running := 0
	for _, j := range v.jobs {
		if !j.State.Final() {
			running++
		}
	}
	errW := max(10, inner-2-12-1-28-1-6-1-8-1-8-1)
	lines := []string{s.TableHeader.Render(pad("  STATE", 14) + " " + pad("JOB", 28) + " " + pad("ENGINE", 6) + " " + pad("QUEUED", 8) + " " + padLeft("TOOK", 8) + " " + "ERROR")}
	if len(v.jobs) == 0 {
		lines = append(lines, "", s.Muted.Render("No jobs yet. Start, stop, restart, pause or delete an instance from the Table (3)."))
	}
	end := min(len(v.jobs), v.offset+v.page)
	for i := v.offset; i < end; i++ {
		j := v.jobs[i]
		st, lvl := stateCell(j)
		queued := j.Queued.In(time.Local).Format("15:04:05")
		cells := pad(st, 12) + " " + pad(j.Title(), 28) + " " + pad(j.Key.Engine().Short(), 6) + " " + pad(queued, 8) + " " + padLeft(duration(j, ctx.Now), 8) + " "
		errText := ansi.Truncate(j.Err, errW, "…")
		if i == v.cursor {
			lines = append(lines, s.Selected.Render(pad("▶ "+cells+errText, inner)))
			continue
		}
		lines = append(lines, "  "+s.LevelText(lvl).Render(pad(st, 12))+" "+s.Text.Render(pad(j.Title(), 28))+" "+
			s.Muted.Render(pad(j.Key.Engine().Short(), 6)+" "+pad(queued, 8)+" "+padLeft(duration(j, ctx.Now), 8))+" "+
			s.LevelText(theme.LevelErr).Render(errText))
	}
	title := fmt.Sprintf("Jobs · %d in progress · %d in history", running, len(v.jobs))
	return panel.Render(s, title, layout.Fit(strings.Join(lines, "\n"), inner, ctx.Height-2), ctx.Width, ctx.Height, ctx.Focus == 0)
}

func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

func padLeft(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return strings.Repeat(" ", max(0, w-ansi.StringWidth(s))) + s
}
