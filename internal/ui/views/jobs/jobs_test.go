package jobs

import (
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/uitest"
	"github.com/blokxz/viewDI/internal/ui/views"
)

func snap(s *store.Store, n int) views.DataMsg {
	for i := 1; i <= n; i++ {
		s.Apply(store.JobUpdated{Job: store.Job{
			ID: uint64(i), Key: engine.MakeKey(engine.Docker, "x"), Name: "x", Verb: "stop",
			Action: engine.Stop, State: store.JobSucceeded, Queued: time.Unix(int64(i), 0),
		}})
	}
	return views.DataMsg{Snapshot: s.Snapshot()}
}

func TestCursorFollowsNewestUntilMoved(t *testing.T) {
	v := New(keys.MustDefault())
	s := store.New(engine.Docker)
	v.Update(snap(s, 1))
	v.Update(snap(s, 3))
	if j, _ := v.Selected(); j.ID != 3 {
		t.Fatalf("cursor on job %d, want the newest", j.ID)
	}
	v.HandleKey(uitest.Key("down"), 0)
	v.Update(snap(s, 5))
	if j, _ := v.Selected(); j.ID != 2 {
		t.Fatalf("moved cursor jumped to job %d", j.ID)
	}
	v.HandleKey(uitest.Key("home"), 0)
	v.Update(snap(s, 6))
	if j, _ := v.Selected(); j.ID != 6 {
		t.Fatalf("home did not resume following: job %d", j.ID)
	}
}

func TestCancelOnlyUnfinished(t *testing.T) {
	v := New(keys.MustDefault())
	s := store.New(engine.Docker)
	v.Update(snap(s, 1))
	if _, cmd, _ := v.HandleKey(uitest.Key("c"), 0); cmd != nil {
		t.Fatal("cancel offered for a finished job")
	}
	s.Apply(store.JobUpdated{Job: store.Job{ID: 2, Key: "docker/y", Name: "y", Verb: "stop", State: store.JobRunning}})
	v.Update(views.DataMsg{Snapshot: s.Snapshot()})
	_, cmd, _ := v.HandleKey(uitest.Key("c"), 0)
	if msgs := uitest.Collect(cmd); len(msgs) != 1 || msgs[0] != (CancelMsg{ID: 2}) {
		t.Fatalf("cancel msgs %v", msgs)
	}
}

func TestDetail(t *testing.T) {
	for _, tc := range []struct {
		job  store.Job
		want string
	}{
		{store.Job{State: store.JobFailed, Err: "boom"}, "boom"},
		{store.Job{State: store.JobRunning, Progress: engine.Progress{Phase: "pulling", Current: 45, Total: 100}}, "pulling ▕█████████░░░░░░░░░░░▏  45%"},
		{store.Job{State: store.JobRunning, Progress: engine.Progress{Phase: "creating"}}, "creating…"},
		{store.Job{State: store.JobRunning, Progress: engine.Progress{Phase: "uploading", Current: 9, Total: 3}}, "uploading ▕████████████████████▏ 100%"},
		{store.Job{State: store.JobSucceeded, Result: "nginx:1.27\x1b[2J"}, "→ nginx:1.27"},
		{store.Job{State: store.JobSucceeded}, ""},
	} {
		if got, _ := detail(tc.job, 60); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.job, got, tc.want)
		}
	}
	if got := progressText(engine.Progress{Phase: "pulling", Current: 1, Total: 2}, 12); got != "pulling  50%" {
		t.Errorf("narrow: %q", got)
	}
}
