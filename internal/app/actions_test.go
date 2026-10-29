package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/jobs"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/store"
)

// stubJobs records submitted requests; tests deliver job states with
// finish. Next never blocks (the driver runs commands synchronously).
type stubJobs struct {
	reqs     []jobs.Request
	canceled []jobs.ID
}

func (s *stubJobs) Submit(r jobs.Request) jobs.ID {
	s.reqs = append(s.reqs, r)
	return jobs.ID(len(s.reqs))
}

func (s *stubJobs) Cancel(id jobs.ID) bool                  { s.canceled = append(s.canceled, id); return true }
func (s *stubJobs) Next(context.Context) ([]jobs.Job, bool) { return nil, false }

func (s *stubJobs) titles() []string {
	out := make([]string, len(s.reqs))
	for i, r := range s.reqs {
		out[i] = r.Title()
	}
	return out
}

// job returns request i (1-based ID) in the given state.
func (s *stubJobs) job(id int, st jobs.State, err string) jobs.Job {
	j := jobs.Job{ID: jobs.ID(id), Request: s.reqs[id-1], State: st, Err: err, Queued: fixedNow, Started: fixedNow}
	if st.Final() {
		j.Ended = fixedNow.Add(time.Second)
	}
	return j
}

func withJobs(jr JobRunner) func(*Options) { return func(o *Options) { o.Jobs = jr } }

func (d *driver) rowState(name string) string {
	for _, l := range strings.Split(d.screen(), "\n") {
		if strings.Contains(l, " "+name+" ") {
			return l
		}
	}
	return ""
}

func TestActionLifecycleWithPendingRow(t *testing.T) {
	jr := &stubJobs{}
	d := newDriver(t, withEngines(nil), withJobs(jr))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.openDetailOf("redis-cache")
	d.press("esc", "x")
	if got := jr.titles(); len(got) != 1 || got[0] != "stop redis-cache" || jr.reqs[0].Options.Timeout != 0 {
		t.Fatalf("submitted %v %+v", got, jr.reqs)
	}
	d.send(JobsMsg{Jobs: []jobs.Job{jr.job(1, jobs.Running, "")}})
	if !strings.Contains(d.rowState("redis-cache"), "… stopping") {
		t.Fatalf("row %q", d.rowState("redis-cache"))
	}
	d.press("x")
	if len(jr.reqs) != 1 || !strings.Contains(d.lastToast(), "already stopping") {
		t.Fatalf("a pending instance accepted a second action: %v %q", jr.titles(), d.lastToast())
	}
	d.send(JobsMsg{Jobs: []jobs.Job{jr.job(1, jobs.Succeeded, "")}})
	if !strings.Contains(d.lastToast(), "Stop redis-cache: done") || !strings.Contains(d.rowState("redis-cache"), "stopping") {
		t.Fatalf("toast %q row %q", d.lastToast(), d.rowState("redis-cache"))
	}
	// The engine's event confirms the new state.
	d.send(ChangesMsg{Changes: homelab(t, func(w *fake.World) { w.Docker.SetState("redis-cache", engine.StateStopped) })})
	if row := d.rowState("redis-cache"); strings.Contains(row, "stopping") || !strings.Contains(row, "stopped") {
		t.Fatalf("row after confirmation %q", row)
	}
	d.press("5")
	if !strings.Contains(d.screen(), "stop redis-cache") || !strings.Contains(d.screen(), "✓ done") {
		t.Fatalf("jobs view:\n%s", d.screen())
	}
}

func TestFailedAndUnconfirmedActions(t *testing.T) {
	jr := &stubJobs{}
	src := newStubSource()
	d := newDriver(t, withEngines(src), withJobs(jr))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.openDetailOf("web-api")
	d.press("F") // pause
	d.send(JobsMsg{Jobs: []jobs.Job{jr.job(1, jobs.Failed, "instance is not running")}})
	if !strings.Contains(d.lastToast(), "pause web-api failed: instance is not running") {
		t.Fatalf("toast %q", d.lastToast())
	}
	if _, ok := d.m.Snapshot().Pending(jr.reqs[0].Key); ok {
		t.Fatal("failed job left the row pending")
	}

	d.press("R")
	d.send(JobsMsg{Jobs: []jobs.Job{jr.job(2, jobs.Running, ""), jr.job(2, jobs.Succeeded, "")}})
	if _, ok := d.m.Snapshot().Pending(jr.reqs[1].Key); !ok {
		t.Fatal("restart confirmed without a new start time")
	}
	d.send(ClockTickMsg(fixedNow.Add(store.ConfirmTimeout + 2*time.Second)))
	if !strings.Contains(d.lastToast(), "No confirmation from the engine for restart web-api") || src.reconnects != 1 {
		t.Fatalf("toast %q, %d resyncs", d.lastToast(), src.reconnects)
	}
}

func TestBulkActionNeedsConfirmation(t *testing.T) {
	jr := &stubJobs{}
	d := newDriver(t, withEngines(nil), withJobs(jr))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("3", "space", "space", "space")
	marked := d.table().Marked()
	d.press("x")
	if len(jr.reqs) != 0 || d.m.ModalDepth() != 1 || !strings.Contains(d.screen(), "Stop 3 instances?") {
		t.Fatalf("bulk stop without confirmation: %v", jr.titles())
	}
	d.press("y")
	if len(jr.reqs) != 3 || jr.reqs[0].Batch == 0 || jr.reqs[0].Batch != jr.reqs[2].Batch {
		t.Fatalf("submitted %+v", jr.reqs)
	}
	for i, r := range jr.reqs {
		if r.Key != marked[i].Key() {
			t.Fatalf("request %d for %s, want %s", i, r.Key, marked[i].Key())
		}
	}
	d.send(JobsMsg{Jobs: []jobs.Job{jr.job(1, jobs.Succeeded, ""), jr.job(2, jobs.Failed, "boom"), jr.job(3, jobs.Succeeded, "")}})
	if d.lastToast() != "Stop: 2 of 3 done" {
		t.Fatalf("summary %q", d.lastToast())
	}
}

func TestDeleteDialogFlow(t *testing.T) {
	jr := &stubJobs{}
	d := newDriver(t, withEngines(nil), withJobs(jr))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.openDetailOf("web-api")
	d.press("d")
	if d.m.ModalDepth() != 1 || !strings.Contains(d.screen(), "Type the name web-api to confirm") {
		t.Fatalf("dialog:\n%s", d.screen())
	}
	d.press("enter") // nothing typed: the button is disabled
	if len(jr.reqs) != 0 || d.m.ModalDepth() != 1 {
		t.Fatal("delete without typing the name")
	}
	for _, k := range strings.Split("web-api", "") {
		d.press(k)
	}
	d.press("tab", "space", "tab", "tab", "tab") // tick Force, then focus Delete
	d.press("enter")
	if len(jr.reqs) != 1 || !jr.reqs[0].Delete || !jr.reqs[0].DeleteOptions.Force || jr.reqs[0].DeleteOptions.Volumes {
		t.Fatalf("submitted %+v", jr.reqs)
	}
	if d.m.ModalDepth() != 0 {
		t.Fatal("dialog still open")
	}
}

func TestDeleteDialogEscCancels(t *testing.T) {
	jr := &stubJobs{}
	d := newDriver(t, withEngines(nil), withJobs(jr))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.openDetailOf("builder") // created, no volumes: no name to type
	d.press("d")
	if strings.Contains(d.screen(), "Type the name") {
		t.Fatal("a created container needs no typed name")
	}
	d.press("enter") // focus starts on Cancel
	if len(jr.reqs) != 0 || d.m.ModalDepth() != 0 {
		t.Fatalf("enter on the default focus deleted: %v", jr.titles())
	}
	d.press("d", "esc")
	if len(jr.reqs) != 0 {
		t.Fatal("esc deleted")
	}
}

func TestReadOnlyAndRestricted(t *testing.T) {
	jr := &stubJobs{}
	d := newDriver(t, withEngines(nil), withJobs(jr), func(o *Options) { o.ReadOnly = true })
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("3", "s", "d")
	if len(jr.reqs) != 0 || d.m.ModalDepth() != 0 || !strings.Contains(d.lastToast(), "Read-only") {
		t.Fatalf("read-only: %v, toast %q", jr.titles(), d.lastToast())
	}
	if strings.Contains(d.screen(), "s start") {
		t.Error("read-only status bar advertises actions")
	}
}

func TestCancelFromJobsView(t *testing.T) {
	jr := &stubJobs{}
	d := newDriver(t, withEngines(nil), withJobs(jr))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.openDetailOf("redis-cache")
	d.press("R")
	d.send(JobsMsg{Jobs: []jobs.Job{jr.job(1, jobs.Running, "")}})
	d.press("5", "c")
	if len(jr.canceled) != 1 || jr.canceled[0] != 1 {
		t.Fatalf("canceled %v", jr.canceled)
	}
}

func TestRestrictedEngineExplainsWhy(t *testing.T) {
	jr := &stubJobs{}
	d := newDriver(t, withEngines(nil), withJobs(jr))
	d.send(ChangesMsg{Changes: homelab(t, func(w *fake.World) {
		w.Incus.Configure(func(i *engine.Info) {
			i.Capabilities.Restricted, i.Capabilities.Reason = true, "restricted incus-user socket"
		})
	})})
	d.openDetailOf("web-lab")
	if s := d.screen(); !strings.Contains(s, "RESTRICTED") || strings.Contains(s, "x stop") {
		t.Fatalf("status bar:\n%s", s)
	}
	d.press("x")
	if len(jr.reqs) != 0 || !strings.Contains(d.lastToast(), "restricted incus-user socket") {
		t.Fatalf("restricted action: %v %q", jr.titles(), d.lastToast())
	}
}
