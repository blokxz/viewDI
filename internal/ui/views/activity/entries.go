package activity

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/blokxz/viewDI/internal/alerts"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/theme"
	upd "github.com/blokxz/viewDI/internal/updates"
)

// Kind is what an entry is about.
type Kind int

// Entry kinds.
const (
	KindAlert  Kind = iota // something is wrong now
	KindJob                // something viewDI is doing, or did
	KindUpdate             // a newer engine or image exists
	KindEvent              // something that happened on a daemon outside viewDI
)

// Section groups entries by how urgent they are.
type Section int

// Sections, in display order.
const (
	SecNow Section = iota
	SecToday
	SecEarlier
)

func (s Section) String() string { return [...]string{"NOW", "TODAY", "EARLIER"}[s] }

// Entry is one line of the feed.
type Entry struct {
	ID      string
	Kind    Kind
	Section Section
	When    time.Time
	Level   theme.Level
	Title   string // what it is about: a machine, an engine, an image
	Text    string // what happened, in plain words
	Source  string // who did it: viewDI, the engine, "outside viewDI", "alert", "update"
	Hidden  string // "snoozed" or "ignored" for a hidden update

	Job    *store.Job
	Alert  *alerts.Alert
	Engine *upd.EngineStatus
	Image  *upd.ImageUpdate
	// Machine is the machine the entry is about, when there is one.
	Machine engine.Key
}

// eventWindow is how long after a job an event of the same machine is
// taken as the job's own doing and not listed again.
const eventWindow = 15 * time.Second

// Build lists the feed at now: active alerts, jobs in flight and available
// updates under NOW; finished jobs, hidden updates and what happened on the
// daemons outside viewDI under TODAY and EARLIER, newest first.
func Build(snap *store.Snapshot, now time.Time) []Entry {
	if snap == nil {
		return nil
	}
	var out []Entry

	for i := range snap.Alerts {
		a := snap.Alerts[i]
		if a.Rule == alerts.RuleEngineUpdate || a.Rule == alerts.RuleImageUpdates {
			continue // the update entries say it better and carry the actions
		}
		out = append(out, Entry{
			ID: "alert/" + a.ID, Kind: KindAlert, Section: SecNow, When: a.Since, Source: "alert",
			Level: alertLevel(a.Severity), Title: textsafe.Clean(a.Subject), Text: textsafe.Clean(a.Text),
			Alert: &a, Machine: a.Key,
		})
	}

	jobs := snap.Jobs()
	for i := range jobs {
		j := jobs[i]
		e := Entry{
			ID: jobID(j), Kind: KindJob, When: jobTime(j), Source: "viewDI", Job: &j,
			Title: textsafe.Clean(j.Title()), Level: jobLevel(j),
		}
		if j.Lifecycle {
			e.Machine = j.Key
		}
		e.Text = jobText(j)
		e.Section = sectionFor(e.When, now, !j.State.Final())
		out = append(out, e)
	}

	out = append(out, updateEntries(snap, now)...)

	for _, ev := range snap.Events {
		if covered(ev, jobs, now) {
			continue
		}
		verb, lvl := eventText(ev)
		out = append(out, Entry{
			ID:   "event/" + string(ev.Key()) + "/" + ev.Time.Format(time.RFC3339Nano) + "/" + ev.Action,
			Kind: KindEvent, Section: sectionFor(ev.Time, now, false), When: ev.Time, Level: lvl,
			Title: textsafe.Clean(ev.Name), Text: verb, Source: "outside viewDI", Machine: ev.Key(),
		})
	}

	slices.SortStableFunc(out, func(a, b Entry) int {
		if c := cmp.Compare(a.Section, b.Section); c != 0 {
			return c
		}
		if a.Section == SecNow {
			// What is wrong first, then what is running, then what could be better.
			if c := cmp.Compare(rankNow(a), rankNow(b)); c != 0 {
				return c
			}
		}
		return cmp.Or(b.When.Compare(a.When), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// rankNow orders the NOW section: errors, warnings, running jobs, security
// updates, engine updates, image updates.
func rankNow(e Entry) int {
	switch {
	case e.Kind == KindAlert && e.Level == theme.LevelErr:
		return 0
	case e.Kind == KindAlert:
		return 1
	case e.Kind == KindJob:
		return 2
	case e.Level == theme.LevelErr: // a security update
		return 3
	case e.Engine != nil:
		return 4
	}
	return 5
}

func sectionFor(t, now time.Time, active bool) Section {
	switch {
	case active:
		return SecNow
	case sameDay(t, now):
		return SecToday
	}
	return SecEarlier
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.In(time.Local).Date()
	by, bm, bd := b.In(time.Local).Date()
	return ay == by && am == bm && ad == bd
}

func alertLevel(s alerts.Severity) theme.Level {
	switch s {
	case alerts.Error:
		return theme.LevelErr
	case alerts.Warn:
		return theme.LevelWarn
	}
	return theme.LevelInfo
}

func jobID(j store.Job) string { return "job/" + strconv.FormatUint(j.ID, 10) }

// jobTime is when the job last changed: when it ended, else when it began.
func jobTime(j store.Job) time.Time {
	switch {
	case !j.Ended.IsZero():
		return j.Ended
	case !j.Started.IsZero():
		return j.Started
	}
	return j.Queued
}

func jobLevel(j store.Job) theme.Level {
	switch j.State {
	case store.JobFailed:
		return theme.LevelErr
	case store.JobSucceeded:
		return theme.LevelOK
	case store.JobRunning:
		return theme.LevelWarn
	}
	return theme.LevelInfo
}

// jobText is the plain-words state of a job; running jobs get their
// progress drawn by the view.
func jobText(j store.Job) string {
	switch j.State {
	case store.JobQueued:
		return "waiting its turn"
	case store.JobRunning:
		return strings.TrimSpace(j.Progress.Phase)
	case store.JobSucceeded:
		if j.Result != "" {
			return "done → " + shortResult(textsafe.Clean(j.Result))
		}
		return "done"
	case store.JobFailed:
		return "failed: " + textsafe.Clean(j.Err)
	}
	return "canceled"
}

// shortResult shortens a full container ID to its usual 12 characters.
func shortResult(r string) string {
	if len(r) == 64 && strings.Trim(r, "0123456789abcdef") == "" {
		return r[:12]
	}
	return r
}

// covered reports whether a job explains the event: the same machine,
// around the job's lifetime.
func covered(ev engine.Event, jobs []store.Job, now time.Time) bool {
	for _, j := range jobs {
		// By key, or by name: a recreate leaves the old key behind.
		if j.Key.Engine() != ev.Engine || j.Key != ev.Key() && j.Name != ev.Name {
			continue
		}
		from := j.Queued.Add(-2 * time.Second)
		to := now
		if !j.Ended.IsZero() {
			to = j.Ended.Add(eventWindow)
		}
		if !ev.Time.Before(from) && !ev.Time.After(to) {
			return true
		}
	}
	return false
}

// eventText says what a daemon event means for the user.
func eventText(ev engine.Event) (string, theme.Level) {
	if ev.Kind == engine.EventRemoved {
		return "was removed", theme.LevelInfo
	}
	switch ev.Action {
	case "start", "instance-started":
		return "started", theme.LevelInfo
	case "stop", "instance-stopped":
		return "stopped", theme.LevelInfo
	case "restart", "instance-restarted":
		return "restarted", theme.LevelInfo
	case "die":
		return "exited", theme.LevelWarn
	case "oom":
		return "ran out of memory", theme.LevelErr
	case "kill":
		return "was killed", theme.LevelWarn
	case "create", "instance-created":
		return "was created", theme.LevelInfo
	case "pause", "instance-paused":
		return "paused", theme.LevelInfo
	case "unpause", "instance-resumed":
		return "resumed", theme.LevelInfo
	}
	return textsafe.Clean(ev.Action), theme.LevelInfo
}

// updateEntries lists the engine and image updates the checker found.
// Snoozed and ignored ones stay in the feed, marked, under EARLIER.
func updateEntries(snap *store.Snapshot, now time.Time) []Entry {
	var out []Entry
	d := snap.Updates.Decisions
	r := snap.Updates.Report
	hide := func(e *Entry, key, version string) {
		e.Section = SecNow
		snoozed, ignored := d.Hidden(key, version, now)
		switch {
		case ignored:
			e.Hidden, e.Section = "ignored", SecEarlier
		case snoozed:
			e.Hidden, e.Section = "snoozed", SecEarlier
		}
	}
	if r.Failed != "" {
		out = append(out, Entry{
			ID: "update/failed", Kind: KindUpdate, Section: SecNow, When: cmpTime(r.EnginesAt, r.ImagesAt), Source: "update",
			Level: theme.LevelWarn, Title: "Update check", Text: "last check failed: " + textsafe.Clean(r.Failed),
		})
	}
	for i := range r.Engines {
		st := r.Engines[i]
		if !st.HasUpdate() {
			continue
		}
		e := Entry{
			ID: "update/" + st.Key(), Kind: KindUpdate, When: r.EnginesAt, Source: "update", Engine: &st,
			Level: theme.LevelWarn, Title: st.Engine.Label(), Text: "version " + textsafe.Clean(st.Available) + " is available",
		}
		if st.Level == upd.LevelSecurity {
			e.Level, e.Text = theme.LevelErr, "version "+textsafe.Clean(st.Available)+" is available (security)"
		}
		hide(&e, st.Key(), st.Available)
		out = append(out, e)
	}
	for i := range r.Images {
		img := r.Images[i]
		e := Entry{
			ID: "update/" + img.Key(), Kind: KindUpdate, When: r.ImagesAt, Source: "update", Image: &img,
			Level: theme.LevelWarn, Title: textsafe.Clean(img.Check.Ref), Text: imageText(img),
		}
		hide(&e, img.Key(), img.Available)
		out = append(out, e)
	}
	return out
}

func cmpTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func imageText(img upd.ImageUpdate) string {
	used := textsafe.Clean(strings.Join(img.Names, ", "))
	switch {
	case img.Pulled:
		return "pulled, recreate " + used + " to use it"
	case used != "":
		return "newer build for " + used
	}
	return "newer build available"
}
