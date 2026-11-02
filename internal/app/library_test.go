package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blokxz/viewDI/internal/catalog"
	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/jobs"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/trace"
	"github.com/blokxz/viewDI/library"
)

func withLibrary(t *testing.T) func(*Options) {
	t.Helper()
	cat, warns := catalog.Load(library.FS, "")
	if len(warns) > 0 {
		t.Fatal(warns)
	}
	return func(o *Options) { o.Library = cat }
}

func libraryDriver(t *testing.T, jr *stubJobs, mutates ...func(*Options)) *driver {
	t.Helper()
	d := newDriver(t, append([]func(*Options){withEngines(nil), withJobs(jr), withImages(t), withLibrary(t)}, mutates...)...)
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	return d
}

func TestLibraryViewListsFiltersAndSwitchesEngine(t *testing.T) {
	d := libraryDriver(t, &stubJobs{})
	d.press("7")
	s := d.screen()
	for _, want := range []string{"Library · ", "Categories", "Databases", "PostgreSQL 17", "7 Library"} {
		if !strings.Contains(s, want) {
			t.Fatalf("library view lacks %q:\n%s", want, s)
		}
	}
	d.press("/")
	d.typeText("rocky")
	d.press("enter")
	s = d.screen()
	if !strings.Contains(s, "Rocky Linux 9") || strings.Contains(s, "PostgreSQL 17") {
		t.Fatalf("filter:\n%s", s)
	}
	d.press("esc", "e") // clear the filter, then Docker only
	if s = d.screen(); strings.Contains(s, "Rocky Linux 9") || !strings.Contains(s, "PostgreSQL 17") {
		t.Fatalf("engine filter (Docker):\n%s", s)
	}
	d.press("e")
	if s = d.screen(); !strings.Contains(s, "Rocky Linux 9") || strings.Contains(s, "PostgreSQL 17") {
		t.Fatalf("engine filter (Incus):\n%s", s)
	}
}

func TestLibraryRunsADockerEntryWithSecretsPortsAndLabel(t *testing.T) {
	jr := &stubJobs{}
	d := libraryDriver(t, jr)
	d.press("7", "/")
	d.typeText("postgres")
	d.press("enter", "enter") // keep the filter, run the entry
	s := d.screen()
	for _, want := range []string{"Run PostgreSQL 17", "127.0.0.1:5432:5432", "POSTGRES_PASSWORD=", "on 127.0.0.1 only"} {
		if !strings.Contains(s, want) {
			t.Fatalf("form lacks %q:\n%s", want, s)
		}
	}
	d.press("enter")
	if len(jr.reqs) != 1 {
		t.Fatalf("submitted %v:\n%s", jr.titles(), d.screen())
	}
	spec := jr.reqs[0].Create
	switch {
	case spec == nil || spec.Image != "postgres:17" || spec.Name != "postgres" || !spec.Start:
		t.Fatalf("spec %+v", spec)
	case spec.Labels["io.viewdi.library"] != "postgres":
		t.Fatalf("labels %v", spec.Labels)
	case len(spec.Volumes) != 1 || spec.Volumes[0].Source != "postgres-data" || spec.Volumes[0].Destination != "/var/lib/postgresql/data":
		t.Fatalf("volumes %+v", spec.Volumes)
	case len(spec.Ports) != 1 || spec.Ports[0].HostPort != 5432 || spec.Ports[0].HostIP != "127.0.0.1":
		t.Fatalf("ports %+v", spec.Ports)
	}
	var password string
	for _, e := range spec.Env {
		if v, ok := strings.CutPrefix(e, "POSTGRES_PASSWORD="); ok {
			password = v
		}
	}
	if len(password) != 24 {
		t.Fatalf("password %q", password)
	}
	j := jr.job(1, jobs.Succeeded, "")
	j.Result = "4f1c9a"
	d.send(JobsMsg{Jobs: []jobs.Job{j}})
	if s = d.screen(); !strings.Contains(s, "postgres is ready") || !strings.Contains(s, "-p 5432") {
		t.Fatalf("the note should appear with the real port:\n%s", s)
	}
}

func TestLibraryMovesATakenPortAndNames(t *testing.T) {
	d := libraryDriver(t, &stubJobs{})
	d.press("7", "/")
	d.typeText("nginx")
	d.press("enter", "enter")
	s := d.screen()
	if !strings.Contains(s, "is taken, using") || strings.Contains(s, "127.0.0.1:8080:80") {
		t.Fatalf("web-api already publishes 8080, so the proposal must move:\n%s", s)
	}
}

func TestLibraryRunsAnIncusEntryWithCloudInit(t *testing.T) {
	jr := &stubJobs{}
	d := libraryDriver(t, jr)
	d.press("7", "/")
	d.typeText("debian-12-docker")
	d.press("enter", "enter")
	if s := d.screen(); !strings.Contains(s, "Run Debian 12 + Docker") || !strings.Contains(s, "images:debian/12/cloud") {
		t.Fatalf("form:\n%s", s)
	}
	d.press("enter")
	if len(jr.reqs) != 1 {
		t.Fatalf("submitted %v:\n%s", jr.titles(), d.screen())
	}
	spec := jr.reqs[0].Create
	switch {
	case spec == nil || spec.Image != "images:debian/12/cloud" || spec.Kind != engine.KindContainer || spec.Memory != "2GiB":
		t.Fatalf("spec %+v", spec)
	case !strings.HasPrefix(spec.Config["cloud-init.user-data"], "#cloud-config") || spec.Config["security.nesting"] != "true":
		t.Fatalf("config %v", spec.Config)
	case spec.Labels["viewdi.library"] != "debian-12-docker":
		t.Fatalf("labels %v", spec.Labels)
	}
}

func TestLibraryRunIsRefusedInReadOnlyAndWithoutTheEngine(t *testing.T) {
	jr := &stubJobs{}
	d := libraryDriver(t, jr, func(o *Options) { o.ReadOnly = true })
	d.press("7", "enter")
	if got := d.lastToast(); !strings.Contains(got, "Read-only") || d.m.ModalDepth() != 0 || len(jr.reqs) != 0 {
		t.Fatalf("toast %q, modals %d", got, d.m.ModalDepth())
	}
}

func TestLibraryHelpers(t *testing.T) {
	a, b := generatePassword(), generatePassword()
	if len(a) != 24 || a == b || strings.ContainsAny(a, ",= \"'$\\") {
		t.Fatalf("passwords %q %q", a, b)
	}
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	snap := d.m.store.Snapshot()
	var existing string
	for _, in := range snap.Instances {
		if in.Engine == engine.Docker {
			existing = in.Name
			break
		}
	}
	if got := freeName(snap, engine.Docker, "", existing); got != existing+"-2" {
		t.Fatalf("freeName(%q) = %q", existing, got)
	}
	if got := freeName(snap, engine.Docker, "", "no-such-thing"); got != "no-such-thing" {
		t.Fatalf("freeName = %q", got)
	}
	var _ store.Snapshot
}

func TestLibrarySearchesDockerHubAndOpensTheForm(t *testing.T) {
	jr := &stubJobs{}
	d := libraryDriver(t, jr)
	d.press("7", "S")
	if s := d.screen(); !strings.Contains(s, "Search Docker Hub") {
		t.Fatalf("search form:\n%s", s)
	}
	d.typeText("grafana")
	d.press("enter")
	s := d.screen()
	if !strings.Contains(s, "grafana/grafana") || !strings.Contains(s, "★ 3000") {
		t.Fatalf("results:\n%s", s)
	}
	d.press("enter")
	s = d.screen()
	if !strings.Contains(s, "Create a Docker container") || !strings.Contains(s, "grafana/grafana") {
		t.Fatalf("form:\n%s", s)
	}
	d.press("enter")
	if len(jr.reqs) != 1 || jr.reqs[0].Create.Image != "grafana/grafana" || !jr.reqs[0].Create.Pull {
		t.Fatalf("submitted %+v", jr.reqs)
	}
}

func TestLibraryFormShowsTheDownloadSize(t *testing.T) {
	d := libraryDriver(t, &stubJobs{})
	d.press("7", "/")
	d.typeText("debian-12-docker")
	d.press("enter", "enter") // an Incus entry: the size is asked for on its own
	if s := d.screen(); !strings.Contains(s, "download of about 100") {
		t.Fatalf("Incus size:\n%s", s)
	}
	d.press("esc", "esc", "/")
	d.typeText("grafana")
	d.press("enter", "enter")
	if s := d.screen(); strings.Contains(s, "download of about") {
		t.Fatalf("Docker must not ask the registry until ctrl+d:\n%s", s)
	}
	d.press("ctrl+d")
	if s := d.screen(); !strings.Contains(s, "download of about 100") {
		t.Fatalf("Docker size after ctrl+d:\n%s", s)
	}
}

func TestLibraryUnusedImagesAreListedAndRemovedAfterConfirming(t *testing.T) {
	d := libraryDriver(t, &stubJobs{})
	d.press("7", "x")
	s := d.screen()
	if !strings.Contains(s, "Unused images") || !strings.Contains(s, "can be reclaimed") {
		t.Fatalf("picker:\n%s", s)
	}
	for _, used := range []string{"nginx:1.27"} { // web-api and nginx-proxy run from it
		if strings.Contains(s, used) {
			t.Fatalf("%s is in use and must not be offered:\n%s", used, s)
		}
	}
	d.press("enter")
	if s = d.screen(); !strings.Contains(s, "Remove image") || !strings.Contains(s, "No instance uses it") {
		t.Fatalf("confirm:\n%s", s)
	}
	d.press("y")
	if got := d.lastToast(); !strings.Contains(got, "Removed") || !strings.Contains(got, "freed") {
		t.Fatalf("toast %q", got)
	}
}

func TestLibraryUnusedImagesRespectReadOnly(t *testing.T) {
	d := libraryDriver(t, &stubJobs{}, func(o *Options) { o.ReadOnly = true })
	d.press("7", "x")
	if got := d.lastToast(); !strings.Contains(got, "Read-only") || d.m.ModalDepth() != 0 {
		t.Fatalf("toast %q, modals %d", got, d.m.ModalDepth())
	}
}

// countingImages fails the test when anything reaches an image source that
// browsing must never touch: searches and sizes are the only network calls
// of the Library, and they happen on S and ctrl+d only.
type countingImages struct {
	t *testing.T
	fakeImages
}

func (c countingImages) SearchImages(ctx context.Context, e engine.ID, q string) ([]engine.RemoteImage, error) {
	c.t.Errorf("SearchImages(%q) while browsing", q)
	return c.fakeImages.SearchImages(ctx, e, q)
}

func (c countingImages) ImageSize(ctx context.Context, e engine.ID, ref string, vm bool) (engine.ImageSize, error) {
	c.t.Errorf("ImageSize(%q) while browsing", ref)
	return c.fakeImages.ImageSize(ctx, e, ref, vm)
}

func TestLibraryBrowsingMakesNoNetworkCall(t *testing.T) {
	w := fake.NewWorld(clock.NewFake(fixedNow))
	fake.Homelab(w)
	d := newDriver(t, withEngines(nil), withJobs(&stubJobs{}), withLibrary(t), func(o *Options) {
		o.Images = countingImages{t: t, fakeImages: fakeImages{w}}
	})
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.press("7", "down", "down", "tab", "down", "tab", "tab", "/")
	d.typeText("redis")
	d.press("enter", "e", "e", "e", "esc", "?", "esc")
	d.press("enter") // running an Docker entry opens the form: only the local list is read
	d.press("esc")
}

func TestLibrarySecretsStayOutOfTheTraceAndTheState(t *testing.T) {
	jr := &stubJobs{}
	d := libraryDriver(t, jr)
	d.press("7", "/")
	d.typeText("postgres")
	d.press("enter", "enter", "enter")
	if len(jr.reqs) != 1 {
		t.Fatalf("submitted %v", jr.titles())
	}
	var password string
	for _, e := range jr.reqs[0].Create.Env {
		if v, ok := strings.CutPrefix(e, "POSTGRES_PASSWORD="); ok {
			password = v
		}
	}
	if password == "" {
		t.Fatal("no generated password")
	}

	// Every message the program processed, as the --trace recorder writes it.
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	w, err := trace.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range d.seen {
		w.Record(msg)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), password) {
		t.Fatal("the generated password is in the --trace file")
	}
	saved, err := json.Marshal(d.saved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), password) {
		t.Fatal("the generated password is in state.json")
	}
	for _, line := range strings.Split(d.screen(), "\n") {
		if strings.Contains(line, password) {
			t.Fatalf("the password is on screen after submitting: %q", line)
		}
	}
}
