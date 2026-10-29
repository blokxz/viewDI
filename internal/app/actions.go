package app

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/jobs"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views/table"
)

// JobRunner runs lifecycle operations in the background; *jobs.Runner
// implements it.
type JobRunner interface {
	Submit(req jobs.Request) jobs.ID
	Cancel(id jobs.ID) bool
	Next(ctx context.Context) ([]jobs.Job, bool)
}

// JobsMsg carries job state changes from the runner.
type JobsMsg struct{ Jobs []jobs.Job }

// actionConfirmedMsg carries a bulk action the user confirmed.
type actionConfirmedMsg struct {
	action  engine.Action
	targets []engine.Instance
}

// batch tracks the jobs of one user action, for a single summary toast.
type batch struct {
	verb                string
	total, done, failed int
}

func followJobs(ctx context.Context, r JobRunner) tea.Cmd {
	return func() tea.Msg {
		js, ok := r.Next(ctx)
		if !ok {
			return nil
		}
		return JobsMsg{Jobs: js}
	}
}

// actionTargets returns the instances an action applies to: the marked
// rows of the Table view, else the instance under the cursor.
func (m Model) actionTargets() []engine.Instance {
	if t, ok := m.views[m.active].(*table.View); ok && m.detail == nil {
		if marked := t.Marked(); len(marked) > 0 {
			return marked
		}
	}
	if in, ok := m.selected(); ok {
		return []engine.Instance{in}
	}
	return nil
}

var actionByKey = map[string]engine.Action{
	keys.ActStart: engine.Start, keys.ActStop: engine.Stop, keys.ActRestart: engine.Restart, keys.ActPause: engine.Pause,
}

func (m Model) instanceAction(action string) (tea.Model, tea.Cmd) {
	if m.opts.ReadOnly {
		return m, toast.Show(theme.LevelWarn, "Read-only mode: instance actions are disabled")
	}
	targets := m.actionTargets()
	if len(targets) == 0 {
		return m, toast.Show(theme.LevelInfo, "No instance selected")
	}
	snap := m.store.Snapshot()
	var keep []engine.Instance
	var skipped []string
	for _, in := range targets {
		caps := snap.Engine(in.Engine).Info.Capabilities
		_, busy := snap.Pending(in.Key())
		switch {
		case caps.Restricted:
			skipped = append(skipped, fmt.Sprintf("%s (%s: %s)", in.Name, in.Engine.Label(), caps.Reason))
		case busy:
			p, _ := snap.Pending(in.Key())
			skipped = append(skipped, in.Name+" (already "+p.Label()+")")
		default:
			keep = append(keep, in)
		}
	}
	var cmds []tea.Cmd
	if len(skipped) > 0 {
		cmds = append(cmds, toast.Show(theme.LevelWarn, "Skipped "+strings.Join(skipped, ", ")))
	}
	if len(keep) == 0 {
		return m, tea.Batch(cmds...)
	}
	if m.opts.Jobs == nil {
		return m, tea.Batch(append(cmds, toast.Show(theme.LevelWarn, "Instance actions need a connected engine"))...)
	}
	if action == keys.ActDelete {
		return m, tea.Batch(append(cmds, modal.Open(modal.NewDelete(m.keys, keep)))...)
	}
	act := actionByKey[action]
	if len(keep) == 1 {
		model, cmd := m.submitActions(act, keep)
		return model, tea.Batch(append(cmds, cmd)...)
	}
	verb := pauseVerb(act, keep)
	names := make([]string, 0, len(keep))
	for i, in := range keep {
		if i == 10 {
			names = append(names, fmt.Sprintf("… and %d more", len(keep)-i))
			break
		}
		names = append(names, "• "+in.Name+" ("+in.Engine.Short()+")")
	}
	title := fmt.Sprintf("%s %d instances?", capital(verb), len(keep))
	dialog := modal.NewConfirm(m.keys, title, strings.Join(names, "\n"), capital(verb), act != engine.Start,
		actionConfirmedMsg{action: act, targets: keep})
	return m, tea.Batch(append(cmds, modal.Open(dialog))...)
}

// pauseVerb names the F toggle: "pause", "resume" or "pause / resume".
func pauseVerb(act engine.Action, targets []engine.Instance) string {
	if act != engine.Pause {
		return string(act)
	}
	paused := 0
	for _, in := range targets {
		if in.State == engine.StatePaused {
			paused++
		}
	}
	switch paused {
	case 0:
		return "pause"
	case len(targets):
		return "resume"
	}
	return "pause / resume"
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// submitActions starts one job per target. Pause toggles: paused targets
// are resumed.
func (m Model) submitActions(act engine.Action, targets []engine.Instance) (tea.Model, tea.Cmd) {
	reqs := make([]jobs.Request, 0, len(targets))
	for _, in := range targets {
		a := act
		if act == engine.Pause && in.State == engine.StatePaused {
			a = engine.Unpause
		}
		reqs = append(reqs, jobs.Request{Key: in.Key(), Name: in.Name, Action: a, Options: engine.ActionOptions{Timeout: m.opts.StopTimeout}})
	}
	return m.submit(pauseVerb(act, targets), reqs)
}

func (m Model) submitDelete(msg modal.DeleteConfirmedMsg) (tea.Model, tea.Cmd) {
	reqs := make([]jobs.Request, 0, len(msg.Targets))
	for _, in := range msg.Targets {
		reqs = append(reqs, jobs.Request{Key: in.Key(), Name: in.Name, Delete: true, DeleteOptions: msg.Options})
	}
	return m.submit("delete", reqs)
}

// submit hands the requests to the runner in a command (Submit spawns the
// job goroutines) and marks the instances pending right away.
func (m Model) submit(verb string, reqs []jobs.Request) (tea.Model, tea.Cmd) {
	if m.opts.Jobs == nil || len(reqs) == 0 {
		return m, nil
	}
	m.nextBatch++
	id := m.nextBatch
	m.batches[id] = &batch{verb: verb, total: len(reqs)}
	for i := range reqs {
		reqs[i].Batch = id
	}
	runner := m.opts.Jobs
	return m, func() tea.Msg {
		for _, r := range reqs {
			runner.Submit(r)
		}
		return nil
	}
}

// applyJobs records job changes in the store and announces finished ones.
func (m Model) applyJobs(js []jobs.Job) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{followJobs(m.opts.Context, m.opts.Jobs)}
	changes := make([]store.Change, 0, len(js))
	for _, j := range js {
		changes = append(changes, store.JobUpdated{Job: storeJob(j)})
		if !j.State.Final() {
			continue
		}
		b, ok := m.batches[j.Batch]
		if !ok {
			continue
		}
		b.done++
		switch j.State {
		case jobs.Failed:
			b.failed++
			cmds = append(cmds, toast.Show(theme.LevelErr, j.Title()+" failed: "+j.Err))
		case jobs.Canceled:
			b.failed++
		}
		if b.done < b.total {
			continue
		}
		delete(m.batches, j.Batch)
		switch {
		case b.total == 1 && j.State == jobs.Succeeded && !j.Lifecycle():
			cmds = append(cmds, toast.Show(theme.LevelOK, resultText(j)))
		case b.total == 1 && j.State == jobs.Succeeded:
			cmds = append(cmds, toast.Show(theme.LevelOK, capital(j.Verb())+" "+j.Name+": done"))
		case b.total > 1:
			lvl := theme.LevelOK
			if b.failed > 0 {
				lvl = theme.LevelWarn
			}
			cmds = append(cmds, toast.Show(lvl, fmt.Sprintf("%s: %d of %d done", capital(b.verb), b.total-b.failed, b.total)))
		}
	}
	if m.store.Apply(changes...) {
		m.broadcast(m.dataMsg())
	}
	return m, tea.Batch(cmds...)
}

// resultText announces what a create or import made.
func resultText(j jobs.Job) string {
	switch {
	case j.Create != nil:
		name := j.Name
		if name == "" {
			name = shortID(j.Result)
		}
		return "Created " + name + " (" + j.Key.Engine().Label() + ")"
	case j.Import != nil && j.Import.Kind == engine.ImportIncusBackup:
		return "Restored " + textsafe.Clean(j.Result) + " from " + j.Name
	}
	return "Imported " + j.Name + " → " + textsafe.Clean(j.Result)
}

// shortID shortens a Docker container ID for messages.
func shortID(id string) string { return id[:min(12, len(id))] }

func storeJob(j jobs.Job) store.Job {
	out := store.Job{
		ID: uint64(j.ID), Key: j.Key, Name: j.Name, Verb: j.Verb(), Batch: j.Batch, Lifecycle: j.Lifecycle(),
		State: store.JobState(j.State), Err: j.Err, Class: j.Class, Progress: j.Progress, Result: j.Result,
		Queued: j.Queued, Started: j.Started, Ended: j.Ended,
	}
	if !j.Delete && j.Lifecycle() {
		out.Action = j.Action
	}
	return out
}

// unconfirmed warns about operations the engine never confirmed and
// resyncs the engines, whose event stream may have stalled.
func (m Model) unconfirmed() tea.Cmd {
	un := m.store.TakeUnconfirmed()
	if len(un) == 0 {
		return nil
	}
	names := make([]string, len(un))
	for i, j := range un {
		names[i] = j.Title()
	}
	cmds := []tea.Cmd{toast.Show(theme.LevelWarn, "No confirmation from the engine for "+strings.Join(names, ", ")+" — resyncing")}
	if src := m.opts.Source; src != nil {
		cmds = append(cmds, func() tea.Msg { src.Reconnect(); return nil })
	}
	return tea.Batch(cmds...)
}
