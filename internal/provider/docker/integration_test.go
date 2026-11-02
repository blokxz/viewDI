//go:build integration

package docker

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/engine/enginetest"
)

// Every container the integration tests create starts with this prefix, so a
// crashed run is cleaned up by the next one (and by hand with
// `docker rm -f $(docker ps -aq --filter name=viewdi-it-)`).
const itPrefix = "viewdi-it-"

const itImage = "nginx:1.27-alpine" // long-running, logs on start, small

func itHost() string {
	host, _ := Discover("", "", config.DefaultPaths().RuntimeDir, os.Getenv)
	return host
}

func itEngine(t *testing.T) *Adapter {
	t.Helper()
	a, err := New(itHost())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// sweep removes every container left over by the integration tests.
func sweep(t *testing.T, a *Adapter) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	list, err := a.ListInstances(ctx, engine.ListOptions{})
	if err != nil {
		t.Logf("sweep: %v", err)
		return
	}
	for _, in := range list {
		if strings.HasPrefix(in.Name, itPrefix) {
			if err := a.Act(ctx, in.ID, engine.Stop, engine.ActionOptions{Timeout: time.Second}); err != nil {
				t.Logf("sweep stop %s: %v", in.Name, err)
			}
			if err := a.Delete(ctx, in.ID, engine.DeleteOptions{Volumes: true}); err != nil {
				t.Logf("sweep delete %s: %v", in.Name, err)
			}
		}
	}
}

// TestIntegrationContract runs the contract suite against the real daemon,
// on a world of its own (docs/05-quality-plan.md §3).
func TestIntegrationContract(t *testing.T) {
	a := itEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := a.Ping(ctx); err != nil {
		t.Skipf("no Docker daemon at %s: %v", itHost(), err)
	}
	sweep(t, a)
	t.Cleanup(func() { sweep(t, a) })

	// The pull runs once; the suite's Create then finds the image local.
	run := engine.CreateSpec{Name: itPrefix + "run", Image: itImage, Pull: true, Start: true}
	stop := engine.CreateSpec{Name: itPrefix + "stop", Image: itImage, Start: true}
	for _, spec := range []engine.CreateSpec{run, stop} {
		if _, err := a.Create(ctx, spec, nil); err != nil {
			t.Fatalf("create %s: %v", spec.Name, err)
		}
	}
	list, err := a.ListInstances(ctx, engine.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range list {
		if in.Name == stop.Name {
			if err := a.Act(ctx, in.ID, engine.Stop, engine.ActionOptions{Timeout: 2 * time.Second}); err != nil {
				t.Fatalf("stop %s: %v", in.Name, err)
			}
		}
	}

	enginetest.Run(t, enginetest.Harness{
		New: func(t *testing.T) engine.Engine { return itEngine(t) },
		Expect: enginetest.Expect{
			Engine: engine.Docker,
			Instances: []enginetest.Instance{
				{Name: run.Name, Kind: engine.KindContainer, State: engine.StateRunning},
				{Name: stop.Name, Kind: engine.KindContainer, State: engine.StateStopped},
			},
			Toggle:   run.Name,
			Detail:   enginetest.Instance{Name: run.Name},
			Stopped:  enginetest.Instance{Name: stop.Name},
			Stateful: true,
			Image:    itImage,
			Search:   "nginx",
			Create:   engine.CreateSpec{Name: itPrefix + "new", Image: itImage},
		},
		Trigger: func(t *testing.T, e engine.Engine) {
			t.Helper()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			in := mustFind(t, e, run.Name)
			for _, act := range []engine.Action{engine.Stop, engine.Start} {
				if err := e.Act(ctx, in.ID, act, engine.ActionOptions{Timeout: 2 * time.Second}); err != nil {
					t.Fatalf("%s %s: %v", act, run.Name, err)
				}
			}
		},
	})
}

func mustFind(t *testing.T, e engine.Engine, name string) engine.Instance {
	t.Helper()
	list, err := e.ListInstances(context.Background(), engine.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range list {
		if in.Name == name {
			return in
		}
	}
	t.Fatalf("instance %q not found", name)
	return engine.Instance{}
}
