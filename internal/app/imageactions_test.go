package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/jobs"
	"github.com/blokxz/viewDI/internal/store"
	activityview "github.com/blokxz/viewDI/internal/ui/views/activity"
	"github.com/blokxz/viewDI/internal/updates"
)

// imageRig is a driver with stub jobs, a stub updater and the homelab
// report loaded.
func imageRig(t *testing.T, mutates ...func(*Options)) (*driver, *stubJobs, *stubUpdater, updates.Report) {
	t.Helper()
	jr, u := &stubJobs{}, &stubUpdater{}
	d := newDriver(t, append([]func(*Options){withEngines(nil), withJobs(jr), withUpdater(u)}, mutates...)...)
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	rep := homelabReport(t)
	d.send(UpdatesMsg{Report: rep})
	return d, jr, u, rep
}

func imageNamed(t *testing.T, rep updates.Report, ref string) *updates.ImageUpdate {
	t.Helper()
	for i := range rep.Images {
		if rep.Images[i].Check.Ref == ref {
			return &rep.Images[i]
		}
	}
	t.Fatalf("no %s in %+v", ref, rep.Images)
	return nil
}

func TestImageKeysReachEveryRow(t *testing.T) {
	d, jr, _, rep := imageRig(t, func(o *Options) { o.ReadOnly = true })
	d.press("4")
	var refs []string
	engines := 0
	for range 8 {
		before := len(d.seen)
		d.press("p")
		for _, msg := range d.seen[before:] {
			if a, ok := msg.(activityview.ImageActionMsg); ok {
				if a.Action != activityview.ActPull {
					t.Fatalf("p sent %q", a.Action)
				}
				if a.Image == nil {
					engines++
				} else if !slices.Contains(refs, a.Image.Check.Ref) {
					refs = append(refs, a.Image.Check.Ref)
				}
			}
		}
		d.press("down")
	}
	if len(refs) != len(rep.Images) || engines == 0 {
		t.Fatalf("p reached images %v and %d engine rows", refs, engines)
	}
	if len(jr.reqs) != 0 || !strings.Contains(d.lastToast(), "Read-only mode: pulling, refreshing and recreating are disabled") {
		t.Fatalf("read-only: %v, toast %q", jr.titles(), d.lastToast())
	}
}

func TestPullFlow(t *testing.T) {
	d, jr, u, rep := imageRig(t)
	nginx := imageNamed(t, rep, "nginx:1.27")
	d.send(activityview.ImageActionMsg{Action: activityview.ActPull, Image: nginx})
	if len(jr.reqs) != 1 || jr.reqs[0].Pull == nil || jr.reqs[0].Pull.Ref != "nginx:1.27" || jr.reqs[0].Key.Engine() != engine.Docker {
		t.Fatalf("submitted %+v", jr.reqs)
	}
	if !strings.Contains(d.lastToast(), "Pulling nginx:1.27") {
		t.Errorf("toast %q", d.lastToast())
	}
	if _, busy := d.m.Snapshot().Pending(engineKeyOf(t, d, "nginx-proxy")); busy {
		t.Error("a pull marks the container pending")
	}
	d.send(JobsMsg{Jobs: []jobs.Job{jr.job(1, jobs.Succeeded, "")}})
	if !strings.Contains(d.lastToast(), "Pulled nginx:1.27 · c recreates") || u.imageCount() != 1 || u.count() != 0 {
		t.Fatalf("toast %q, %d image checks, %d checks", d.lastToast(), u.imageCount(), u.count())
	}

	// A failed pull re-checks nothing.
	d.send(activityview.ImageActionMsg{Action: activityview.ActPull, Image: nginx})
	d.send(JobsMsg{Jobs: []jobs.Job{jr.job(2, jobs.Failed, "registry unreachable")}})
	if !strings.Contains(d.lastToast(), "pull nginx:1.27 failed: registry unreachable") || u.imageCount() != 1 {
		t.Fatalf("toast %q, %d image checks", d.lastToast(), u.imageCount())
	}

	// A pulled image is recreated, not pulled again.
	pulled := *nginx
	pulled.Pulled = true
	d.send(activityview.ImageActionMsg{Action: activityview.ActPull, Image: &pulled})
	if len(jr.reqs) != 2 || !strings.Contains(d.lastToast(), "already pulled: press c") {
		t.Fatalf("submitted %v, toast %q", jr.titles(), d.lastToast())
	}
}

func engineKeyOf(t *testing.T, d *driver, name string) engine.Key {
	t.Helper()
	for _, in := range d.m.Snapshot().Instances {
		if in.Name == name {
			return in.Key()
		}
	}
	t.Fatalf("no %s", name)
	return ""
}

func TestRefreshFlow(t *testing.T) {
	d, jr, u, rep := imageRig(t)
	var incus *updates.ImageUpdate
	for i := range rep.Images {
		if rep.Images[i].Check.Engine == engine.Incus {
			incus = &rep.Images[i]
		}
	}
	if incus == nil {
		t.Fatal("no Incus image update")
	}
	d.send(activityview.ImageActionMsg{Action: activityview.ActPull, Image: incus})
	if len(jr.reqs) != 0 || !strings.Contains(d.lastToast(), "refreshed, not pulled: press f") {
		t.Fatalf("p on Incus: %v, toast %q", jr.titles(), d.lastToast())
	}
	d.send(activityview.ImageActionMsg{Action: activityview.ActRefresh, Image: imageNamed(t, rep, "nginx:1.27")})
	if len(jr.reqs) != 0 || !strings.Contains(d.lastToast(), "pulled, not refreshed: press p") {
		t.Fatalf("f on Docker: %v, toast %q", jr.titles(), d.lastToast())
	}
	d.send(activityview.ImageActionMsg{Action: activityview.ActRefresh, Image: incus})
	if len(jr.reqs) != 1 || jr.reqs[0].Refresh == nil || jr.reqs[0].Verb() != "refresh" {
		t.Fatalf("submitted %+v", jr.reqs)
	}
	j := jr.job(1, jobs.Succeeded, "")
	j.Result = "0123456789ab"
	d.send(JobsMsg{Jobs: []jobs.Job{j}})
	if !strings.Contains(d.lastToast(), "→ 0123456789ab · new instances start from it") || u.imageCount() != 1 {
		t.Fatalf("toast %q, %d image checks", d.lastToast(), u.imageCount())
	}

	// c on an Incus image explains incus rebuild and never runs it.
	d.send(activityview.ImageActionMsg{Action: activityview.ActRecreate, Image: incus})
	if len(jr.reqs) != 1 || d.m.ModalDepth() != 0 || !strings.Contains(d.lastToast(), "incus rebuild") || !strings.Contains(d.lastToast(), "root filesystem") {
		t.Fatalf("c on Incus: %v, toast %q", jr.titles(), d.lastToast())
	}
}

func TestRecreateFlow(t *testing.T) {
	d, jr, u, rep := imageRig(t)
	d.send(activityview.ImageActionMsg{Action: activityview.ActRecreate, Image: imageNamed(t, rep, "nginx:1.27")})
	s := d.screen()
	if d.m.ModalDepth() != 1 || len(jr.reqs) != 0 {
		t.Fatalf("no preview:\n%s", s)
	}
	for _, want := range []string{"Recreate 1 container on nginx:1.27", "(pulled first)", "nginx-proxy  kept:", "80→80/tcp, 443→443/tcp", "always"} {
		if !strings.Contains(s, want) {
			t.Fatalf("preview lacks %q:\n%s", want, s)
		}
	}
	d.press("y")
	if d.m.ModalDepth() != 1 || len(jr.reqs) != 0 || !strings.Contains(d.screen(), "Recreate 1 container?") {
		t.Fatalf("no confirm after the preview:\n%s", d.screen())
	}
	d.press("y")
	if len(jr.reqs) != 1 || d.m.ModalDepth() != 0 {
		t.Fatalf("submitted %+v", jr.reqs)
	}
	r := jr.reqs[0]
	if r.Recreate == nil || !r.Recreate.Pull || r.Key != engineKeyOf(t, d, "nginx-proxy") || r.Title() != "recreate nginx-proxy" {
		t.Fatalf("request %+v", r)
	}
	d.send(JobsMsg{Jobs: []jobs.Job{jr.job(1, jobs.Running, "")}})
	if p, busy := d.m.Snapshot().Pending(r.Key); !busy || p.Label() != "recreating" {
		t.Fatalf("pending %+v (%v)", p, busy)
	}
	// A busy container is skipped by the next recreate.
	d.send(activityview.ImageActionMsg{Action: activityview.ActRecreate, Image: imageNamed(t, rep, "nginx:1.27")})
	if d.m.ModalDepth() != 0 || !strings.Contains(d.lastToast(), "Skipped nginx-proxy (busy)") {
		t.Fatalf("busy: modal %d, toast %q", d.m.ModalDepth(), d.lastToast())
	}

	// Rollback: the job fails, the row is no longer pending, nothing is
	// re-checked.
	d.send(JobsMsg{Jobs: []jobs.Job{jr.job(1, jobs.Failed, "the new container exited: exit 1")}})
	if !strings.Contains(d.lastToast(), "recreate nginx-proxy failed: the new container exited") || u.imageCount() != 0 {
		t.Fatalf("toast %q, %d image checks", d.lastToast(), u.imageCount())
	}
	if _, busy := d.m.Snapshot().Pending(r.Key); busy {
		t.Fatal("a failed recreate left the row pending")
	}

	// Success: the toast names it and the images are checked again.
	d.send(activityview.ImageActionMsg{Action: activityview.ActRecreate, Image: imageNamed(t, rep, "nginx:1.27")})
	d.press("y", "y")
	d.send(JobsMsg{Jobs: []jobs.Job{jr.job(2, jobs.Succeeded, "")}})
	if !strings.Contains(d.lastToast(), "Recreated nginx-proxy on the new build") || u.imageCount() != 1 {
		t.Fatalf("toast %q, %d image checks", d.lastToast(), u.imageCount())
	}
}

func TestRecreateEscapes(t *testing.T) {
	d, jr, _, rep := imageRig(t)
	img := imageNamed(t, rep, "nginx:1.27")
	d.send(activityview.ImageActionMsg{Action: activityview.ActRecreate, Image: img})
	d.press("esc")
	d.send(activityview.ImageActionMsg{Action: activityview.ActRecreate, Image: img})
	d.press("y", "esc")
	if len(jr.reqs) != 0 || d.m.ModalDepth() != 0 {
		t.Fatalf("esc recreated: %v", jr.titles())
	}
}

func TestRecreateComposeHint(t *testing.T) {
	d, jr, _, rep := imageRig(t)
	d.send(activityview.ImageActionMsg{Action: activityview.ActRecreate, Image: imageNamed(t, rep, "ghcr.io/me/api:1.4")})
	if len(jr.reqs) != 0 || d.m.ModalDepth() != 0 || !strings.Contains(d.lastToast(), "docker compose -p web up -d api") {
		t.Fatalf("compose: %v, modal %d, toast %q", jr.titles(), d.m.ModalDepth(), d.lastToast())
	}
}

func TestImageActionsNeedAConnectedEngine(t *testing.T) {
	d, jr, _, rep := imageRig(t)
	d.send(ChangesMsg{Changes: []store.Change{store.StatusChanged{Engine: engine.Docker, Health: store.Health{State: store.StateDegraded, Since: fixedNow}}}})
	d.send(activityview.ImageActionMsg{Action: activityview.ActPull, Image: imageNamed(t, rep, "nginx:1.27")})
	if len(jr.reqs) != 0 || !strings.Contains(d.lastToast(), "Docker is not connected") {
		t.Fatalf("disconnected: %v, toast %q", jr.titles(), d.lastToast())
	}
	d.send(activityview.ImageActionMsg{Action: activityview.ActPull})
	if !strings.Contains(d.lastToast(), "act on image rows") {
		t.Fatalf("engine row: toast %q", d.lastToast())
	}
}
