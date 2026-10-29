package sampler

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics/cgroup"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/store"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// src adapts a fake engine to Source.
type src struct {
	e     *fake.Engine
	down  bool
	pids  map[string]int
	fixed []engine.Instance // when set, Running returns it as is
}

func (s *src) Engine() engine.Engine { return s.e }
func (s *src) Connected() bool       { return !s.down }
func (s *src) Running() []engine.Instance {
	if s.fixed != nil {
		return s.fixed
	}
	all, _ := s.e.ListInstances(context.Background(), engine.ListOptions{})
	var out []engine.Instance
	for _, in := range all {
		if in.State == engine.StateRunning {
			in.PID = s.pids[in.Name]
			out = append(out, in)
		}
	}
	return out
}

func world() (*clock.Fake, *fake.World) {
	clk := clock.NewFake(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	w := fake.NewWorld(clk)
	fake.Homelab(w)
	return clk, w
}

func TestAPIPassComputesRates(t *testing.T) {
	clk, w := world()
	d, i := &src{e: w.Docker}, &src{e: w.Incus}
	s := New(Options{Clock: clk}, d, i)
	ctx := context.Background()
	first := s.Pass(ctx)
	if len(first.Points) != 0 {
		t.Fatal("the first pass has nothing to compare with")
	}
	clk.Advance(time.Second)
	m := s.Pass(ctx)
	api := engine.MakeKey(engine.Docker, w.Docker.InstanceID("", "web-api"))
	p, ok := m.Points[api]
	if !ok || p.CPU <= 0 || p.MemLimit != 1<<30 || p.NetRx <= 0 {
		t.Fatalf("web-api point %+v", p)
	}
	if h := m.Health[engine.Docker]; h.Source != store.SourceAPI || h.Instances != 6 {
		t.Fatalf("docker health %+v", h)
	}
	if _, ok := m.Points[engine.MakeKey(engine.Docker, w.Docker.InstanceID("", "postgres-dev"))]; ok {
		t.Fatal("stopped instances are not sampled")
	}
	// A disconnected engine is skipped and reported.
	i.down = true
	w.Incus.Disconnect()
	clk.Advance(time.Second)
	m = s.Pass(ctx)
	if h := m.Health[engine.Incus]; h.Source != "" {
		t.Fatalf("incus health %+v", h)
	}
	for k := range m.Points {
		if k.Engine() == engine.Incus {
			t.Fatal("points from a disconnected engine")
		}
	}
}

func TestStatsFailureIsReported(t *testing.T) {
	clk, w := world()
	d := &src{e: w.Docker}
	d.fixed = d.Running() // as last reported by the supervisor
	s := New(Options{Clock: clk}, d)
	w.Docker.Disconnect() // the supervisor has not noticed yet
	m := s.Pass(context.Background())
	if h := m.Health[engine.Docker]; h.Failing != 1 || h.Err == "" {
		t.Fatalf("health %+v", h)
	}
}

func TestCgroupPathPreferredAndFallsBack(t *testing.T) {
	clk, w := world()
	root, proc := t.TempDir(), t.TempDir()
	write := func(dir, name, content string) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(root, "cgroup.controllers", "cpu")
	id := w.Docker.InstanceID("", "web-api")
	dir := filepath.Join(root, "system.slice", "docker-"+id+".scope")
	usec := 1_000_000
	writeCPU := func() { write(dir, "cpu.stat", "usage_usec "+strconv.Itoa(usec)+"\n") }
	writeCPU()
	write(dir, "memory.current", "1000\n")
	write(filepath.Join(proc, "4242"), "cgroup", "0::/system.slice/docker-"+id+".scope\n")
	d := &src{e: w.Docker, pids: map[string]int{"web-api": 4242}}
	s := New(Options{Clock: clk, Cgroup: &cgroup.Reader{Root: root, Proc: proc}}, d)
	ctx := context.Background()
	s.Pass(ctx)
	clk.Advance(time.Second)
	usec += 500_000
	writeCPU()
	m := s.Pass(ctx)
	k := engine.MakeKey(engine.Docker, id)
	if p := m.Points[k]; p.CPU != 50 || p.MemUsed != 1000 {
		t.Fatalf("cgroup point %+v", p)
	}
	if h := m.Health[engine.Docker]; h.Source != store.SourceMixed {
		t.Fatalf("source %q, want cgroup for web-api and api for the rest", h.Source)
	}
	// The cgroup disappears: the instance moves to the API path.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Second)
	m = s.Pass(ctx)
	if _, ok := m.Points[k]; ok {
		t.Fatal("no rate across a source switch")
	}
	clk.Advance(time.Second)
	m = s.Pass(ctx)
	if _, ok := m.Points[k]; !ok {
		t.Fatal("API fallback must produce points")
	}
}

func TestPublishKeepsOnlyTheNewest(t *testing.T) {
	s := New(Options{})
	for i := range 3 {
		s.publish(store.MetricsSampled{At: time.Unix(int64(i), 0)})
	}
	if got := <-s.Out(); got.At.Unix() != 2 {
		t.Fatalf("delivered pass %v, want the newest", got.At)
	}
}

func TestRunAndSetInterval(t *testing.T) {
	clk, w := world()
	s := New(Options{Clock: clk, Interval: time.Second}, &src{e: w.Docker})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	defer func() { cancel(); <-done }()
	recv := func() store.MetricsSampled {
		t.Helper()
		select {
		case m := <-s.Out():
			return m
		case <-time.After(5 * time.Second):
			t.Fatal("no pass")
		}
		return store.MetricsSampled{}
	}
	recv() // the first pass runs at once
	s.SetInterval(5 * time.Second)
	if m := recv(); m.Health[engine.Docker].Interval != 5*time.Second {
		t.Fatalf("interval %v", m.Health[engine.Docker].Interval)
	}
}

// BenchmarkCgroupPass50 is one sampling pass over 50 running containers
// read through cgroup files. Budget: well under 20 ms of CPU, i.e. < 2 % of
// one core at 1 Hz (docs/05-quality-plan.md §4.1).
func BenchmarkCgroupPass50(b *testing.B) {
	clk := clock.NewFake(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	w := fake.NewWorld(clk)
	root, proc := b.TempDir(), b.TempDir()
	write := func(dir, name, content string) {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
	}
	write(root, "cgroup.controllers", "cpu io memory pids")
	pids := map[string]int{}
	for i := range 50 {
		name := "svc-" + strconv.Itoa(i)
		in := w.Docker.Add(engine.Instance{Name: name, State: engine.StateRunning})
		dir := filepath.Join(root, "system.slice", "docker-"+in.ID+".scope")
		write(dir, "cpu.stat", "usage_usec 123456\nuser_usec 1\nsystem_usec 1\n")
		write(dir, "cpu.max", "max 100000\n")
		write(dir, "memory.current", "104857600\n")
		write(dir, "memory.stat", "anon 1\ninactive_file 1048576\n")
		write(dir, "memory.max", "max\n")
		write(dir, "io.stat", "259:0 rbytes=1 wbytes=2 rios=1 wios=1\n")
		write(dir, "pids.current", "4\n")
		pid := 1000 + i
		pids[name] = pid
		write(filepath.Join(proc, strconv.Itoa(pid)), "cgroup", "0::/system.slice/docker-"+in.ID+".scope\n")
		write(filepath.Join(proc, strconv.Itoa(pid), "net"), "dev", "  eth0: 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16\n")
	}
	s := New(Options{Clock: clk, Cgroup: &cgroup.Reader{Root: root, Proc: proc}}, &src{e: w.Docker, pids: pids})
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		clk.Advance(time.Second)
		m := s.Pass(ctx)
		if h := m.Health[engine.Docker]; h.Source != store.SourceCgroup {
			b.Fatalf("source %q", h.Source)
		}
	}
}
