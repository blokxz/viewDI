package incus

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"go.uber.org/goleak"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/engine/enginetest"
	"github.com/blokxz/viewDI/internal/fixtures"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func fixtureSets(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob("../../../testdata/fixtures/incus/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no incus fixtures: %v", err)
	}
	return dirs
}

func serve(t *testing.T, dir string) (*fixtures.Server, *Adapter) {
	t.Helper()
	srv := fixtures.Serve(t, dir)
	a, err := New(context.Background(), srv.Socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = a.Close()
		if m := srv.Misses(); len(m) > 0 {
			t.Logf("requests without fixture: %v", m)
		}
	})
	return srv, a
}

func TestContract(t *testing.T) {
	for _, dir := range fixtureSets(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			var srv *fixtures.Server
			enginetest.Run(t, enginetest.Harness{
				New: func(t *testing.T) engine.Engine {
					var a *Adapter
					srv, a = serve(t, dir)
					return a
				},
				Expect: enginetest.Expect{
					Engine: engine.Incus,
					Instances: []enginetest.Instance{
						{Name: "web-lab", Project: "default", Kind: engine.KindContainer, State: engine.StateRunning},
						{Name: "k3s-node1", Project: "default", Kind: engine.KindVM, State: engine.StateRunning},
						{Name: "test-alpine", Project: "default", Kind: engine.KindContainer, State: engine.StateStopped},
						{Name: "ci-runner", Project: "lab", Kind: engine.KindContainer, State: engine.StatePaused},
					},
					Networks: []string{"incusbr0", "macvlan0"},
					Volumes:  []string{"backups", "isos"},
					Toggle:   "web-lab",
					Projects: []string{"default", "lab"},
					Detail:   enginetest.Instance{Name: "web-lab", Project: "default"},
				},
				Trigger: func(*testing.T, engine.Engine) { srv.Emit() },
			})
		})
	}
}

func TestMapping(t *testing.T) {
	_, a := serve(t, fixtureSets(t)[0])
	ctx := context.Background()
	in, err := a.Inspect(ctx, "default/web-lab")
	if err != nil {
		t.Fatal(err)
	}
	x := in.Extra.(*engine.IncusExtra)
	switch {
	case in.Image != "debian/12":
		t.Errorf("image %q", in.Image)
	case len(in.Networks) != 1 || in.Networks[0].Network != "incusbr0" || in.Networks[0].IPv4 != "10.10.0.21" || in.Networks[0].IPv6 != "":
		t.Errorf("networks %+v (link-local addresses must be skipped)", in.Networks)
	case len(in.Ports) != 1 || in.Ports[0] != (engine.Port{HostIP: "0.0.0.0", HostPort: 8081, ContainerPort: 80, Protocol: "tcp"}):
		t.Errorf("ports %+v", in.Ports)
	case len(in.Mounts) != 1 || in.Mounts[0].Source != "default/backups" || in.Mounts[0].Destination != "/srv/backups":
		t.Errorf("mounts %+v", in.Mounts)
	case in.Labels["owner"] != "homelab":
		t.Errorf("labels %v", in.Labels)
	case x.Location != "none" || len(x.Profiles) != 1 || len(x.BaseImage) != 64:
		t.Errorf("extra %+v", x)
	case !in.Started.Equal(time.Date(2026, 10, 29, 6, 0, 0, 250_000_000, time.UTC)):
		t.Errorf("started %v", in.Started)
	}

	info, err := a.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "6.0.5" || !info.Capabilities.AllProjects || !info.Capabilities.VMs || info.Capabilities.Restricted || info.Host != "viewdi-host" {
		t.Errorf("info %+v", info)
	}

	nets, _ := a.Networks(ctx, engine.ListOptions{})
	for _, n := range nets {
		if n.Name == "lo" || n.Name == "eno1" {
			t.Errorf("unused unmanaged interface %q listed", n.Name)
		}
		if n.Name == "incusbr0" && (n.Gateway != "10.10.0.1" || len(n.Subnets) != 2) {
			t.Errorf("incusbr0 %+v", n)
		}
	}
	vols, _ := a.Volumes(ctx, engine.ListOptions{})
	if len(vols) != 2 {
		t.Errorf("only custom volumes must be listed: %+v", vols)
	}
}

func TestEventFilterAndRename(t *testing.T) {
	srv, a := serve(t, fixtureSets(t)[0])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feed, err := a.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}
	srv.Emit()
	want := []struct {
		action, id string
		kind       engine.EventKind
	}{
		{"instance-shutdown", "default/web-lab", engine.EventChanged},
		{"instance-stopped", "default/web-lab", engine.EventChanged},
		{"instance-started", "default/web-lab", engine.EventChanged},
		{"instance-renamed", "lab/dev-arch", engine.EventRemoved},
		{"instance-renamed", "lab/dev-box", engine.EventChanged},
	}
	for _, w := range want {
		select {
		case ev := <-feed.C():
			if ev.Action != w.action || ev.InstanceID != w.id || ev.Kind != w.kind {
				t.Fatalf("got %+v, want %+v", ev, w)
			}
		case <-time.After(enginetest.Timeout):
			t.Fatalf("timeout waiting for %+v", w)
		}
	}
}

func TestErrorClasses(t *testing.T) {
	dir := t.TempDir()
	a, err := New(context.Background(), filepath.Join(dir, "unix.socket"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Ping(context.Background()); engine.Classify(err) != engine.ClassMissing {
		t.Errorf("missing socket: %v (%v)", err, engine.Classify(err))
	}
	if _, err := a.Events(context.Background()); engine.Classify(err) != engine.ClassMissing {
		t.Errorf("events on missing socket: %v (%v)", err, engine.Classify(err))
	}
}

func TestPermissionClass(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores socket permissions")
	}
	srv := fixtures.Serve(t, fixtureSets(t)[0])
	if err := os.Chmod(srv.Socket, 0); err != nil {
		t.Fatal(err)
	}
	a, _ := New(context.Background(), srv.Socket)
	defer a.Close()
	if err := a.Ping(context.Background()); engine.Classify(err) != engine.ClassPermission {
		t.Fatalf("EACCES: %v (%v)", err, engine.Classify(err))
	}
}

func TestDiscover(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	type perm struct{ exists, writable bool }
	fs := map[string]perm{}
	probe := func(p string) (bool, bool) { return fs[p].exists, fs[p].writable }

	if p, s := Discover("/flag.sock", "/cfg", getenv, probe); p != "/flag.sock" || s != SourceFlag {
		t.Errorf("flag: %s %s", p, s)
	}
	env["INCUS_SOCKET"] = "/env.sock"
	if p, s := Discover("", "/cfg", getenv, probe); p != "/env.sock" || s != SourceEnv {
		t.Errorf("env: %s %s", p, s)
	}
	delete(env, "INCUS_SOCKET")
	if p, s := Discover("", "/cfg", getenv, probe); p != "/cfg" || s != SourceConfig {
		t.Errorf("config: %s %s", p, s)
	}
	// Nothing installed: the default path, so the hint can name it.
	if p, _ := Discover("", "", getenv, probe); p != "/var/lib/incus/unix.socket" {
		t.Errorf("default: %s", p)
	}
	// Member of "incus" only: the user socket.
	fs["/var/lib/incus/unix.socket"] = perm{true, false}
	fs["/var/lib/incus/unix.socket.user"] = perm{true, true}
	if p, s := Discover("", "", getenv, probe); p != "/var/lib/incus/unix.socket.user" || s != SourceUser || !IsUserSocket(p) {
		t.Errorf("user: %s %s", p, s)
	}
	// Member of no group: the full socket, which yields the permission hint.
	fs["/var/lib/incus/unix.socket.user"] = perm{true, false}
	if p, _ := Discover("", "", getenv, probe); p != "/var/lib/incus/unix.socket" {
		t.Errorf("no access: %s", p)
	}
	// incus-admin: the full socket wins.
	fs["/var/lib/incus/unix.socket"] = perm{true, true}
	fs["/var/lib/incus/unix.socket.user"] = perm{true, true}
	if p, _ := Discover("", "", getenv, probe); p != "/var/lib/incus/unix.socket" {
		t.Errorf("admin: %s", p)
	}
	// INCUS_DIR moves both sockets.
	env["INCUS_DIR"] = "/opt/incus"
	fs["/opt/incus/unix.socket"] = perm{true, true}
	if p, _ := Discover("", "", getenv, probe); p != "/opt/incus/unix.socket" {
		t.Errorf("INCUS_DIR: %s", p)
	}
}

func TestProxyPortAndImageName(t *testing.T) {
	if _, ok := proxyPort("tcp:0.0.0.0", "tcp:127.0.0.1:80"); ok {
		t.Error("malformed listen accepted")
	}
	p, ok := proxyPort("udp:[::]:53", "udp:127.0.0.1:5353")
	if !ok || p.HostIP != "::" || p.HostPort != 53 || p.ContainerPort != 5353 || p.Protocol != "udp" {
		t.Errorf("udp v6: %+v %v", p, ok)
	}
	if imageName(map[string]string{"image.description": "Custom"}) != "Custom" || imageName(nil) != "" {
		t.Error("imageName fallbacks")
	}
}

func TestStatsFromMetrics(t *testing.T) {
	_, a := serve(t, fixtureSets(t)[0])
	ctx := context.Background()
	samples, err := a.Stats(ctx, []string{"default/web-lab", "default/k3s-node1", "lab/nope"})
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 {
		t.Fatalf("samples %+v", samples)
	}
	s := samples[0]
	if s.InstanceID != "default/web-lab" || s.CPU != 150750*time.Millisecond || s.MemUsed != 1073741824-980000000 ||
		s.MemLimit != 1<<30 || s.NetRx != 52428800 || s.NetTx != 10485760 || s.BlkWrite != 4194304 || s.PIDs != 23 || s.CPULimit != 2 {
		t.Errorf("web-lab sample %+v", s)
	}
	info, _ := a.Info(ctx)
	if info.NCPU != 12 || info.MemTotal != 33554432000 || info.Images != 2 {
		t.Errorf("info host figures %+v", info)
	}
	in, _ := a.Inspect(ctx, "default/web-lab")
	if in.PID != 4321 {
		t.Errorf("PID %d", in.PID)
	}
}

func TestStatsFromState(t *testing.T) {
	st := &api.InstanceState{
		StatusCode: api.Running, Processes: 5,
		CPU:    api.InstanceStateCPU{Usage: 2_000_000_000},
		Memory: api.InstanceStateMemory{Usage: 100, Total: 1000},
		Network: map[string]api.InstanceStateNetwork{
			"eth0": {Counters: api.InstanceStateNetworkCounters{BytesReceived: 10, BytesSent: 20}},
			"lo":   {Type: "loopback", Counters: api.InstanceStateNetworkCounters{BytesReceived: 99}},
		},
	}
	s, ok := fromState("default/x", st, time.Unix(0, 0))
	if !ok || s.CPU != 2*time.Second || s.MemUsed != 100 || s.NetRx != 10 || s.NetTx != 20 || s.Has&engine.HasBlk != 0 {
		t.Fatalf("%+v", s)
	}
	if _, ok := fromState("x", &api.InstanceState{StatusCode: api.Stopped}, time.Time{}); ok {
		t.Fatal("stopped instance sampled")
	}
}
