package store

import (
	"maps"
	"slices"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

// JobState is a background job's state.
type JobState int

// Job states.
const (
	JobQueued JobState = iota
	JobRunning
	JobSucceeded
	JobFailed
	JobCanceled
)

func (s JobState) String() string {
	return [...]string{"queued", "running", "succeeded", "failed", "canceled"}[s]
}

// Final reports whether the job is over.
func (s JobState) Final() bool { return s >= JobSucceeded }

// Job is one lifecycle operation, as the UI shows it.
type Job struct {
	ID      uint64
	Key     engine.Key
	Name    string
	Verb    string        // "stop", "delete"…
	Action  engine.Action // "" for delete
	Batch   uint64
	State   JobState
	Err     string
	Class   engine.Class
	Queued  time.Time
	Started time.Time
	Ended   time.Time
}

// Title is "stop web-api".
func (j Job) Title() string { return j.Verb + " " + j.Name }

// JobUpdated reports a job's new state.
type JobUpdated struct{ Job Job }

func (JobUpdated) isChange() {}

// Pending marks an instance with an operation in flight: the UI shows it
// instead of the last known state until the daemon confirms the result.
type Pending struct {
	Job      uint64
	Verb     string
	Action   engine.Action // "" for delete
	Since    time.Time
	Started  time.Time // the instance's start time when the job began (restart)
	Done     bool      // the job succeeded; waiting for the engine to confirm
	Deadline time.Time // when Done, give up waiting at this time
}

// Label is the row text: "stopping", "deleting".
func (p Pending) Label() string {
	if p.Action == "" {
		return "deleting"
	}
	return p.Action.Progressive()
}

// Confirmation bounds and history size.
const (
	ConfirmTimeout = 10 * time.Second
	maxJobs        = 200
)

type jobState struct {
	jobs        map[uint64]Job
	pending     map[engine.Key]Pending
	unconfirmed []Job
}

func newJobState() *jobState {
	return &jobState{jobs: map[uint64]Job{}, pending: map[engine.Key]Pending{}}
}

func (s *Store) applyJob(c JobUpdated) bool {
	j := c.Job
	js := s.jobs
	js.jobs[j.ID] = j
	if len(js.jobs) > maxJobs {
		ids := slices.Sorted(maps.Keys(js.jobs))
		for _, id := range ids[:len(ids)-maxJobs] {
			if js.jobs[id].State.Final() {
				delete(js.jobs, id)
			}
		}
	}
	p, ok := js.pending[j.Key]
	switch {
	case !j.State.Final():
		if !ok || p.Job != j.ID {
			p = Pending{Job: j.ID, Verb: j.Verb, Action: j.Action, Since: j.Queued}
			if in, found := s.lookup(j.Key); found {
				p.Started = in.Started
			}
			js.pending[j.Key] = p
		}
	case !ok || p.Job != j.ID:
	case j.State == JobSucceeded:
		p.Done, p.Deadline = true, j.Ended.Add(ConfirmTimeout)
		js.pending[j.Key] = p
		s.confirm(j.Key)
	default:
		delete(js.pending, j.Key) // failed or canceled: back to the real state
	}
	return true
}

func (s *Store) lookup(k engine.Key) (engine.Instance, bool) {
	e, ok := s.engines[k.Engine()]
	if !ok {
		return engine.Instance{}, false
	}
	in, ok := e.instances[k]
	return in, ok
}

// confirm clears a done pending mark once the store shows its result.
func (s *Store) confirm(k engine.Key) {
	p, ok := s.jobs.pending[k]
	if !ok || !p.Done {
		return
	}
	in, exists := s.lookup(k)
	switch {
	case p.Action == "":
		if exists {
			return
		}
	case !exists:
	case in.State != p.Action.Expected():
		return
	case p.Action == engine.Restart && !in.Started.After(p.Started):
		return
	}
	delete(s.jobs.pending, k)
}

// confirmAll re-checks every done pending mark after instance changes.
func (s *Store) confirmAll() {
	for k := range s.jobs.pending {
		s.confirm(k)
	}
}

// expirePending drops done marks the engine never confirmed and keeps
// their jobs for TakeUnconfirmed.
func (s *Store) expirePending(now time.Time) bool {
	changed := false
	for k, p := range s.jobs.pending {
		if p.Done && now.After(p.Deadline) {
			delete(s.jobs.pending, k)
			s.jobs.unconfirmed = append(s.jobs.unconfirmed, s.jobs.jobs[p.Job])
			changed = true
		}
	}
	return changed
}

// TakeUnconfirmed returns (and forgets) the jobs that succeeded but whose
// result the engine did not confirm in time: the app warns and resyncs.
func (s *Store) TakeUnconfirmed() []Job {
	out := s.jobs.unconfirmed
	s.jobs.unconfirmed = nil
	return out
}

// HasPending reports whether any operation awaits confirmation (the app
// then ticks every second to expire them on time).
func (s *Store) HasPending() bool { return len(s.jobs.pending) > 0 }

// Pending returns the in-flight operation of an instance.
func (s *Snapshot) Pending(k engine.Key) (Pending, bool) {
	p, ok := s.pending[k]
	return p, ok
}

// Jobs returns the job history, newest first.
func (s *Snapshot) Jobs() []Job { return s.jobs }
