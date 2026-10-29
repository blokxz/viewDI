package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/jobs"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/store"
)

// fakeImages serves the homelab world's images.
type fakeImages struct{ w *fake.World }

func (f fakeImages) Images(ctx context.Context, e engine.ID) ([]engine.Image, error) {
	return f.w.Engine(e).Images(ctx)
}

func (f fakeImages) SearchImages(ctx context.Context, e engine.ID, q string) ([]engine.RemoteImage, error) {
	return f.w.Engine(e).SearchImages(ctx, q)
}

func withImages(t *testing.T) func(*Options) {
	w := fake.NewWorld(clock.NewFake(fixedNow))
	fake.Homelab(w)
	return func(o *Options) { o.Images = fakeImages{w} }
}

func (d *driver) typeText(s string) {
	for _, r := range s {
		k := string(r)
		if r == ' ' {
			k = "space"
		}
		d.press(k)
	}
}

func TestCreateDockerContainer(t *testing.T) {
	jr := &stubJobs{}
	d := newDriver(t, withEngines(nil), withJobs(jr), withImages(t))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("n")
	if s := d.screen(); !strings.Contains(s, "Create on") || !strings.Contains(s, "container or virtual machine") {
		t.Fatalf("engine picker:\n%s", s)
	}
	d.press("enter") // Docker
	if s := d.screen(); !strings.Contains(s, "Create a Docker container") || !strings.Contains(s, "local images") {
		t.Fatalf("form:\n%s", s)
	}
	d.typeText("ngin")
	d.press("ctrl+n")
	d.press("tab", "tab") // pull, name
	d.typeText("new-web")
	d.press("tab")
	d.typeText("8081:80, 53:53/udp")
	d.press("tab")
	d.typeText("DB_PASSWORD=hunter2")
	d.press("tab", "tab", "tab", "right", "right") // restart: unless-stopped
	if s := d.screen(); strings.Contains(s, "hunter2") || !strings.Contains(s, "DB_PASSWORD=••••••") {
		t.Fatalf("secret shown:\n%s", s)
	}
	d.press("enter")
	if len(jr.reqs) != 1 || d.m.ModalDepth() != 0 {
		t.Fatalf("submitted %v, modals %d:\n%s", jr.titles(), d.m.ModalDepth(), d.screen())
	}
	r := jr.reqs[0]
	spec := r.Create
	switch {
	case r.Key != engine.MakeKey(engine.Docker, "") || r.Title() != "create new-web":
		t.Fatalf("request %+v", r)
	case spec.Image != "nginx:1.27" || spec.Name != "new-web" || !spec.Start || spec.Pull:
		t.Fatalf("spec %+v", spec)
	case len(spec.Ports) != 2 || spec.Ports[1].Protocol != "udp" || len(spec.Env) != 1 || spec.RestartPolicy != "unless-stopped":
		t.Fatalf("spec %+v", spec)
	}
	if !strings.Contains(d.lastToast(), "Creating new-web") {
		t.Fatalf("toast %q", d.lastToast())
	}
	j := jr.job(1, jobs.Succeeded, "")
	j.Result = "4f1c9a"
	d.send(JobsMsg{Jobs: []jobs.Job{j}})
	if !strings.Contains(d.lastToast(), "Created new-web (Docker)") {
		t.Fatalf("toast %q", d.lastToast())
	}
	if d.m.store.HasPending() {
		t.Fatal("a create left a pending mark")
	}
}

func TestCreateFormValidates(t *testing.T) {
	jr := &stubJobs{}
	d := newDriver(t, withEngines(nil), withJobs(jr), withImages(t))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("n", "down", "enter") // Incus
	d.press("enter")
	if len(jr.reqs) != 0 || !strings.Contains(d.screen(), "required") {
		t.Fatalf("submitted an empty form: %v\n%s", jr.titles(), d.screen())
	}
	d.typeText("debian")
	d.press("ctrl+f")
	if s := d.screen(); !strings.Contains(s, "images:debian/12") || !strings.Contains(s, "found for \"debian\"") {
		t.Fatalf("search results:\n%s", s)
	}
	d.press("ctrl+n", "tab")
	d.typeText("1lab")
	d.press("tab", "tab", "tab", "tab")
	d.typeText("lots")
	d.press("enter")
	s := d.screen()
	if len(jr.reqs) != 0 || !strings.Contains(s, "starts with a letter") || !strings.Contains(s, "512MiB") {
		t.Fatalf("invalid form submitted: %v\n%s", jr.titles(), s)
	}
	d.press("esc")
	if d.m.ModalDepth() != 0 || len(jr.reqs) != 0 {
		t.Fatal("esc did not cancel")
	}
}

func TestCreateIncusVM(t *testing.T) {
	jr := &stubJobs{}
	d := newDriver(t, withEngines(nil), withJobs(jr), withImages(t))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("n", "down", "enter")
	d.typeText("images:ubuntu/24.04")
	d.press("tab")
	d.typeText("vm1")
	d.press("tab", "right", "tab", "tab")
	d.typeText("2")
	d.press("tab")
	d.typeText("2GiB")
	d.press("enter")
	if len(jr.reqs) != 1 {
		t.Fatalf("not submitted:\n%s", d.screen())
	}
	spec := jr.reqs[0].Create
	if spec.Kind != engine.KindVM || spec.CPU != 2 || spec.Memory != "2GiB" || len(spec.Profiles) != 1 || spec.Profiles[0] != "default" || spec.Name != "vm1" {
		t.Fatalf("spec %+v", spec)
	}
}

func TestImportIncusImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom.tar.gz"), []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	jr := &stubJobs{}
	d := newDriver(t, withEngines(nil), withJobs(jr), func(o *Options) { o.WorkDir = dir })
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("i")
	if s := d.screen(); !strings.Contains(s, "Docker root filesystem tarball") || !strings.Contains(s, "Incus instance backup") {
		t.Fatalf("kind picker:\n%s", s)
	}
	d.press("down", "down", "down", "enter")
	if s := d.screen(); !strings.Contains(s, "custom.tar.gz") || strings.Contains(s, "notes.txt") {
		t.Fatalf("file picker:\n%s", s)
	}
	d.press("down", "enter") // past ".."
	if s := d.screen(); !strings.Contains(s, "Import into Incus") || !strings.Contains(s, "5B") {
		t.Fatalf("import form:\n%s", s)
	}
	d.typeText("custom/app")
	d.press("enter")
	if len(jr.reqs) != 1 {
		t.Fatalf("not submitted:\n%s", d.screen())
	}
	r := jr.reqs[0]
	want := engine.ImportSpec{Kind: engine.ImportIncusImage, Path: filepath.Join(dir, "custom.tar.gz"), Name: "custom/app"}
	if r.Import == nil || r.Import.Kind != want.Kind || r.Import.Path != want.Path || r.Import.Name != want.Name || r.Key.Engine() != engine.Incus {
		t.Fatalf("request %+v", r)
	}
	j := jr.job(1, jobs.Succeeded, "")
	j.Result = "custom/app"
	d.send(JobsMsg{Jobs: []jobs.Job{j}})
	if !strings.Contains(d.lastToast(), "Imported custom.tar.gz → custom/app") {
		t.Fatalf("toast %q", d.lastToast())
	}
	// The next import starts where the last file was.
	if d.m.importDir != dir {
		t.Fatalf("import dir %q", d.m.importDir)
	}
}

func TestCreateNeedsAWritableEngine(t *testing.T) {
	jr := &stubJobs{}
	d := newDriver(t, withEngines(nil), withJobs(jr), func(o *Options) { o.ReadOnly = true })
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("n")
	if d.m.ModalDepth() != 0 || !strings.Contains(d.lastToast(), "Read-only") {
		t.Fatalf("read-only create: %q", d.lastToast())
	}

	d = newDriver(t, withEngines(nil), withJobs(jr))
	changes := homelab(t, func(w *fake.World) {
		w.Incus.Configure(func(i *engine.Info) {
			i.Capabilities.Restricted, i.Capabilities.Reason = true, "restricted incus-user socket"
		})
	})
	changes = append(changes, store.StatusChanged{Engine: engine.Docker, Health: store.Health{State: store.StateDegraded, Since: fixedNow}})
	d.send(ChangesMsg{Changes: changes})
	d.press("i")
	if t1 := d.lastToast(); d.m.ModalDepth() != 0 || !strings.Contains(t1, "Docker is not connected") || !strings.Contains(t1, "restricted incus-user socket") {
		t.Fatalf("toast %q, modals %d", t1, d.m.ModalDepth())
	}
}
