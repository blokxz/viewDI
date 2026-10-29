// Package enginetest is the engine contract suite: every adapter (fake,
// recorded fixtures, real daemons) must pass the same tests, so the fake
// engine cannot drift from real daemon behaviour.
//
// An adapter's test supplies a Harness: a constructor for an engine that
// holds the reference world described by Expect, and optionally a Trigger
// that makes the engine emit events.
package enginetest

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

// Harness adapts one engine implementation to the suite.
type Harness struct {
	// New returns a fresh engine serving the reference world. The suite
	// closes it.
	New func(t *testing.T) engine.Engine
	// Expect describes what New's engine holds.
	Expect Expect
	// Trigger makes the engine emit at least two events about
	// Expect.Toggle, in order (a stop then a start on a live engine, or a
	// replay of recorded events). Nil skips the event tests.
	Trigger func(t *testing.T, e engine.Engine)
}

// Expect describes the reference world.
type Expect struct {
	Engine    engine.ID
	Instances []Instance // must all be listed (more may exist)
	Networks  []string   // names that must be listed
	Volumes   []string   // names that must be listed
	Toggle    string     // instance name Trigger produces events for
	Projects  []string   // projects Info must report (Incus)
	// Detail is a running instance with a log and stats, for the detail
	// streams (Logs, WatchStats, InspectRaw) and the lifecycle actions.
	// Its zero value skips them.
	Detail Instance
	// Stopped is a stopped instance: pausing it is a conflict, deleting
	// it works. Its zero value skips the action tests.
	Stopped Instance
	// Stateful engines apply actions (fake, real daemons): the suite then
	// checks the resulting states. Recorded fixtures always answer the same.
	Stateful bool
}

// Instance is one expected instance.
type Instance struct {
	Name    string
	Project string
	Kind    engine.Kind
	State   engine.State
}

// Timeout bounds every wait in the suite.
const Timeout = 5 * time.Second

// Run runs the contract suite.
func Run(t *testing.T, h Harness) {
	t.Helper()
	t.Run("PingAndInfo", func(t *testing.T) { testPingInfo(t, h) })
	t.Run("ListInstances", func(t *testing.T) { testList(t, h) })
	t.Run("InspectMatchesList", func(t *testing.T) { testInspect(t, h) })
	t.Run("InspectMissing", func(t *testing.T) { testInspectMissing(t, h) })
	t.Run("ProjectScope", func(t *testing.T) { testProjectScope(t, h) })
	t.Run("NetworksAndVolumes", func(t *testing.T) { testNetVol(t, h) })
	t.Run("CanceledContext", func(t *testing.T) { testCanceled(t, h) })
	t.Run("Stats", func(t *testing.T) { testStats(t, h) })
	t.Run("Detail", func(t *testing.T) {
		if h.Expect.Detail.Name == "" {
			t.Skip("harness names no detail instance")
		}
		t.Run("InspectRaw", func(t *testing.T) { testInspectRaw(t, h) })
		t.Run("LogsTail", func(t *testing.T) { testLogsTail(t, h) })
		t.Run("LogsFollowCancel", func(t *testing.T) { testLogsFollow(t, h) })
		t.Run("WatchStats", func(t *testing.T) { testWatchStats(t, h) })
		t.Run("Missing", func(t *testing.T) { testDetailMissing(t, h) })
	})
	t.Run("Actions", func(t *testing.T) {
		if h.Expect.Detail.Name == "" || h.Expect.Stopped.Name == "" {
			t.Skip("harness names no instances for actions")
		}
		testActions(t, h)
	})
	t.Run("EventsOrderAndClose", func(t *testing.T) {
		if h.Trigger == nil {
			t.Skip("harness has no event trigger")
		}
		testEvents(t, h)
	})
}

func open(t *testing.T, h Harness) engine.Engine {
	t.Helper()
	e := h.New(t)
	t.Cleanup(func() { _ = e.Close() })
	if e.ID() != h.Expect.Engine {
		t.Fatalf("ID() = %q, want %q", e.ID(), h.Expect.Engine)
	}
	return e
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), Timeout)
	t.Cleanup(cancel)
	return c
}

func testPingInfo(t *testing.T, h Harness) {
	e := open(t, h)
	if err := e.Ping(ctx(t)); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	info, err := e.Info(ctx(t))
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Engine != e.ID() || info.Version == "" {
		t.Fatalf("Info = %+v: want engine %q and a version", info, e.ID())
	}
	for _, p := range h.Expect.Projects {
		if !slices.Contains(info.Projects, p) {
			t.Errorf("Info.Projects %v lacks %q", info.Projects, p)
		}
	}
}

func list(t *testing.T, e engine.Engine, project string) []engine.Instance {
	t.Helper()
	got, err := e.ListInstances(ctx(t), engine.ListOptions{Project: project})
	if err != nil {
		t.Fatalf("ListInstances(%q): %v", project, err)
	}
	return got
}

func find(list []engine.Instance, name, project string) (engine.Instance, bool) {
	i := slices.IndexFunc(list, func(in engine.Instance) bool { return in.Name == name && in.Project == project })
	if i < 0 {
		return engine.Instance{}, false
	}
	return list[i], true
}

func testList(t *testing.T, h Harness) {
	e := open(t, h)
	got := list(t, e, "")
	keys := map[engine.Key]bool{}
	for _, in := range got {
		if in.Engine != e.ID() || in.ID == "" || in.Name == "" {
			t.Errorf("incomplete instance %+v", in)
		}
		if keys[in.Key()] {
			t.Errorf("duplicate key %q", in.Key())
		}
		keys[in.Key()] = true
		if in.State == engine.StateRunning && in.Started.IsZero() {
			t.Errorf("%s: running without a start time", in.Name)
		}
		if in.State != engine.StateRunning && !in.Started.IsZero() {
			t.Errorf("%s: %v with a start time", in.Name, in.State)
		}
	}
	for _, want := range h.Expect.Instances {
		in, ok := find(got, want.Name, want.Project)
		if !ok {
			t.Errorf("instance %s (project %q) not listed", want.Name, want.Project)
			continue
		}
		if in.Kind != want.Kind || in.State != want.State {
			t.Errorf("%s: kind %v state %v, want %v %v", want.Name, in.Kind, in.State, want.Kind, want.State)
		}
	}
	// Keys are stable across refreshes.
	again := list(t, e, "")
	for _, in := range again {
		if !keys[in.Key()] {
			t.Errorf("key %q appeared on a second list of an unchanged engine", in.Key())
		}
	}
}

func testInspect(t *testing.T, h Harness) {
	e := open(t, h)
	for _, in := range list(t, e, "") {
		got, err := e.Inspect(ctx(t), in.ID)
		if err != nil {
			t.Errorf("Inspect(%s): %v", in.Name, err)
			continue
		}
		if got.Key() != in.Key() || got.Name != in.Name || got.Project != in.Project ||
			got.State != in.State || got.Kind != in.Kind || got.Image != in.Image {
			t.Errorf("Inspect(%s) = %+v\nlist gave %+v", in.Name, got, in)
		}
		if !got.Started.Equal(in.Started) {
			t.Errorf("Inspect(%s): started %v, list %v", in.Name, got.Started, in.Started)
		}
	}
}

func testInspectMissing(t *testing.T, h Harness) {
	e := open(t, h)
	id := "doesnotexist0000000000000000000000000000000000000000000000000000"
	if e.ID() == engine.Incus {
		id = "default/doesnotexist"
	}
	_, err := e.Inspect(ctx(t), id)
	if engine.Classify(err) != engine.ClassNotFound {
		t.Fatalf("Inspect(missing) = %v (class %v), want not-found", err, engine.Classify(err))
	}
}

func testProjectScope(t *testing.T, h Harness) {
	if len(h.Expect.Projects) == 0 {
		t.Skip("engine has no projects")
	}
	e := open(t, h)
	for _, p := range h.Expect.Projects {
		for _, in := range list(t, e, p) {
			if in.Project != p {
				t.Errorf("ListInstances(project %q) returned %s from %q", p, in.Name, in.Project)
			}
		}
	}
}

func testNetVol(t *testing.T, h Harness) {
	e := open(t, h)
	nets, err := e.Networks(ctx(t), engine.ListOptions{})
	if err != nil {
		t.Fatalf("Networks: %v", err)
	}
	for _, name := range h.Expect.Networks {
		if !slices.ContainsFunc(nets, func(n engine.Network) bool { return n.Name == name && n.Engine == e.ID() }) {
			t.Errorf("network %q not listed", name)
		}
	}
	vols, err := e.Volumes(ctx(t), engine.ListOptions{})
	if err != nil {
		t.Fatalf("Volumes: %v", err)
	}
	for _, name := range h.Expect.Volumes {
		if !slices.ContainsFunc(vols, func(v engine.Volume) bool { return v.Name == name && v.Engine == e.ID() }) {
			t.Errorf("volume %q not listed", name)
		}
	}
}

func testCanceled(t *testing.T, h Harness) {
	e := open(t, h)
	c, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := e.ListInstances(c, engine.ListOptions{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ListInstances with a canceled context succeeded")
		}
	case <-time.After(Timeout):
		t.Fatal("ListInstances ignored a canceled context")
	}
}

func testEvents(t *testing.T, h Harness) {
	e := open(t, h)
	var toggle engine.Instance
	for _, in := range list(t, e, "") {
		if in.Name == h.Expect.Toggle {
			toggle = in
		}
	}
	if toggle.ID == "" {
		t.Fatalf("toggle instance %q not listed", h.Expect.Toggle)
	}

	sctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feed, err := e.Events(sctx)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	h.Trigger(t, e)

	var got []engine.Event
	deadline := time.After(Timeout)
	for len(got) < 2 {
		select {
		case ev, ok := <-feed.C():
			if !ok {
				t.Fatalf("feed closed early: %v", feed.Err())
			}
			if ev.Engine != e.ID() {
				t.Fatalf("event from engine %q", ev.Engine)
			}
			if ev.Key() == toggle.Key() {
				got = append(got, ev)
			}
		case <-deadline:
			t.Fatalf("got %d events about %s, want 2", len(got), toggle.Name)
		}
	}
	for i, ev := range got {
		if ev.Kind != engine.EventChanged || ev.Action == "" || ev.Name != toggle.Name {
			t.Errorf("event %d = %+v", i, ev)
		}
		if i > 0 && ev.Time.Before(got[i-1].Time) {
			t.Errorf("events out of order: %v before %v", got[i-1].Time, ev.Time)
		}
	}

	cancel()
	deadline = time.After(Timeout)
	for {
		select {
		case _, ok := <-feed.C():
			if ok {
				continue
			}
			if err := feed.Err(); err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("feed closed with %v after cancel, want nil or context.Canceled", err)
			}
			return
		case <-deadline:
			t.Fatal("feed not closed after cancel")
		}
	}
}

func testStats(t *testing.T, h Harness) {
	e := open(t, h)
	all := list(t, e, "")
	var ids []string
	running := map[string]bool{}
	for _, in := range all {
		ids = append(ids, in.ID)
		running[in.ID] = in.State == engine.StateRunning
	}
	ids = append(ids, "doesnotexist")
	first, err := e.Stats(ctx(t), ids)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	seen := map[string]engine.Sample{}
	for _, s := range first {
		// Samples for paused or stopped instances are allowed (Incus
		// metrics include frozen instances), unknown IDs are not.
		if _, known := running[s.InstanceID]; !known {
			t.Errorf("sample for unknown instance %q", s.InstanceID)
		}
		if seen[s.InstanceID].InstanceID != "" {
			t.Errorf("duplicate sample for %q", s.InstanceID)
		}
		seen[s.InstanceID] = s
		if s.Has&engine.HasCPU == 0 || s.Time.IsZero() {
			t.Errorf("sample %+v lacks CPU or time", s)
		}
		if s.MemLimit > 0 && s.MemUsed > s.MemLimit {
			t.Errorf("%s: memory %d over limit %d", s.InstanceID, s.MemUsed, s.MemLimit)
		}
	}
	for id, r := range running {
		if r && seen[id].InstanceID == "" {
			t.Errorf("no sample for running instance %q", id)
		}
	}
	// Counters never go backwards between two reads of a live instance.
	second, err := e.Stats(ctx(t), ids)
	if err != nil {
		t.Fatalf("second Stats: %v", err)
	}
	for _, s := range second {
		p, ok := seen[s.InstanceID]
		if !ok {
			continue
		}
		if s.CPU < p.CPU || s.NetRx < p.NetRx || s.NetTx < p.NetTx || s.BlkRead < p.BlkRead || s.BlkWrite < p.BlkWrite {
			t.Errorf("%s: counters went backwards: %+v then %+v", s.InstanceID, p, s)
		}
	}
	if got, err := e.Stats(ctx(t), nil); err != nil || len(got) != 0 {
		t.Errorf("Stats(nil) = %v, %v", got, err)
	}
}

// detailID lists e and returns the ID of the detail instance.
func detailID(t *testing.T, e engine.Engine, h Harness) string {
	t.Helper()
	d := h.Expect.Detail
	in, ok := find(list(t, e, ""), d.Name, d.Project)
	if !ok {
		t.Fatalf("detail instance %s not listed", d.Name)
	}
	if in.State != engine.StateRunning {
		t.Fatalf("detail instance %s is %v, want running", d.Name, in.State)
	}
	return in.ID
}

func testInspectRaw(t *testing.T, h Harness) {
	e := open(t, h)
	raw, err := e.InspectRaw(ctx(t), detailID(t, e, h))
	if err != nil {
		t.Fatalf("InspectRaw: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc) == 0 {
		t.Fatalf("InspectRaw is not a JSON object (%v): %.200s", err, raw)
	}
}

// drain collects a stream until it closes.
func drain[T any](t *testing.T, s *engine.Stream[T]) []T {
	t.Helper()
	var out []T
	deadline := time.After(Timeout)
	for {
		select {
		case v, ok := <-s.C():
			if !ok {
				return out
			}
			out = append(out, v)
		case <-deadline:
			t.Fatalf("stream not closed after %d values", len(out))
		}
	}
}

func testLogsTail(t *testing.T, h Harness) {
	e := open(t, h)
	s, err := e.Logs(ctx(t), detailID(t, e, h), engine.LogOptions{Tail: 10})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	lines := drain(t, s)
	if err := s.Err(); err != nil {
		t.Fatalf("Logs without follow ended with %v, want a clean end", err)
	}
	if len(lines) == 0 || len(lines) > 10+3 { // Incus sends its runtime log too
		t.Fatalf("Logs(tail 10) sent %d lines", len(lines))
	}
	for i, l := range lines {
		if l.Source < engine.Stdout || l.Source > engine.System {
			t.Errorf("line %d: source %v", i, l.Source)
		}
		if len(l.Text) > engine.MaxLogLine {
			t.Errorf("line %d: %d bytes over the cap", i, len(l.Text))
		}
	}
}

func testLogsFollow(t *testing.T, h Harness) {
	e := open(t, h)
	c, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := e.Logs(c, detailID(t, e, h), engine.LogOptions{Tail: 1, Follow: true})
	if err != nil {
		t.Fatalf("Logs(follow): %v", err)
	}
	cancel()
	drain(t, s)
	if err := s.Err(); err != nil && !errors.Is(err, context.Canceled) && !engine.IsEOF(err) {
		t.Fatalf("followed log closed with %v after cancel", err)
	}
}

func testWatchStats(t *testing.T, h Harness) {
	e := open(t, h)
	id := detailID(t, e, h)
	c, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := e.WatchStats(c, id, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("WatchStats: %v", err)
	}
	select {
	case smp, ok := <-s.C():
		if !ok {
			t.Fatalf("stats stream closed before a sample: %v", s.Err())
		}
		if smp.InstanceID != id || smp.Has&engine.HasCPU == 0 || smp.Time.IsZero() {
			t.Fatalf("sample %+v: want instance %q with CPU and time", smp, id)
		}
		if !slices.IsSortedFunc(smp.Ifaces, func(a, b engine.IfaceCounters) int { return strings.Compare(a.Name, b.Name) }) {
			t.Errorf("interfaces not sorted: %+v", smp.Ifaces)
		}
	case <-time.After(Timeout):
		t.Fatal("no sample")
	}
	cancel()
	drain(t, s)
}

func testDetailMissing(t *testing.T, h Harness) {
	e := open(t, h)
	id := "doesnotexist0000000000000000000000000000000000000000000000000000"
	if e.ID() == engine.Incus {
		id = "default/doesnotexist"
	}
	if _, err := e.InspectRaw(ctx(t), id); engine.Classify(err) != engine.ClassNotFound {
		t.Errorf("InspectRaw(missing) = %v, want not-found", err)
	}
	if _, err := e.Logs(ctx(t), id, engine.LogOptions{Tail: 1}); engine.Classify(err) != engine.ClassNotFound {
		t.Errorf("Logs(missing) = %v, want not-found", err)
	}
	if _, err := e.WatchStats(ctx(t), id, time.Second); engine.Classify(err) != engine.ClassNotFound {
		t.Errorf("WatchStats(missing) = %v, want not-found", err)
	}
}

// state re-reads an instance's state (stateful harnesses only).
func state(t *testing.T, e engine.Engine, id string) engine.State {
	t.Helper()
	in, err := e.Inspect(ctx(t), id)
	if err != nil {
		t.Fatalf("Inspect after action: %v", err)
	}
	return in.State
}

func testActions(t *testing.T, h Harness) {
	e := open(t, h)
	all := list(t, e, "")
	run, ok1 := find(all, h.Expect.Detail.Name, h.Expect.Detail.Project)
	stop, ok2 := find(all, h.Expect.Stopped.Name, h.Expect.Stopped.Project)
	if !ok1 || !ok2 || run.State != engine.StateRunning || stop.State != engine.StateStopped {
		t.Fatalf("want %s running and %s stopped", h.Expect.Detail.Name, h.Expect.Stopped.Name)
	}
	steps := []struct {
		action engine.Action
		want   engine.State
	}{
		{engine.Stop, engine.StateStopped},
		{engine.Stop, engine.StateStopped}, // already in effect: no-op
		{engine.Start, engine.StateRunning},
		{engine.Start, engine.StateRunning},
		{engine.Pause, engine.StatePaused},
		{engine.Unpause, engine.StateRunning},
		{engine.Restart, engine.StateRunning},
	}
	for _, st := range steps {
		if err := e.Act(ctx(t), run.ID, st.action, engine.ActionOptions{Timeout: time.Second}); err != nil {
			t.Fatalf("%s %s: %v", st.action, run.Name, err)
		}
		if h.Expect.Stateful {
			if got := state(t, e, run.ID); got != st.want {
				t.Fatalf("after %s: %v, want %v", st.action, got, st.want)
			}
		}
	}

	if err := e.Act(ctx(t), stop.ID, engine.Pause, engine.ActionOptions{}); engine.Classify(err) != engine.ClassConflict {
		t.Errorf("pause of a stopped instance = %v, want a conflict", err)
	}
	if err := e.Delete(ctx(t), run.ID, engine.DeleteOptions{}); engine.Classify(err) != engine.ClassConflict {
		t.Errorf("delete of a running instance = %v, want a conflict", err)
	}
	missing := "doesnotexist0000000000000000000000000000000000000000000000000000"
	if e.ID() == engine.Incus {
		missing = "default/doesnotexist"
	}
	if err := e.Act(ctx(t), missing, engine.Start, engine.ActionOptions{}); engine.Classify(err) != engine.ClassNotFound {
		t.Errorf("start of a missing instance = %v, want not-found", err)
	}
	if err := e.Delete(ctx(t), missing, engine.DeleteOptions{}); engine.Classify(err) != engine.ClassNotFound {
		t.Errorf("delete of a missing instance = %v, want not-found", err)
	}

	if err := e.Delete(ctx(t), stop.ID, engine.DeleteOptions{Volumes: true}); err != nil {
		t.Fatalf("delete %s: %v", stop.Name, err)
	}
	if h.Expect.Stateful {
		if _, err := e.Inspect(ctx(t), stop.ID); engine.Classify(err) != engine.ClassNotFound {
			t.Errorf("deleted instance still inspectable: %v", err)
		}
	}
}
