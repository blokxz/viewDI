// Package jobs runs lifecycle operations (start, stop, delete, create,
// import…) in the background: each job gets its own cancellable context and
// a timeout, at most MaxRunning run at once, panics become failed jobs, and
// every state change is reported to the UI, which never waits on a daemon.
//
// Updates are coalesced per job (latest state wins) but never lost: a
// job's final state is always delivered. Progress reports are throttled to
// one per ProgressInterval per job.
package jobs

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
)

// MaxRunning bounds concurrent jobs; others wait queued.
const MaxRunning = 4

// ProgressInterval is the minimum time between two progress reports of a
// job (a phase change is always reported).
const ProgressInterval = 100 * time.Millisecond

// LongTimeout bounds creates and imports, which pull and upload images.
const LongTimeout = 30 * time.Minute

// ID identifies a job.
type ID uint64

// State is a job's state.
type State int

// Job states.
const (
	Queued State = iota
	Running
	Succeeded
	Failed
	Canceled
)

func (s State) String() string {
	return [...]string{"queued", "running", "succeeded", "failed", "canceled"}[s]
}

// Final reports whether the job is over.
func (s State) Final() bool { return s >= Succeeded }

// Request describes one operation on one instance, or one create or
// import on an engine.
type Request struct {
	// Key is the instance; for Create and Import, only its engine part is
	// set (engine.MakeKey(id, "")).
	Key  engine.Key
	Name string // instance name (or image, file), for display
	// Action is the lifecycle action; ignored when Delete, Create or Import
	// is set.
	Action  engine.Action
	Options engine.ActionOptions
	// Delete removes the instance with DeleteOptions.
	Delete        bool
	DeleteOptions engine.DeleteOptions
	// Create creates an instance.
	Create *engine.CreateSpec
	// Import imports a file.
	Import *engine.ImportSpec
	// Batch groups the jobs started by one user action (0 = none).
	Batch uint64
}

// Verb is "stop", "delete", "create", "import"…
func (r Request) Verb() string {
	switch {
	case r.Create != nil:
		return "create"
	case r.Import != nil:
		return "import"
	case r.Delete:
		return "delete"
	}
	return string(r.Action)
}

// Lifecycle reports whether the job acts on an existing instance.
func (r Request) Lifecycle() bool { return r.Create == nil && r.Import == nil }

// Title is "stop web-api".
func (r Request) Title() string { return r.Verb() + " " + r.Name }

// Job is a snapshot of one job.
type Job struct {
	ID ID
	Request
	State State
	Err   string
	Class engine.Class // of Err
	// Progress is the latest progress of a create or import.
	Progress engine.Progress
	// Result is what a create or import made: the instance ID, image refs.
	Result  string
	Queued  time.Time
	Started time.Time
	Ended   time.Time
}

// Options tunes a runner.
type Options struct {
	Clock  clock.Clock
	Logger *slog.Logger
}

// Runner runs jobs on the engines it was given.
type Runner struct {
	ctx     context.Context
	engines map[engine.ID]engine.Engine
	clk     clock.Clock
	log     *slog.Logger
	sem     chan struct{}
	wg      sync.WaitGroup

	mu      sync.Mutex
	next    ID
	cancels map[ID]context.CancelFunc
	changed map[ID]Job // since the last Next
	notify  chan struct{}
}

// New returns a runner whose jobs are canceled when ctx ends.
func New(ctx context.Context, opts Options, engines ...engine.Engine) *Runner {
	r := &Runner{
		ctx: ctx, engines: map[engine.ID]engine.Engine{}, clk: opts.Clock, log: opts.Logger,
		sem: make(chan struct{}, MaxRunning), cancels: map[ID]context.CancelFunc{},
		changed: map[ID]Job{}, notify: make(chan struct{}, 1),
	}
	if r.clk == nil {
		r.clk = clock.Real{}
	}
	if r.log == nil {
		r.log = slog.New(slog.DiscardHandler)
	}
	for _, e := range engines {
		r.engines[e.ID()] = e
	}
	return r
}

// Wait blocks until every job goroutine exited (after the context ended).
func (r *Runner) Wait() { r.wg.Wait() }

// Submit queues a job and returns its ID. It never blocks.
func (r *Runner) Submit(req Request) ID {
	ctx, cancel := context.WithCancel(r.ctx)
	r.mu.Lock()
	r.next++
	j := Job{ID: r.next, Request: req, State: Queued, Queued: r.clk.Now()}
	r.cancels[j.ID] = cancel
	r.mu.Unlock()
	r.report(j)
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer cancel()
		r.run(ctx, j)
	}()
	return j.ID
}

// Cancel cancels a queued or running job. It reports false if the job is
// unknown or already over.
func (r *Runner) Cancel(id ID) bool {
	r.mu.Lock()
	cancel, ok := r.cancels[id]
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

func (r *Runner) run(ctx context.Context, j Job) {
	finish := func(err error) {
		j.Ended = r.clk.Now()
		switch {
		case err == nil:
			j.State = Succeeded
		case errors.Is(ctx.Err(), context.Canceled) && r.ctx.Err() == nil:
			j.State, j.Err = Canceled, "canceled"
		default:
			j.State, j.Err, j.Class = Failed, message(err), engine.Classify(err)
		}
		r.mu.Lock()
		delete(r.cancels, j.ID)
		r.mu.Unlock()
		r.report(j)
	}
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		finish(ctx.Err())
		return
	}
	j.State, j.Started = Running, r.clk.Now()
	r.report(j)
	eng, ok := r.engines[j.Key.Engine()]
	if !ok {
		finish(fmt.Errorf("unknown engine %q", j.Key.Engine()))
		return
	}
	var (
		mu   sync.Mutex // progress may come from the adapter's goroutines,
		last time.Time
		over bool // even after the operation returned (an upload's writer)
	)
	progress := func(p engine.Progress) {
		mu.Lock()
		defer mu.Unlock()
		now := r.clk.Now()
		if over || p.Phase == j.Progress.Phase && now.Sub(last) < ProgressInterval {
			return
		}
		last, j.Progress = now, p
		r.report(j)
	}
	result, err := r.do(ctx, eng, j.Request, progress)
	mu.Lock()
	defer mu.Unlock()
	j.Result, over = result, true
	finish(err)
}

// do runs the operation, turning a panic into an error.
func (r *Runner) do(ctx context.Context, eng engine.Engine, req Request, progress engine.ProgressFunc) (result string, err error) {
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("job panic", "job", req.Title(), "panic", p, "stack", string(debug.Stack()))
			err = fmt.Errorf("internal error: %v", p)
		}
	}()
	timeout := engine.ActionTimeout(req.Options)
	if !req.Lifecycle() {
		timeout = LongTimeout
	}
	ctx, cancel := clock.WithTimeout(ctx, r.clk, timeout)
	defer cancel()
	_, id, _ := strings.Cut(string(req.Key), "/")
	switch {
	case req.Create != nil:
		return eng.Create(ctx, *req.Create, progress)
	case req.Import != nil:
		return eng.Import(ctx, *req.Import, progress)
	case req.Delete:
		return "", eng.Delete(ctx, id, req.DeleteOptions)
	}
	return "", eng.Act(ctx, id, req.Action, req.Options)
}

// message is the error text without the operation prefix the UI already
// shows ("stop 9f3c…: ").
func message(err error) string {
	var e *engine.Error
	if errors.As(err, &e) && e.Err != nil {
		return e.Err.Error()
	}
	return err.Error()
}

func (r *Runner) report(j Job) {
	r.mu.Lock()
	r.changed[j.ID] = j
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

// Next waits for job changes and returns the latest state of every job
// that changed, by ID. It reports false once ctx or the runner ended.
func (r *Runner) Next(ctx context.Context) ([]Job, bool) {
	for {
		select {
		case <-ctx.Done():
			return nil, false
		case <-r.ctx.Done():
			return nil, false
		case <-r.notify:
		}
		r.mu.Lock()
		out := make([]Job, 0, len(r.changed))
		for _, j := range r.changed {
			out = append(out, j)
		}
		clear(r.changed)
		r.mu.Unlock()
		if len(out) > 0 {
			slices.SortFunc(out, func(a, b Job) int { return cmp.Compare(a.ID, b.ID) })
			return out, true
		}
	}
}
