//go:build integration

package incus

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/engine/enginetest"
)

// Every instance the integration tests create starts with this prefix, so a
// crashed run is cleaned up by the next one (and by hand with
// `incus list viewdi-it- -c n -f csv | xargs -r -n1 incus delete -f`).
const itPrefix = "viewdi-it-"

const itImage = "images:alpine/3.20" // a few MiB, boots in about a second

func itSocket() string {
	sock, _ := Discover("", "", os.Getenv, StatProbe)
	return sock
}

func itEngine(t *testing.T) *Adapter {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	a, err := New(ctx, itSocket())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(); cancel() })
	return a
}

// sweep deletes every instance left over by the integration tests.
func sweep(t *testing.T, a *Adapter) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	list, err := a.ListInstances(ctx, engine.ListOptions{})
	if err != nil {
		t.Logf("sweep: %v", err)
		return
	}
	for _, in := range list {
		if !strings.HasPrefix(in.Name, itPrefix) {
			continue
		}
		_ = a.Act(ctx, in.ID, engine.Stop, engine.ActionOptions{Timeout: time.Second})
		if err := a.Delete(ctx, in.ID, engine.DeleteOptions{}); err != nil {
			t.Logf("sweep delete %s: %v", in.Name, err)
		}
	}
}

// TestIntegrationContract runs the contract suite against the real daemon, on
// a world of its own (docs/05-quality-plan.md §3).
func TestIntegrationContract(t *testing.T) {
	a := itEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := a.Ping(ctx); err != nil {
		t.Skipf("no Incus daemon at %s: %v", itSocket(), err)
	}
	sweep(t, a)
	t.Cleanup(func() { sweep(t, a) })

	spec := func(name string) engine.CreateSpec {
		return engine.CreateSpec{Name: itPrefix + name, Image: itImage, Start: true, Kind: engine.KindContainer}
	}
	run, stop := spec("run"), spec("stop")
	for _, s := range []engine.CreateSpec{run, stop} {
		if _, err := a.Create(ctx, s, nil); err != nil {
			t.Fatalf("create %s: %v", s.Name, err)
		}
	}
	for _, in := range mustList(t, a) {
		if in.Name == stop.Name {
			if err := a.Act(ctx, in.ID, engine.Stop, engine.ActionOptions{Timeout: 5 * time.Second}); err != nil {
				t.Fatalf("stop %s: %v", in.Name, err)
			}
		}
	}

	newSpec := spec("new")
	newSpec.Start = false
	enginetest.Run(t, enginetest.Harness{
		New: func(t *testing.T) engine.Engine { return itEngine(t) },
		Expect: enginetest.Expect{
			Engine: engine.Incus,
			Instances: []enginetest.Instance{
				{Name: run.Name, Project: "default", Kind: engine.KindContainer, State: engine.StateRunning},
				{Name: stop.Name, Project: "default", Kind: engine.KindContainer, State: engine.StateStopped},
			},
			Toggle:   run.Name,
			Projects: []string{"default"},
			Detail:   enginetest.Instance{Name: run.Name, Project: "default"},
			Stopped:  enginetest.Instance{Name: stop.Name, Project: "default"},
			Stateful: true,
			// No Image: a cached remote image has no local alias, so the
			// Images subtest cannot find a reference; search is tested below.
			Create: newSpec,
		},
		Trigger: func(t *testing.T, e engine.Engine) {
			t.Helper()
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			var id string
			for _, in := range mustList(t, e) {
				if in.Name == run.Name {
					id = in.ID
				}
			}
			for _, act := range []engine.Action{engine.Stop, engine.Start} {
				if err := e.Act(ctx, id, act, engine.ActionOptions{Timeout: 5 * time.Second}); err != nil {
					t.Fatalf("%s %s: %v", act, run.Name, err)
				}
			}
		},
	})
}

func mustList(t *testing.T, e engine.Engine) []engine.Instance {
	t.Helper()
	list, err := e.ListInstances(context.Background(), engine.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// TestIntegrationSearch queries the real public image server.
func TestIntegrationSearch(t *testing.T) {
	a := itEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := a.Ping(ctx); err != nil {
		t.Skipf("no Incus daemon at %s: %v", itSocket(), err)
	}
	res, err := a.SearchImages(ctx, "alpine")
	if err != nil || len(res) == 0 {
		t.Fatalf("SearchImages(alpine) = %d results, %v", len(res), err)
	}
}
