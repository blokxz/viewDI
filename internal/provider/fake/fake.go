// Package fake is a scriptable in-memory engine. It backs --mock mode and the
// UI, supervisor and chaos tests. It is a first-class adapter: it passes the
// engine contract suite, and its mutations emit the same events a real
// daemon would.
package fake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
)

// faultKind is the injected failure mode.
type faultKind int

const (
	faultNone faultKind = iota
	faultHang           // calls block until their context ends; events stall
	faultFail           // calls fail with err; event feeds close with err
)

// Engine is an in-memory engine.
type Engine struct {
	id  engine.ID
	clk clock.Clock

	mu        sync.Mutex
	info      engine.Info
	instances map[string]*engine.Instance // by Instance.ID
	meters    map[string]*meter           // synthetic counters, by Instance.ID
	logs      map[string]*logState        // synthetic log timelines, by Instance.ID
	networks  []engine.Network
	volumes   []engine.Volume
	fault     faultKind
	faultErr  error
	// faultChanged is closed and replaced on every fault change.
	faultChanged chan struct{}
	latency      time.Duration
	subs         map[*subscriber]struct{}
	calls        int // daemon calls served, for tests
	quiet        int // >0: mutations emit no events (Quietly)
	closed       bool
}

var _ engine.Engine = (*Engine)(nil)

// New returns an empty engine of kind id.
func New(id engine.ID, clk clock.Clock) *Engine {
	if clk == nil {
		clk = clock.Real{}
	}
	e := &Engine{
		id:           id,
		clk:          clk,
		instances:    map[string]*engine.Instance{},
		meters:       map[string]*meter{},
		logs:         map[string]*logState{},
		subs:         map[*subscriber]struct{}{},
		faultChanged: make(chan struct{}),
		info: engine.Info{
			Engine:        id,
			Version:       map[engine.ID]string{engine.Docker: "29.0.1", engine.Incus: "6.0.5"}[id],
			APIVersion:    map[engine.ID]string{engine.Docker: "1.52", engine.Incus: "1.0"}[id],
			OS:            "linux",
			Arch:          "x86_64",
			Kernel:        "6.12.0",
			StorageDriver: map[engine.ID]string{engine.Docker: "overlay2", engine.Incus: "zfs"}[id],
			CgroupVersion: "2",
			Socket:        "mock://" + string(id),
			Host:          "homelab",
			NCPU:          12,
			MemTotal:      32 << 30,
			Images:        map[engine.ID]int{engine.Docker: 34, engine.Incus: 9}[id],
			Capabilities:  engine.Capabilities{AllProjects: true, VMs: id == engine.Incus},
		},
	}
	if id == engine.Incus {
		e.info.Projects = []string{"default"}
	}
	return e
}

// ID implements engine.Engine.
func (e *Engine) ID() engine.ID { return e.id }

// enter applies latency and injected faults before a call, and reports the
// error the call must fail with, if any.
func (e *Engine) enter(ctx context.Context, op string) error {
	if err := ctx.Err(); err != nil {
		return engine.Wrap(op, err)
	}
	e.mu.Lock()
	e.calls++
	closed, kind, ferr, lat := e.closed, e.fault, e.faultErr, e.latency
	e.mu.Unlock()
	if closed {
		return engine.Errorf(engine.ClassTransient, op, errors.New("engine closed"))
	}
	if lat > 0 {
		select {
		case <-e.clk.After(lat):
		case <-ctx.Done():
			return engine.Wrap(op, ctx.Err())
		}
	}
	switch kind {
	case faultHang:
		<-ctx.Done()
		return engine.Wrap(op, ctx.Err())
	case faultFail:
		return engine.Wrap(op, ferr)
	}
	return nil
}

// Ping implements engine.Engine.
func (e *Engine) Ping(ctx context.Context) error { return e.enter(ctx, "ping") }

// Info implements engine.Engine.
func (e *Engine) Info(ctx context.Context) (engine.Info, error) {
	if err := e.enter(ctx, "info"); err != nil {
		return engine.Info{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	info := e.info
	info.Projects = slices.Clone(e.info.Projects)
	return info, nil
}

// ListInstances implements engine.Engine.
func (e *Engine) ListInstances(ctx context.Context, opts engine.ListOptions) ([]engine.Instance, error) {
	if err := e.enter(ctx, "list instances"); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]engine.Instance, 0, len(e.instances))
	for _, id := range slices.Sorted(maps.Keys(e.instances)) {
		in := e.instances[id]
		if opts.Project != "" && e.id == engine.Incus && in.Project != opts.Project {
			continue
		}
		c := clone(*in)
		c.Env = nil // like the real list endpoints: only Inspect returns it
		out = append(out, c)
	}
	return out, nil
}

// Inspect implements engine.Engine.
func (e *Engine) Inspect(ctx context.Context, id string) (engine.Instance, error) {
	if err := e.enter(ctx, "inspect"); err != nil {
		return engine.Instance{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ok := e.instances[id]
	if !ok {
		return engine.Instance{}, engine.NotFound("inspect", "instance "+id)
	}
	return clone(*in), nil
}

// Networks implements engine.Engine.
func (e *Engine) Networks(ctx context.Context, opts engine.ListOptions) ([]engine.Network, error) {
	if err := e.enter(ctx, "list networks"); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]engine.Network, 0, len(e.networks))
	for _, n := range e.networks {
		if opts.Project == "" || n.Project == "" || n.Project == opts.Project {
			n.Subnets = slices.Clone(n.Subnets)
			out = append(out, n)
		}
	}
	return out, nil
}

// Volumes implements engine.Engine.
func (e *Engine) Volumes(ctx context.Context, opts engine.ListOptions) ([]engine.Volume, error) {
	if err := e.enter(ctx, "list volumes"); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]engine.Volume, 0, len(e.volumes))
	for _, v := range e.volumes {
		if opts.Project == "" || v.Project == "" || v.Project == opts.Project {
			v.UsedBy = slices.Clone(v.UsedBy)
			out = append(out, v)
		}
	}
	return out, nil
}

// Close implements engine.Engine: open feeds end and later calls fail.
func (e *Engine) Close() error {
	e.mu.Lock()
	e.closed = true
	subs := slices.Collect(maps.Keys(e.subs))
	e.mu.Unlock()
	for _, s := range subs {
		s.end(io.EOF)
	}
	return nil
}

// Calls returns the number of daemon calls served so far.
func (e *Engine) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

// ----- fault injection -----

// Hang makes every call block until its context ends, and stalls event
// feeds, like a daemon whose socket accepts but never answers (F2, F3).
func (e *Engine) Hang() { e.setFault(faultHang, nil) }

// Fail makes every call fail with err and closes event feeds with it.
func (e *Engine) Fail(err error) { e.setFault(faultFail, err) }

// FailErrno is Fail with a dial error carrying errno, as the SDKs report
// them: ECONNREFUSED (daemon stopped, F1), ENOENT (socket removed),
// EACCES (permission revoked, F5).
func (e *Engine) FailErrno(errno syscall.Errno) {
	e.Fail(&net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", errno)})
}

// Disconnect simulates a stopped daemon: calls are refused and event feeds
// end with EOF (F1, F4).
func (e *Engine) Disconnect() { e.FailErrno(syscall.ECONNREFUSED) }

// Recover clears any injected fault.
func (e *Engine) Recover() { e.setFault(faultNone, nil) }

// SetLatency delays every call by d (F6).
func (e *Engine) SetLatency(d time.Duration) {
	e.mu.Lock()
	e.latency = d
	e.mu.Unlock()
}

func (e *Engine) setFault(k faultKind, err error) {
	e.mu.Lock()
	e.fault, e.faultErr = k, err
	close(e.faultChanged)
	e.faultChanged = make(chan struct{})
	var subs []*subscriber
	if k == faultFail {
		subs = slices.Collect(maps.Keys(e.subs))
	}
	e.mu.Unlock()
	for _, s := range subs {
		if errors.Is(err, syscall.ECONNREFUSED) {
			s.end(fmt.Errorf("events: %w", io.EOF))
		} else {
			s.end(engine.Wrap("events", err))
		}
	}
}

// ----- world mutation -----

// Configure edits the daemon info (version, projects, capabilities).
func (e *Engine) Configure(f func(*engine.Info)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f(&e.info)
}

// AddNetwork adds a network.
func (e *Engine) AddNetwork(n engine.Network) {
	n.Engine = e.id
	if n.ID == "" {
		n.ID = n.Name
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.networks = append(e.networks, n)
}

// AddVolume adds a volume.
func (e *Engine) AddVolume(v engine.Volume) {
	v.Engine = e.id
	e.mu.Lock()
	defer e.mu.Unlock()
	e.volumes = append(e.volumes, v)
}

// Add creates an instance and emits a create event (plus a start event if
// it is running). Missing fields are filled in: ID, engine, creation time,
// and the start time of running instances.
func (e *Engine) Add(in engine.Instance) engine.Instance {
	in.Engine = e.id
	if in.ID == "" {
		in.ID = e.InstanceID(in.Project, in.Name)
	}
	now := e.clk.Now()
	if in.Created.IsZero() {
		in.Created = now
	}
	if in.State == engine.StateRunning && in.Started.IsZero() {
		in.Started = now
	}
	if in.State != engine.StateRunning {
		in.Started = time.Time{}
	}
	if in.Status == "" {
		in.Status = in.State.String()
	}
	e.mu.Lock()
	c := clone(in)
	e.instances[in.ID] = &c
	e.mu.Unlock()
	e.emit(in, engine.EventChanged, "create")
	if in.State == engine.StateRunning {
		e.emit(in, engine.EventChanged, "start")
	}
	return in
}

// InstanceID returns the ID the engine gives to an instance: a stable
// 64-hex container ID for Docker, "<project>/<name>" for Incus.
func (e *Engine) InstanceID(project, name string) string {
	if e.id == engine.Incus {
		if project == "" {
			project = "default"
		}
		return project + "/" + name
	}
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// Remove deletes an instance by name and emits a remove event.
func (e *Engine) Remove(name string) bool {
	e.mu.Lock()
	in, ok := e.byName(name)
	if ok {
		delete(e.instances, in.ID)
		delete(e.meters, in.ID)
		delete(e.logs, in.ID)
	}
	e.mu.Unlock()
	if ok {
		e.emit(*in, engine.EventRemoved, "destroy")
	}
	return ok
}

// SetState moves an instance to state and emits the matching event.
func (e *Engine) SetState(name string, state engine.State) bool {
	e.mu.Lock()
	in, ok := e.byName(name)
	if !ok {
		e.mu.Unlock()
		return false
	}
	prev := in.State
	in.State = state
	in.Status = state.String()
	now := e.clk.Now()
	switch {
	case state != engine.StateRunning:
		in.Started = time.Time{} // only running instances have an uptime
		if l, ok := e.logs[in.ID]; ok && l.end.IsZero() {
			l.end = now
		}
	case prev != engine.StateRunning:
		in.Started = now
		if m, ok := e.meters[in.ID]; ok { // counters restart with the instance
			e.meters[in.ID] = &meter{profile: m.profile}
		}
		if l, ok := e.logs[in.ID]; ok { // the log continues where it stopped
			l.resume(now)
		}
	}
	snap := *in
	e.mu.Unlock()
	e.emit(snap, engine.EventChanged, action(state, prev))
	return true
}

// Update edits an instance in place and emits a change event.
func (e *Engine) Update(name string, f func(*engine.Instance)) bool {
	e.mu.Lock()
	in, ok := e.byName(name)
	if !ok {
		e.mu.Unlock()
		return false
	}
	f(in)
	snap := *in
	e.mu.Unlock()
	e.emit(snap, engine.EventChanged, "update")
	return true
}

// Quietly runs f with event emission suppressed: the world changes but no
// subscriber hears about it, like an event lost on a stalled stream (F3).
// Only a resync can notice.
func (e *Engine) Quietly(f func()) {
	e.mu.Lock()
	e.quiet++
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.quiet--
		e.mu.Unlock()
	}()
	f()
}

// Names returns every instance name, sorted.
func (e *Engine) Names() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, in := range e.instances {
		out = append(out, in.Name)
	}
	slices.Sort(out)
	return out
}

// Instance returns an instance by name.
func (e *Engine) Instance(name string) (engine.Instance, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ok := e.byName(name)
	if !ok {
		return engine.Instance{}, false
	}
	return clone(*in), true
}

func (e *Engine) byName(name string) (*engine.Instance, bool) {
	for _, in := range e.instances {
		if in.Name == name {
			return in, true
		}
	}
	return nil, false
}

func action(state, prev engine.State) string {
	switch state {
	case engine.StateRunning:
		if prev == engine.StatePaused {
			return "unpause"
		}
		return "start"
	case engine.StatePaused:
		return "pause"
	case engine.StateRestarting:
		return "restart"
	case engine.StateStopped, engine.StateDead:
		return "die"
	}
	return "update"
}

// incusActions maps the Docker-style action names used internally to the
// Incus lifecycle names, so each fake speaks its engine's dialect.
var incusActions = map[string]string{
	"create": "instance-created", "start": "instance-started", "die": "instance-stopped",
	"pause": "instance-paused", "unpause": "instance-resumed", "restart": "instance-restarted",
	"destroy": "instance-deleted", "update": "instance-updated",
}

func clone(in engine.Instance) engine.Instance {
	in.Labels = maps.Clone(in.Labels)
	in.Env = slices.Clone(in.Env)
	in.Networks = slices.Clone(in.Networks)
	in.Ports = slices.Clone(in.Ports)
	in.Mounts = slices.Clone(in.Mounts)
	switch x := in.Extra.(type) {
	case *engine.DockerExtra:
		c := *x
		in.Extra = &c
	case *engine.IncusExtra:
		c := *x
		c.Profiles = slices.Clone(x.Profiles)
		in.Extra = &c
	}
	return in
}
