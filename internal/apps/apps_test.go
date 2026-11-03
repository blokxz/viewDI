package apps

import (
	"testing"

	"github.com/blokxz/viewDI/internal/alerts"
	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
)

func docker(name, project string, st engine.State) engine.Instance {
	return engine.Instance{
		ID: name, Engine: engine.Docker, Name: name, State: st,
		Extra: &engine.DockerExtra{ComposeProject: project},
	}
}

func byName(as []App) map[string]App {
	m := map[string]App{}
	for _, a := range as {
		m[a.Name] = a
	}
	return m
}

func TestDeriveGroupsByLabThenComposeThenLibraryThenOther(t *testing.T) {
	var labs config.Labs
	id, _ := labs.Add("k8s")
	labs.Labs[0].Members = []config.LabMember{
		{Engine: "docker", Name: "api"},                       // also in a compose project: the lab wins
		{Engine: "incus", Project: "default", Name: "node-1"}, // the other engine
	}
	_ = id
	lib := docker("pg", "", engine.StateRunning)
	lib.Labels = map[string]string{"io.viewdi.library": "postgres"}
	incusLib := engine.Instance{
		ID: "default/dev", Engine: engine.Incus, Project: "default", Name: "dev", State: engine.StateRunning,
		Labels: map[string]string{"viewdi.library": "debian-12"},
	}
	got := Derive(Inputs{
		Labs: labs,
		Instances: []engine.Instance{
			docker("api", "web", engine.StateRunning),
			docker("db", "web", engine.StateRunning),
			{ID: "default/node-1", Engine: engine.Incus, Project: "default", Name: "node-1", State: engine.StateRunning},
			lib, incusLib,
			docker("misc", "", engine.StateRunning),
		},
	})
	apps := byName(got)
	if len(apps["k8s"].Machines) != 2 || apps["k8s"].Kind != Lab {
		t.Errorf("lab app = %+v", apps["k8s"])
	}
	if len(apps["web"].Machines) != 1 || apps["web"].Machines[0] != "docker/db" || apps["web"].Kind != Compose {
		t.Errorf("compose app = %+v (api belongs to the lab)", apps["web"])
	}
	if apps["pg"].Kind != Library || apps["dev"].Kind != Library || apps["pg"].ID != "library:docker/pg" {
		t.Errorf("library apps = %+v %+v", apps["pg"], apps["dev"])
	}
	if o := apps["other machines"]; o.Kind != Other || len(o.Machines) != 1 || o.ID != "other" {
		t.Errorf("other = %+v", o)
	}
	if len(got) != 5 {
		t.Errorf("%d apps, want 5", len(got))
	}
}

func TestHealthAggregation(t *testing.T) {
	run, stop := engine.StateRunning, engine.StateStopped
	unhealthy := docker("sick", "p", run)
	unhealthy.Health = engine.HealthUnhealthy
	crashed := docker("crashed", "p", stop)
	crashed.Extra = &engine.DockerExtra{ComposeProject: "p", ExitCode: 137}
	cases := []struct {
		name   string
		in     []engine.Instance
		down   map[engine.Key]bool
		alerts []alerts.Alert
		want   Health
		reason string
	}{
		{"all running", []engine.Instance{docker("a", "p", run), docker("b", "p", run)}, nil, nil, OK, ""},
		{"all stopped on purpose", []engine.Instance{docker("a", "p", stop)}, nil, nil, Off, ""},
		{"some off is still ok", []engine.Instance{docker("a", "p", run), docker("b", "p", stop)}, nil, nil, OK, ""},
		{"restarting", []engine.Instance{docker("a", "p", run), docker("b", "p", engine.StateRestarting)}, nil, nil, Starting, ""},
		{"unhealthy", []engine.Instance{unhealthy, docker("a", "p", run)}, nil, nil, Attention, "sick is unhealthy"},
		{"exit code", []engine.Instance{crashed, docker("a", "p", run)}, nil, nil, Down, "crashed stopped on its own"},
		{"stopped by surprise", []engine.Instance{docker("a", "p", stop), docker("b", "p", run)}, map[engine.Key]bool{"docker/a": true}, nil, Down, "a stopped on its own"},
		{"alert", []engine.Instance{docker("db", "p", run)}, nil, []alerts.Alert{{Key: "docker/db", Severity: alerts.Warn, Subject: "db", Text: "db memory 92%"}}, Attention, "db memory 92%"},
		{"info alerts do not count", []engine.Instance{docker("db", "p", run)}, nil, []alerts.Alert{{Key: "docker/db", Severity: alerts.Info, Text: "x"}}, OK, ""},
		{"down beats attention", []engine.Instance{unhealthy, docker("a", "p", stop)}, map[engine.Key]bool{"docker/a": true}, nil, Down, "a stopped on its own"},
	}
	for _, c := range cases {
		got := Derive(Inputs{Instances: c.in, Down: c.down, Alerts: c.alerts})
		if len(got) != 1 || got[0].Health != c.want || got[0].Reason != c.reason {
			t.Errorf("%s: health %v reason %q, want %v %q (%+v)", c.name, got[0].Health, got[0].Reason, c.want, c.reason, got[0])
		}
	}
}

func TestUsageAndOrder(t *testing.T) {
	pts := map[engine.Key]metrics.Point{
		"docker/a": {CPU: 30, MemUsed: 100, MemLimit: 200},
		"docker/b": {CPU: 20, MemUsed: 50},
		"docker/z": {CPU: 90, MemUsed: 1},
	}
	got := Derive(Inputs{
		Points: pts,
		Instances: []engine.Instance{
			docker("a", "web", engine.StateRunning), docker("b", "web", engine.StateRunning),
			docker("z", "zed", engine.StateRunning), docker("c", "calm", engine.StateRunning),
			docker("dead", "broken", engine.StateStopped),
		},
		Down: map[engine.Key]bool{"docker/dead": true},
	})
	web := byName(got)["web"]
	if web.CPU != 50 || web.MemUsed != 150 || web.MemLimit != 200 || web.Up != 2 {
		t.Errorf("web usage %+v", web)
	}
	var names []string
	for _, a := range got {
		names = append(names, a.Name)
	}
	want := []string{"broken", "zed", "web", "calm"} // down first, then the busiest, then by name
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order %v, want %v", names, want)
		}
	}
}

func TestTrackerTellsAStopFromABootStateAndFromARequestedStop(t *testing.T) {
	tr := NewTracker()
	run := []engine.Instance{docker("a", "p", engine.StateRunning), docker("b", "p", engine.StateRunning), docker("c", "p", engine.StateStopped)}
	tr.Observe(run, nil)
	if len(tr.Down()) != 0 {
		t.Fatalf("already-stopped machines are off, not down: %v", tr.Down())
	}
	stop := []engine.Instance{docker("a", "p", engine.StateStopped), docker("b", "p", engine.StateStopped), docker("c", "p", engine.StateStopped)}
	tr.Observe(stop, map[engine.Key]bool{"docker/b": true}) // b was stopped by a viewDI job
	if !tr.Down()["docker/a"] || tr.Down()["docker/b"] || tr.Down()["docker/c"] {
		t.Fatalf("down = %v", tr.Down())
	}
	tr.Observe(run, nil) // a runs again
	if len(tr.Down()) != 0 {
		t.Fatalf("a machine that runs again is no longer down: %v", tr.Down())
	}
	tr.Observe(stop, nil)
	tr.Forget("docker/a")
	if tr.Down()["docker/a"] {
		t.Fatal("Forget did not clear the machine")
	}
	tr.Observe(stop[1:], nil) // a is deleted; b still stopped by surprise
	if tr.Down()["docker/a"] || !tr.Down()["docker/b"] {
		t.Fatalf("a deleted machine cannot be down, b still is: %v", tr.Down())
	}
	tr.Observe(stop[2:], nil) // b is deleted too
	if len(tr.Down()) != 0 {
		t.Fatalf("down = %v", tr.Down())
	}
}

func TestIDsAreStableAcrossRefreshes(t *testing.T) {
	in := Inputs{Instances: []engine.Instance{docker("a", "web", engine.StateRunning), docker("x", "", engine.StateRunning)}}
	first, second := Derive(in), Derive(in)
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatalf("ids %q %q", first[i].ID, second[i].ID)
		}
	}
	in.Instances[0].State = engine.StateStopped
	if third := Derive(in); third[0].ID != "compose:web" && third[1].ID != "compose:web" {
		t.Fatalf("the ID must not depend on the state: %+v", third)
	}
}

func TestNeedsListsStopsAlertsAndUpdatesWorstFirst(t *testing.T) {
	in := Inputs{
		Instances: []engine.Instance{docker("db", "web", engine.StateRunning), docker("gone", "web", engine.StateStopped)},
		Down:      map[engine.Key]bool{"docker/gone": true},
		Alerts: []alerts.Alert{
			{ID: "memory/docker/db", Rule: alerts.RuleMemory, Severity: alerts.Warn, Key: "docker/db", Subject: "db", Text: "db memory 92% for 10m0s"},
			{ID: "engine-update/docker", Rule: alerts.RuleEngineUpdate, Severity: alerts.Info, Engine: engine.Docker, Subject: "Docker", Text: "update available: 29.1.3 → 29.2.0"},
			{ID: "image-updates", Rule: alerts.RuleImageUpdates, Severity: alerts.Info, Subject: "images", Text: "3 images have updates"},
			{ID: "cpu/x", Rule: alerts.RuleCPU, Severity: alerts.Info, Key: "docker/db", Subject: "db", Text: "noise"},
		},
	}
	got := Needs(in, Derive(in), false)
	var ids []string
	for _, it := range got {
		ids = append(ids, it.ID)
	}
	want := []string{"down:docker/gone", "alert:memory/docker/db", "alert:engine-update/docker", "alert:image-updates"}
	if len(got) != len(want) {
		t.Fatalf("items %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("items %v, want %v", ids, want)
		}
	}
	if got[0].Actions[0].ID != ActStart || got[1].Text != "memory 92% for 10m0s" || got[2].Actions[0].ID != ActUpgrade {
		t.Errorf("items %+v", got)
	}
}

func TestNeedsDropsMutatingActionsInReadOnly(t *testing.T) {
	in := Inputs{
		Instances: []engine.Instance{docker("gone", "web", engine.StateStopped)},
		Down:      map[engine.Key]bool{"docker/gone": true},
		Alerts: []alerts.Alert{
			{ID: "engine-update/docker", Rule: alerts.RuleEngineUpdate, Severity: alerts.Info, Subject: "Docker", Text: "update available: x"},
			{ID: "unhealthy/docker/gone", Rule: alerts.RuleUnhealthy, Severity: alerts.Error, Key: "docker/gone", Subject: "gone", Text: "gone is unhealthy"},
		},
	}
	for _, it := range Needs(in, Derive(in), true) {
		for _, a := range it.Actions {
			if mutating[a.ID] {
				t.Errorf("%s offers %s in read-only", it.ID, a.ID)
			}
		}
		if len(it.Actions) == 0 && it.Rule != alerts.RuleEngineUpdate {
			t.Errorf("%s has no action left", it.ID)
		}
	}
}

func TestFineSilencesAStopUntilItRunsAgain(t *testing.T) {
	crashed := docker("crashed", "p", engine.StateStopped)
	crashed.Extra = &engine.DockerExtra{ComposeProject: "p", ExitCode: 1}
	tr := NewTracker()
	tr.Observe([]engine.Instance{crashed}, nil)
	in := Inputs{Instances: []engine.Instance{crashed}, Down: tr.Down(), Fine: tr.Fine()}
	if got := Derive(in); got[0].Health != Down {
		t.Fatalf("a non-zero exit is down: %+v", got[0])
	}
	tr.Forget("docker/crashed")
	in.Fine = tr.Fine()
	if got := Derive(in); got[0].Health != Off || len(Needs(in, got, false)) != 0 {
		t.Fatalf("fine must silence it: %+v", got[0])
	}
	running := docker("crashed", "p", engine.StateRunning)
	tr.Observe([]engine.Instance{running}, nil)
	tr.Observe([]engine.Instance{crashed}, nil) // it ran and stopped again: news
	in.Down, in.Fine = tr.Down(), tr.Fine()
	if got := Derive(in); got[0].Health != Down {
		t.Fatalf("a stop after it ran again must be reported: %+v", got[0])
	}
}
