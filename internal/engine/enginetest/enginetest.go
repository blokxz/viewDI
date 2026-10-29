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
	"errors"
	"slices"
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
