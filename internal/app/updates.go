package app

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
	updatesview "github.com/blokxz/viewDI/internal/ui/views/updates"
	"github.com/blokxz/viewDI/internal/updates"
)

// Updater is the update checker as the UI sees it; checker.Checker
// implements it and main wires it. Nil means checks are off.
type Updater interface {
	Reports() <-chan updates.Report
	CheckNow()
}

// UpdatesMsg carries a checker report.
type UpdatesMsg struct{ Report updates.Report }

// notesOpenedMsg reports whether the release notes opened in a browser.
type notesOpenedMsg struct {
	url string
	err error
}

// followUpdates waits for the next report in its own goroutine; Update
// re-arms it after each one. A closed channel ends the loop.
func followUpdates(ctx context.Context, u Updater) tea.Cmd {
	ch := u.Reports()
	return func() tea.Msg {
		select {
		case r, ok := <-ch:
			if !ok {
				return nil
			}
			return UpdatesMsg{Report: r}
		case <-ctx.Done():
			return nil
		}
	}
}

// applyUpdates stores a report and toasts what is new and visible: a
// report from the cache or one repeating known updates toasts nothing.
func (m Model) applyUpdates(r updates.Report) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{followUpdates(m.opts.Context, m.opts.Updates)}
	m.store.ApplyUpdates(r, m.now)
	m.store.Evaluate(m.now)
	m.broadcast(views.DataMsg{Snapshot: m.store.Snapshot()})

	dec := m.store.Decisions()
	var found []string
	level := theme.LevelInfo
	images := 0
	for _, f := range r.New {
		if !dec.Visible(f.Key, f.Version, m.now) {
			continue
		}
		if strings.HasPrefix(f.Key, "image/") {
			images++
			continue
		}
		found = append(found, textsafe.Clean(f.Text))
		if f.Level == updates.LevelSecurity {
			level = theme.LevelWarn
		}
	}
	switch images {
	case 0:
	case 1:
		found = append(found, "1 image")
	default:
		found = append(found, fmt.Sprintf("%d images", images))
	}
	if len(found) > 0 {
		cmds = append(cmds, toast.Show(level, "Updates available: "+strings.Join(found, ", ")+" · 6 for details"))
	}
	return m, tea.Batch(cmds...)
}

// checkUpdates handles U.
func (m Model) checkUpdates() (tea.Model, tea.Cmd) {
	u := m.opts.Updates
	if u == nil {
		return m, toast.Show(theme.LevelInfo, "Update checks are off (updates.enabled = false)")
	}
	return m, tea.Batch(func() tea.Msg { u.CheckNow(); return nil }, toast.Show(theme.LevelInfo, "Checking for updates…"))
}

// decide applies a snooze or ignore toggle from the Updates view and
// persists it in state.json.
func (m Model) decide(msg tea.Msg) (tea.Model, tea.Cmd) {
	dec := m.store.Decisions()
	var text string
	switch msg := msg.(type) {
	case updatesview.SnoozeMsg:
		dec = dec.Snooze(msg.Key, m.now)
		if s, _ := dec.Hidden(msg.Key, "", m.now); s {
			text = "Snoozed for 7 days: " + msg.Text
		} else {
			text = "No longer snoozed: " + msg.Text
		}
	case updatesview.IgnoreMsg:
		dec = dec.Ignore(msg.Key, msg.Version)
		if _, i := dec.Hidden(msg.Key, msg.Version, m.now); i {
			text = "Ignoring this version: " + msg.Text
		} else {
			text = "No longer ignored: " + msg.Text
		}
	}
	m.store.SetDecisions(dec, m.now)
	m.store.Evaluate(m.now)
	m.state.Updates = dec
	m.broadcast(views.DataMsg{Snapshot: m.store.Snapshot()})
	return m, tea.Batch(m.saveState(), toast.Show(theme.LevelOK, textsafe.Clean(text)))
}

// openNotes opens release notes with Options.OpenURL in a command; without
// one, or when it fails, a toast shows the URL instead.
func (m Model) openNotes(url string) (tea.Model, tea.Cmd) {
	open := m.opts.OpenURL
	if open == nil {
		return m, toast.Show(theme.LevelInfo, "Release notes: "+url)
	}
	return m, func() tea.Msg { return notesOpenedMsg{url: url, err: open(url)} }
}
