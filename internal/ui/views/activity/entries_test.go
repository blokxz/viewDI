package activity

import (
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/alerts"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/theme"
	upd "github.com/blokxz/viewDI/internal/updates"
)

var now = time.Date(2026, 11, 3, 15, 0, 0, 0, time.Local)

func ids(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func find(t *testing.T, es []Entry, id string) Entry {
	t.Helper()
	for _, e := range es {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("no entry %q in %v", id, ids(es))
	return Entry{}
}

// world has one of everything: an error alert, a running job, a finished one
// from yesterday, an engine update and a daemon event.
func world(t *testing.T) *store.Snapshot {
	t.Helper()
	s := store.New(engine.Docker)
	s.Apply(
		store.JobUpdated{Job: store.Job{ID: 1, Key: "docker/web", Name: "web", Verb: "restart", Lifecycle: true, State: store.JobRunning, Queued: now.Add(-time.Minute), Started: now.Add(-50 * time.Second)}},
		store.JobUpdated{Job: store.Job{ID: 2, Key: "docker/db", Name: "db", Verb: "stop", Lifecycle: true, State: store.JobSucceeded, Queued: now.Add(-30 * time.Hour), Ended: now.Add(-30 * time.Hour)}},
		store.EventRecorded{Event: engine.Event{Engine: engine.Docker, Action: "die", InstanceID: "api", Name: "api", Time: now.Add(-time.Hour)}},
	)
	s.ApplyUpdates(upd.Report{
		EnginesAt: now.Add(-2 * time.Hour),
		Engines:   []upd.EngineStatus{{Engine: engine.Docker, Installed: "29.0.0", Available: "29.1.0", Level: upd.LevelSecurity}},
	}, now)
	snap := *s.Snapshot()
	snap.Alerts = []alerts.Alert{
		{ID: "mem/api", Rule: alerts.RuleMemory, Severity: alerts.Error, Key: "docker/api", Subject: "api", Text: "using 96% of its memory", Since: now.Add(-5 * time.Minute)},
		{ID: "upd", Rule: alerts.RuleEngineUpdate, Severity: alerts.Info, Subject: "Docker", Text: "update"},
	}
	return &snap
}

func TestBuildSectionsAndOrder(t *testing.T) {
	es := Build(world(t), now)
	got := ids(es)
	want := []string{"alert/mem/api", "job/1", "update/engine/docker", "event/docker/api/" + now.Add(-time.Hour).Format(time.RFC3339Nano) + "/die", "job/2"}
	if len(got) != len(want) {
		t.Fatalf("entries %v, want %v", got, want)
	}
	// Alerts first, then what is running, then the updates (security first).
	order := []string{"alert/mem/api", "job/1", "update/engine/docker"}
	for i, id := range order {
		if es[i].ID != id {
			t.Fatalf("NOW order %v, want %v first", got, order)
		}
	}
	if find(t, es, "job/2").Section != SecEarlier {
		t.Error("a job from yesterday is not EARLIER")
	}
	if ev := es[3]; ev.Kind != KindEvent || ev.Section != SecToday || ev.Source != "outside viewDI" || ev.Text != "exited" {
		t.Errorf("event %+v", ev)
	}
}

func TestBuildSkipsUpdateAlertsAndAnswersWithTheUpdate(t *testing.T) {
	for _, e := range Build(world(t), now) {
		if e.Alert != nil && e.Alert.Rule == alerts.RuleEngineUpdate {
			t.Fatalf("update alert listed twice: %+v", e)
		}
	}
	u := find(t, Build(world(t), now), "update/engine/docker")
	if u.Level != theme.LevelErr || u.Section != SecNow || u.Engine == nil {
		t.Errorf("update %+v", u)
	}
}

func TestEventsOfViewDIJobsAreNotListedAgain(t *testing.T) {
	s := store.New(engine.Docker)
	s.Apply(
		store.JobUpdated{Job: store.Job{ID: 1, Key: "docker/web", Name: "web", Verb: "stop", Lifecycle: true, State: store.JobSucceeded, Queued: now.Add(-20 * time.Second), Ended: now.Add(-18 * time.Second)}},
		store.EventRecorded{Event: engine.Event{Engine: engine.Docker, Action: "stop", InstanceID: "web", Name: "web", Time: now.Add(-19 * time.Second)}},
		store.EventRecorded{Event: engine.Event{Engine: engine.Docker, Action: "stop", InstanceID: "other", Name: "other", Time: now.Add(-19 * time.Second)}},
		store.EventRecorded{Event: engine.Event{Engine: engine.Docker, Action: "start", InstanceID: "web", Name: "web", Time: now.Add(-time.Hour)}},
	)
	es := Build(s.Snapshot(), now)
	var events []string
	for _, e := range es {
		if e.Kind == KindEvent {
			events = append(events, e.Title+" "+e.Text)
		}
	}
	if len(events) != 2 || events[0] != "other stopped" || events[1] != "web started" {
		t.Fatalf("events %v, want the stop outside viewDI and the earlier start", events)
	}
}

func TestHiddenUpdatesMoveToEarlier(t *testing.T) {
	s := store.New(engine.Docker)
	img := upd.ImageUpdate{Check: engine.ImageCheck{Engine: engine.Docker, Ref: "nginx:1.27"}, Names: []string{"web"}, Available: "sha256:aaaa"}
	s.ApplyUpdates(upd.Report{ImagesAt: now, Images: []upd.ImageUpdate{img}}, now)
	s.SetDecisions(upd.Decisions{}.Snooze(img.Key(), now), now)
	es := Build(s.Snapshot(), now)
	if len(es) != 1 || es[0].Hidden != "snoozed" || es[0].Section != SecEarlier {
		t.Fatalf("entries %+v", es)
	}
	s.SetDecisions(upd.Decisions{}, now)
	if es := Build(s.Snapshot(), now); es[0].Hidden != "" || es[0].Section != SecNow {
		t.Fatalf("unsnoozed: %+v", es)
	}
}

func TestBuildNilSnapshot(t *testing.T) {
	if es := Build(nil, now); es != nil {
		t.Fatalf("entries %v", es)
	}
}
