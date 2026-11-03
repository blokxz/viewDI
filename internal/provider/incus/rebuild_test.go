package incus

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

// rebuildDaemon is a daemon double for one instance: it records every
// mutating request and lets a test make one of them fail.
type rebuildDaemon struct {
	mu     sync.Mutex
	status int // 103 running, 102 stopped
	calls  []string
	failOn string // "METHOD path" suffix that fails its operation
	bodies map[string]string
}

func (d *rebuildDaemon) log() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

func (d *rebuildDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/1.0")
	sync := func(meta any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "sync", "status": "Success", "status_code": 200, "metadata": meta})
	}
	switch {
	case r.Method == http.MethodGet && path == "/instances/web":
		sync(map[string]any{"name": "web", "type": "container", "status": "Running"})
	case r.Method == http.MethodGet && path == "/instances/web/state":
		code := d.status
		sync(map[string]any{"status": "x", "status_code": code})
	case strings.HasPrefix(path, "/operations/"):
		id := strings.Split(strings.TrimPrefix(path, "/operations/"), "/")[0]
		code, errText := 200, ""
		if strings.HasPrefix(id, "fail-") {
			code, errText = 400, "boom"
		}
		sync(map[string]any{"id": id, "status": "x", "status_code": code, "err": errText})
	case r.Method != http.MethodGet:
		key := r.Method + " " + path
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		if strings.HasSuffix(path, "/state") {
			var put struct{ Action string }
			_ = json.Unmarshal([]byte(body), &put)
			key += " " + put.Action
			switch put.Action {
			case "stop":
				d.status = 102
			case "start":
				d.status = 103
			}
		}
		d.calls = append(d.calls, key)
		if d.bodies == nil {
			d.bodies = map[string]string{}
		}
		d.bodies[key] = body
		id := "ok-1"
		if d.failOn != "" && strings.HasSuffix(key, d.failOn) {
			id = "fail-1"
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "async", "status": "Operation created", "status_code": 100,
			"operation": "/1.0/operations/" + id,
			"metadata":  map[string]any{"id": id, "status": "Running", "status_code": 103},
		})
	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": "not found", "error_code": 404})
	}
}

func rebuildAdapter(t *testing.T, d *rebuildDaemon) *Adapter {
	t.Helper()
	dir, err := os.MkdirTemp("", "viewdi-rb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: d, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	a, err := New(context.Background(), sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestRebuildTakesASnapshotFirstAndRestartsTheInstance(t *testing.T) {
	d := &rebuildDaemon{status: 103}
	a := rebuildAdapter(t, d)
	var phases []string
	snap, err := a.Recreate(context.Background(), "default/web", engine.RecreateOptions{Source: "images:ubuntu/noble"},
		func(p engine.Progress) { phases = append(phases, p.Phase) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(snap, SnapshotPrefix) {
		t.Errorf("snapshot %q", snap)
	}
	got := strings.Join(d.log(), "\n")
	want := []string{
		"POST /instances/web/snapshots",
		"PUT /instances/web/state stop",
		"POST /instances/web/rebuild",
		"PUT /instances/web/state start",
	}
	if got != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
	if b := d.bodies["POST /instances/web/rebuild"]; !strings.Contains(b, `"alias":"ubuntu/noble"`) || !strings.Contains(b, "simplestreams") {
		t.Errorf("rebuild body %s", b)
	}
	if strings.Join(phases, ",") != "snapshotting,stopping,rebuilding,starting" {
		t.Errorf("phases %v", phases)
	}
}

func TestRebuildRestoresTheSnapshotWhenItFails(t *testing.T) {
	d := &rebuildDaemon{status: 103, failOn: "/rebuild"}
	a := rebuildAdapter(t, d)
	var phases []string
	_, err := a.Recreate(context.Background(), "default/web", engine.RecreateOptions{Source: "images:ubuntu/noble"},
		func(p engine.Progress) { phases = append(phases, p.Phase) })
	if err == nil {
		t.Fatal("a failed rebuild reported success")
	}
	want := []string{
		"POST /instances/web/snapshots",
		"PUT /instances/web/state stop",
		"POST /instances/web/rebuild",
		"PUT /instances/web", // the restore
		"PUT /instances/web/state start",
	}
	if got := strings.Join(d.log(), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
	if b := d.bodies["PUT /instances/web"]; !strings.Contains(b, SnapshotPrefix) || !strings.Contains(b, "restore") {
		t.Errorf("restore body %s", b)
	}
	if d.status != 103 {
		t.Errorf("instance not running again (status %d)", d.status)
	}
	if phases[len(phases)-1] != engine.PhaseRollingBack {
		t.Errorf("phases %v", phases)
	}
}

func TestRebuildNeedsAnImageSourceAndASnapshot(t *testing.T) {
	d := &rebuildDaemon{status: 103}
	a := rebuildAdapter(t, d)
	if _, err := a.Recreate(context.Background(), "default/web", engine.RecreateOptions{}, nil); engine.Classify(err) != engine.ClassConflict {
		t.Errorf("no source: %v", err)
	}
	if len(d.log()) != 0 {
		t.Errorf("touched the daemon: %v", d.log())
	}
	d.failOn = "/snapshots"
	if _, err := a.Recreate(context.Background(), "default/web", engine.RecreateOptions{Source: "images:ubuntu/noble"}, nil); err == nil {
		t.Fatal("rebuilt without a snapshot")
	}
	for _, c := range d.log() {
		if strings.Contains(c, "rebuild") || strings.Contains(c, "stop") {
			t.Errorf("went on after the snapshot failed: %v", d.log())
		}
	}
}
