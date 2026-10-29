package cgroup

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

// fakeHost builds a cgroup2 + procfs tree under a temp dir.
type fakeHost struct {
	t          *testing.T
	root, proc string
}

func newHost(t *testing.T) *fakeHost {
	dir := t.TempDir()
	h := &fakeHost{t: t, root: filepath.Join(dir, "cgroup"), proc: filepath.Join(dir, "proc")}
	h.file(h.root, "cgroup.controllers", "cpu io memory pids")
	return h
}

func (h *fakeHost) file(dir, name, content string) {
	h.t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// container writes a container cgroup at rel with the given CPU µs.
func (h *fakeHost) container(rel string, usec int) string {
	dir := filepath.Join(h.root, rel)
	h.file(dir, "cpu.stat", "usage_usec "+itoa(usec)+"\nuser_usec 1\nsystem_usec 2\n")
	h.file(dir, "cpu.max", "150000 100000\n")
	h.file(dir, "memory.current", "524288000\n")
	h.file(dir, "memory.stat", "anon 400000000\nfile 124288000\ninactive_file 24288000\n")
	h.file(dir, "memory.max", "max\n")
	h.file(dir, "io.stat", "259:0 rbytes=1000 wbytes=2000 rios=1 wios=2 dbytes=0 dios=0\n8:0 rbytes=24 wbytes=48\n")
	h.file(dir, "pids.current", "12\n")
	return dir
}

func (h *fakeHost) proc0(pid int, cgroupLine string) {
	d := filepath.Join(h.proc, itoa(pid))
	h.file(d, "cgroup", cgroupLine+"\n")
	h.file(filepath.Join(d, "net"), "dev", `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:   99999      10    0    0    0     0          0         0    99999      10    0    0    0     0       0          0
  eth0:  123456     100    0    0    0     0          0         0    65432      80    0    0    0     0       0          0
`)
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestReadDockerViaPID(t *testing.T) {
	h := newHost(t)
	id := strings.Repeat("ab", 32)
	h.container("system.slice/docker-"+id+".scope", 2_500_000)
	h.proc0(4242, "0::/system.slice/docker-"+id+".scope")
	r := &Reader{Root: h.root, Proc: h.proc}
	if !r.Available() {
		t.Fatal("cgroup v2 not detected")
	}
	in := engine.Instance{Engine: engine.Docker, ID: id, Name: "web", PID: 4242}
	dir, err := r.Resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	s, err := r.Read(dir, in.PID, now)
	if err != nil {
		t.Fatal(err)
	}
	want := engine.Sample{
		Time: now, Has: engine.HasCPU | engine.HasMem | engine.HasNet | engine.HasBlk | engine.HasPIDs,
		CPU: 2500 * time.Millisecond, CPULimit: 1.5, MemUsed: 500_000_000, MemLimit: 0,
		NetRx: 123456, NetTx: 65432, BlkRead: 1024, BlkWrite: 2048, PIDs: 12,
	}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("\n got %+v\nwant %+v", s, want)
	}
}

func TestResolveLayouts(t *testing.T) {
	h := newHost(t)
	r := &Reader{Root: h.root, Proc: h.proc, UID: 1000}
	id := strings.Repeat("cd", 32)
	cases := []struct {
		in  engine.Instance
		rel string
	}{
		{engine.Instance{Engine: engine.Docker, ID: id}, "docker/" + id},
		{engine.Instance{Engine: engine.Docker, ID: id + "x"}, "user.slice/user-1000.slice/user@1000.service/app.slice/docker.service/docker-" + id + "x.scope"},
		{engine.Instance{Engine: engine.Incus, Name: "web-lab", Project: "default"}, "lxc.payload.web-lab"},
		{engine.Instance{Engine: engine.Incus, Name: "dev", Project: "lab"}, "lxc.payload.lab_dev"},
	}
	for _, c := range cases {
		want := h.container(c.rel, 1)
		got, err := r.Resolve(c.in)
		if err != nil || got != want {
			t.Errorf("%+v: %q %v, want %q", c.in, got, err, want)
		}
	}
	if _, err := r.Resolve(engine.Instance{Engine: engine.Incus, Name: "vm", Kind: engine.KindVM}); !errors.Is(err, ErrNoCgroup) {
		t.Errorf("VMs must not be read through cgroups: %v", err)
	}
	if _, err := r.Resolve(engine.Instance{Engine: engine.Docker, ID: "missing"}); !errors.Is(err, ErrNoCgroup) {
		t.Errorf("missing: %v", err)
	}
}

func TestIncusPIDInsideNestedScope(t *testing.T) {
	h := newHost(t)
	want := h.container("lxc.payload.web-lab", 5)
	h.file(filepath.Join(want, "init.scope"), "cpu.stat", "usage_usec 1\n")
	h.proc0(77, "0::/lxc.payload.web-lab/init.scope")
	r := &Reader{Root: h.root, Proc: h.proc}
	got, err := r.Resolve(engine.Instance{Engine: engine.Incus, Name: "web-lab", PID: 77})
	if err != nil || got != want {
		t.Fatalf("%q %v, want %q", got, err, want)
	}
}

func TestReadGoneCgroup(t *testing.T) {
	r := &Reader{Root: t.TempDir(), Proc: t.TempDir()}
	if _, err := r.Read(filepath.Join(r.Root, "gone"), 0, time.Now()); err == nil {
		t.Fatal("reading a removed cgroup must fail")
	}
}

func TestParsers(t *testing.T) {
	if parseCPUMax("max 100000") != 0 || parseCPUMax("50000 100000") != 0.5 || parseCPUMax("junk") != 0 {
		t.Error("parseCPUMax")
	}
}

func FuzzParsers(f *testing.F) {
	f.Add("259:0 rbytes=1 wbytes=2\n", " eth0: 1 2 3 4 5 6 7 8 9 10\n", "100 200")
	f.Fuzz(func(t *testing.T, io, net, max string) {
		_, _ = parseIOStat([]byte(io))
		_, _ = parseNetDev([]byte(net))
		if v := parseCPUMax(max); v < 0 && !strings.HasPrefix(max, "-") {
			t.Fatalf("negative limit %v from %q", v, max)
		}
	})
}
