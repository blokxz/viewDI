package incus

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

// rebuildDaemon is a daemon double for one instance, "web": it records every
// mutating request and lets a test make one of them fail. Like the real
// daemon it refuses to rebuild an instance that has snapshots.
type rebuildDaemon struct {
	mu        sync.Mutex
	status    int // 103 running, 102 stopped
	snapshots []string
	calls     []string
	failOn    string // a mutating request ending like this fails its operation
	bodies    map[string]string
	failNext  string
	clash     string // what the copy holds that the original needs, "" when nothing
	pool      map[string]any
	resources map[string]any
	usage     int64 // bytes the instance's root disk uses, 0 when the daemon says nothing
}

// claims says what in a copy's devices only one instance can hold, as the
// daemon refuses to start the other one.
func claims(devices map[string]map[string]string) string {
	for name, dev := range devices {
		switch {
		case dev["type"] == "nic" && (dev["ipv4.address"] != "" || dev["ipv6.address"] != "" || dev["hwaddr"] != ""):
			return "address of " + name
		case dev["type"] == "disk" && dev["path"] != "/":
			return "volume " + name
		case dev["type"] != "nic" && dev["type"] != "disk":
			return dev["type"] + " " + name
		}
	}
	return ""
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
	reply := func(meta any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "sync", "status": "Success", "status_code": 200, "metadata": meta})
	}
	switch {
	case r.Method == http.MethodGet && path == "/instances/web":
		reply(map[string]any{
			"name": "web", "type": "container", "description": "the web box",
			"profiles": []string{"default"},
			"config": map[string]string{
				"image.os": "Ubuntu", "user.note": "keep", "limits.cpu": "2",
				"volatile.base_image": "abc123", "volatile.eth0.hwaddr": "00:16:3e:00:00:01", "volatile.uuid": "u-1",
			},
			"devices": map[string]map[string]string{
				"root": {"type": "disk", "pool": "default", "path": "/"},
				"eth0": {"type": "nic", "network": "br0", "ipv4.address": "10.0.0.10", "hwaddr": "00:16:3e:aa:bb:cc"},
				"data": {"type": "disk", "pool": "default", "source": "vol1", "path": "/data"},
				"http": {"type": "proxy", "listen": "tcp:0.0.0.0:80", "connect": "tcp:127.0.0.1:80"},
			},
			"expanded_devices": map[string]map[string]string{"root": {"type": "disk", "pool": "default", "path": "/"}},
		})
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/instances/web"+CopyInfix):
		w.Header().Set("ETag", "e1")
		reply(map[string]any{
			"name": strings.TrimPrefix(path, "/instances/"), "type": "container", "profiles": []string{"default"},
			"devices": map[string]map[string]string{"eth0": {"type": "nic", "network": "br0"}},
		})
	case r.Method == http.MethodGet && path == "/instances/web/snapshots":
		list := make([]map[string]any, 0, len(d.snapshots))
		for _, n := range d.snapshots {
			list = append(list, map[string]any{"name": n})
		}
		reply(list)
	case r.Method == http.MethodGet && path == "/storage-pools/default":
		reply(d.pool)
	case r.Method == http.MethodGet && path == "/storage-pools/default/resources":
		reply(d.resources)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/state"):
		state := map[string]any{"status": "x", "status_code": d.status}
		if d.usage > 0 {
			state["disk"] = map[string]any{"root": map[string]any{"usage": d.usage}}
		}
		reply(state)
	case strings.HasPrefix(path, "/operations/"):
		id := strings.Split(strings.TrimPrefix(path, "/operations/"), "/")[0]
		code, errText := 200, ""
		if strings.HasPrefix(id, "fail-") {
			code, errText = 400, "boom"
		}
		reply(map[string]any{"id": id, "status": "x", "status_code": code, "err": errText})
	case r.Method != http.MethodGet:
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		key := r.Method + " " + path
		if strings.HasSuffix(path, "/state") {
			var put struct{ Action string }
			_ = json.Unmarshal(raw, &put)
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
		if strings.HasSuffix(path, "/rebuild") && len(d.snapshots) > 0 {
			id = "fail-1" // what the real daemon answers
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

const rebuildSource = "images:ubuntu/noble"

func rebuild(a *Adapter, copy bool) (string, []string, error) {
	var phases []string
	got, err := a.Recreate(context.Background(), "default/web", engine.RecreateOptions{Source: rebuildSource, Copy: copy},
		func(p engine.Progress) { phases = append(phases, p.Phase) })
	return got, phases, err
}

var copyDate = regexp.MustCompile(`web` + CopyInfix + `\d{8}-\d{6}`)

func requests(t *testing.T, d *rebuildDaemon, want ...string) {
	t.Helper()
	// The copy's name carries the date: compare it as "<copy>".
	var got []string
	for _, c := range d.log() {
		got = append(got, copyDate.ReplaceAllString(c, "<copy>"))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRebuildCopiesTheStoppedInstanceThenRebuilds(t *testing.T) {
	d := &rebuildDaemon{status: 103}
	a := rebuildAdapter(t, d)
	keep, phases, err := rebuild(a, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(keep, "web"+CopyInfix) {
		t.Errorf("copy name %q", keep)
	}
	requests(t, d, "PUT /instances/web/state stop", "POST /instances", "POST /instances/web/rebuild", "PUT /instances/web/state start")
	body := d.bodies["POST /instances"]
	for _, want := range []string{`"name":"` + keep + `"`, `"type":"copy"`, `"instance_only":true`, `"source":"web"`, `"volatile.base_image":"abc123"`, `"user.note":"keep"`, `"limits.cpu":"2"`} {
		if !strings.Contains(body, want) {
			t.Errorf("copy request lacks %s: %s", want, body)
		}
	}
	var post struct {
		Config  map[string]string
		Devices map[string]map[string]string
	}
	if err := json.Unmarshal([]byte(body), &post); err != nil {
		t.Fatal(err)
	}
	// The copy must not hold anything the rebuilt instance needs to start.
	if len(post.Devices) != 2 || post.Devices["root"]["path"] != "/" || post.Devices["eth0"]["network"] != "br0" {
		t.Errorf("copy devices %v, want the root disk and the NIC", post.Devices)
	}
	if c := claims(post.Devices); c != "" {
		t.Errorf("the copy claims the %s", c)
	}
	// ...but the originals are saved in it, for putting it back by hand.
	var saved map[string]map[string]string
	if err := json.Unmarshal([]byte(post.Config[DevicesKey]), &saved); err != nil || saved["eth0"]["ipv4.address"] != "10.0.0.10" || saved["http"]["type"] != "proxy" {
		t.Errorf("saved devices %q (%v)", post.Config[DevicesKey], err)
	}
	for _, bad := range []string{"volatile.eth0.hwaddr", "volatile.uuid"} {
		if strings.Contains(body, bad) {
			t.Errorf("the copy carries %s", bad)
		}
	}
	if rb := d.bodies["POST /instances/web/rebuild"]; !strings.Contains(rb, `"alias":"ubuntu/noble"`) || !strings.Contains(rb, "simplestreams") {
		t.Errorf("rebuild body %s", rb)
	}
	if strings.Join(phases, ",") != "stopping,copying,rebuilding,starting" {
		t.Errorf("phases %v", phases)
	}
}

func TestRebuildWithoutACopy(t *testing.T) {
	d := &rebuildDaemon{status: 103}
	keep, _, err := rebuild(rebuildAdapter(t, d), false)
	if err != nil || keep != "" {
		t.Fatalf("Recreate = %q, %v", keep, err)
	}
	requests(t, d, "PUT /instances/web/state stop", "POST /instances/web/rebuild", "PUT /instances/web/state start")
}

func TestRebuildPutsTheCopyBackWhenItFails(t *testing.T) {
	d := &rebuildDaemon{status: 103, failOn: "/rebuild"}
	keep, phases, err := rebuild(rebuildAdapter(t, d), true)
	if err == nil || keep != "" {
		t.Fatalf("a failed rebuild reported %q, %v", keep, err)
	}
	requests(t, d,
		"PUT /instances/web/state stop",
		"POST /instances",
		"POST /instances/web/rebuild",
		"DELETE /instances/web",
		"PUT /instances/<copy>",  // the copy gets its addresses back
		"POST /instances/<copy>", // the copy takes the name back
		"PUT /instances/web/state start",
	)
	for k, v := range d.bodies {
		if strings.HasPrefix(k, "PUT /instances/web"+CopyInfix) {
			for _, want := range []string{"10.0.0.10", `"vol1"`, `"proxy"`} {
				if !strings.Contains(v, want) {
					t.Errorf("the copy did not get %s back: %s", want, v)
				}
			}
			if strings.Contains(v, DevicesKey) {
				t.Errorf("the restored instance keeps the note of its devices: %s", v)
			}
		}
	}
	var rename string
	for k, v := range d.bodies {
		if strings.HasPrefix(k, "POST /instances/web"+CopyInfix) {
			rename = v
		}
	}
	if !strings.Contains(rename, `"name":"web"`) {
		t.Errorf("rename body %s", rename)
	}
	if phases[len(phases)-1] != engine.PhaseRollingBack {
		t.Errorf("phases %v", phases)
	}
	if d.status != 103 {
		t.Errorf("instance not running again (status %d)", d.status)
	}
}

func TestRebuildFailureWithoutACopyJustStartsAgain(t *testing.T) {
	d := &rebuildDaemon{status: 103, failOn: "/rebuild"}
	if _, _, err := rebuild(rebuildAdapter(t, d), false); err == nil {
		t.Fatal("a failed rebuild reported success")
	}
	requests(t, d, "PUT /instances/web/state stop", "POST /instances/web/rebuild", "PUT /instances/web/state start")
}

func TestRebuildCopyFailureLeavesTheOriginalRunning(t *testing.T) {
	d := &rebuildDaemon{status: 103, failOn: "POST /instances"}
	if _, _, err := rebuild(rebuildAdapter(t, d), true); err == nil {
		t.Fatal("rebuilt without a copy")
	}
	for _, c := range d.log() {
		if strings.HasSuffix(c, "/rebuild") {
			t.Errorf("went on after the copy failed: %v", d.log())
		}
	}
	if d.status != 103 {
		t.Errorf("original not running again (status %d)", d.status)
	}
}

func TestRebuildRefusesAnInstanceWithSnapshotsBeforeTouchingIt(t *testing.T) {
	d := &rebuildDaemon{status: 103, snapshots: []string{"limpia", "antes-hostkey"}}
	_, _, err := rebuild(rebuildAdapter(t, d), true)
	if engine.Classify(err) != engine.ClassConflict {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"2 snapshots", "limpia", "antes-hostkey", "delete them first"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
	if len(d.log()) != 0 {
		t.Errorf("touched the instance: %v", d.log())
	}
}

func TestRebuildNeedsAnImageSource(t *testing.T) {
	d := &rebuildDaemon{status: 103}
	a := rebuildAdapter(t, d)
	if _, err := a.Recreate(context.Background(), "default/web", engine.RecreateOptions{}, nil); engine.Classify(err) != engine.ClassConflict {
		t.Errorf("no source: %v", err)
	}
	if len(d.log()) != 0 {
		t.Errorf("touched the daemon: %v", d.log())
	}
}

func TestCopyNameFitsAHostName(t *testing.T) {
	now := time.Date(2026, 11, 3, 17, 47, 55, 0, time.UTC)
	if got := copyName("web", now); got != "web-pre-rebuild-20261103-174755" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("a", 60)
	got := copyName(long, now)
	if len(got) > 63 || !strings.HasSuffix(got, CopyInfix+"20261103-174755") || !strings.HasPrefix(got, "aaaa") {
		t.Errorf("got %q (%d)", got, len(got))
	}
}

func TestRebuildStartsWhenTheCopyClaimsNothing(t *testing.T) {
	// The rebuilt instance only starts if the copy does not claim its
	// addresses, volumes and proxies: this is what a real daemon checks.
	d := &rebuildDaemon{status: 103}
	if _, _, err := rebuild(rebuildAdapter(t, d), true); err != nil {
		t.Fatalf("the rebuild failed although the copy claims nothing: %v", err)
	}
}

func TestRebuildRefusesACopyThePoolCannotHold(t *testing.T) {
	d := &rebuildDaemon{
		status: 103, usage: 8 << 30,
		pool:      map[string]any{"name": "default", "driver": "dir"},
		resources: map[string]any{"space": map[string]any{"used": 95 << 30, "total": 100 << 30}},
	}
	_, _, err := rebuild(rebuildAdapter(t, d), true)
	if err == nil || !strings.Contains(err.Error(), "8.0 GiB free") || !strings.Contains(err.Error(), "5.0 GiB") {
		t.Fatalf("error %v", err)
	}
	requests(t, d) // nothing was touched: not even stopped
	if _, _, err := rebuild(rebuildAdapter(t, d), false); err != nil {
		t.Errorf("a rebuild without a copy needs no space: %v", err)
	}
}

func TestRebuildDoesNotCheckSpaceOnACopyOnWritePool(t *testing.T) {
	d := &rebuildDaemon{
		status: 103, usage: 8 << 30,
		pool:      map[string]any{"name": "default", "driver": "zfs"},
		resources: map[string]any{"space": map[string]any{"used": 99 << 30, "total": 100 << 30}},
	}
	if _, _, err := rebuild(rebuildAdapter(t, d), true); err != nil {
		t.Fatal(err)
	}
}
