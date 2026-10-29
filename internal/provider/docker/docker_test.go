package docker

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/engine/enginetest"
	"github.com/blokxz/viewDI/internal/fixtures"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// fixtureSets lists every recorded API version under testdata.
func fixtureSets(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob("../../../testdata/fixtures/docker/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no docker fixtures: %v", err)
	}
	return dirs
}

func serve(t *testing.T, dir string) (*fixtures.Server, *Adapter) {
	t.Helper()
	srv := fixtures.Serve(t, dir)
	a, err := New("unix://" + srv.Socket)
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
					Engine: engine.Docker,
					Instances: []enginetest.Instance{
						{Name: "web-api", Kind: engine.KindContainer, State: engine.StateRunning},
						{Name: "web-db", Kind: engine.KindContainer, State: engine.StateRunning},
						{Name: "postgres-dev", Kind: engine.KindContainer, State: engine.StateStopped},
						{Name: "builder", Kind: engine.KindContainer, State: engine.StateCreated},
					},
					Networks: []string{"bridge", "web_default"},
					Volumes:  []string{"web_dbdata"},
					Toggle:   "web-api",
				},
				Trigger: func(*testing.T, engine.Engine) { srv.Emit() },
			})
		})
	}
}

func TestMapping(t *testing.T) {
	_, a := serve(t, fixtureSets(t)[0])
	ctx := context.Background()
	list, err := a.ListInstances(ctx, engine.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var api engine.Instance
	for _, in := range list {
		if in.Name == "web-api" {
			api = in
		}
	}
	x, _ := api.Extra.(*engine.DockerExtra)
	switch {
	case api.Health != engine.HealthHealthy:
		t.Errorf("health %v", api.Health)
	case len(api.Networks) != 2 || api.Networks[0].Network != "monitoring" || api.Networks[1].IPv4 != "10.1.0.3":
		t.Errorf("networks %+v", api.Networks)
	case len(api.Ports) != 2 || api.Ports[0].HostPort != 8080:
		t.Errorf("ports %+v", api.Ports)
	case len(api.Mounts) != 1 || !api.Mounts[0].ReadOnly:
		t.Errorf("mounts %+v", api.Mounts)
	case x == nil || x.ComposeProject != "web" || x.ComposeService != "api":
		t.Errorf("extra %+v", x)
	case api.Started.IsZero() || api.Started.Year() != 2026:
		t.Errorf("started %v", api.Started)
	}
	// Inspect and list agree on ports and networks, so an event-driven
	// re-inspect never makes the table flicker.
	got, err := a.Inspect(ctx, api.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Ports) != len(api.Ports) || got.Ports[1] != api.Ports[1] || len(got.Networks) != 2 {
		t.Errorf("inspect ports %+v networks %+v", got.Ports, got.Networks)
	}
	if gx := got.Extra.(*engine.DockerExtra); gx.RestartPolicy != "unless-stopped" {
		t.Errorf("restart policy %q", gx.RestartPolicy)
	}

	info, err := a.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "29.1.2" || info.APIVersion != "1.52" || info.CgroupVersion != "2" || info.Rootless {
		t.Errorf("info %+v", info)
	}
}

func TestEventFilter(t *testing.T) {
	srv, a := serve(t, fixtureSets(t)[0])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feed, err := a.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}
	srv.Emit()
	// exec_create and network events are filtered out.
	want := []string{"kill", "die", "stop", "start"}
	for _, w := range want {
		select {
		case ev := <-feed.C():
			if ev.Action != w || ev.Name != "web-api" {
				t.Fatalf("got %+v, want %s", ev, w)
			}
		case <-time.After(enginetest.Timeout):
			t.Fatalf("timeout waiting for %s", w)
		}
	}
}

func TestErrorClasses(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "docker.sock")
	a, err := New("unix://" + missing)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Ping(context.Background()); engine.Classify(err) != engine.ClassMissing {
		t.Errorf("missing socket: %v (%v)", err, engine.Classify(err))
	}
	if _, err := a.Events(context.Background()); engine.Classify(err) != engine.ClassMissing {
		t.Errorf("events on missing socket: %v", err)
	}

	// A stale socket file nobody listens on: connection refused.
	if err := os.WriteFile(filepath.Join(dir, "dead.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	b, _ := New("unix://" + filepath.Join(dir, "dead.sock"))
	defer b.Close()
	if err := b.Ping(context.Background()); engine.Classify(err) != engine.ClassTransient {
		t.Errorf("refused: %v (%v)", err, engine.Classify(err))
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
	a, _ := New("unix://" + srv.Socket)
	defer a.Close()
	err := a.Ping(context.Background())
	if engine.Classify(err) != engine.ClassPermission {
		t.Fatalf("EACCES: %v (%v)", err, engine.Classify(err))
	}
}

func TestDiscover(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	runtime := t.TempDir()

	if h, s := Discover("tcp://1.2.3.4:2375", "x", runtime, getenv); h != "tcp://1.2.3.4:2375" || s != SourceFlag {
		t.Errorf("flag: %s %s", h, s)
	}
	env["DOCKER_HOST"] = "unix:///env.sock"
	if h, s := Discover("", "unix:///cfg.sock", runtime, getenv); h != "unix:///env.sock" || s != SourceEnv {
		t.Errorf("env: %s %s", h, s)
	}
	delete(env, "DOCKER_HOST")
	if h, s := Discover("", "unix:///cfg.sock", runtime, getenv); h != "unix:///cfg.sock" || s != SourceConfig {
		t.Errorf("config: %s %s", h, s)
	}
	if h, s := Discover("", "", runtime, getenv); h != "unix://"+RootfulSocket || s != SourceRootful {
		t.Errorf("rootful: %s %s", h, s)
	}
	sock := filepath.Join(runtime, "docker.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if h, s := Discover("", "", runtime, getenv); h != "unix://"+sock || s != SourceRootless {
		t.Errorf("rootless: %s %s", h, s)
	}
}

func TestExitCodeAndHealthParsing(t *testing.T) {
	if exitCode("Exited (137) 5 minutes ago") != 137 || exitCode("Up 2 days") != 0 {
		t.Error("exitCode")
	}
	if healthFromStatus("Up 1 hour (health: starting)") != engine.HealthStarting || healthFromStatus("Up") != engine.HealthNone {
		t.Error("healthFromStatus")
	}
	if !strings.HasPrefix(image("sha256:0123456789abcdef0123"), "0123456789ab") {
		t.Error("image id shortening")
	}
}

func TestStatsMapping(t *testing.T) {
	_, a := serve(t, fixtureSets(t)[0])
	ctx := context.Background()
	list, _ := a.ListInstances(ctx, engine.ListOptions{})
	ids := make([]string, 0, len(list))
	byName := map[string]engine.Instance{}
	for _, in := range list {
		ids = append(ids, in.ID)
		byName[in.Name] = in
	}
	samples, err := a.Stats(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 {
		t.Fatalf("want samples for the 2 running containers, got %d", len(samples))
	}
	for _, s := range samples {
		if s.InstanceID != byName["web-api"].ID {
			continue
		}
		want := engine.Sample{
			InstanceID: s.InstanceID, Time: s.Time, Has: s.Has,
			CPU: 13573 * time.Second, MemUsed: 412_000_000 - 55_000_000, MemLimit: 1 << 30,
			NetRx: 7_340_032_000, NetTx: 2_400_000_000, BlkRead: 12_582_912, BlkWrite: 104_857_600, PIDs: 14,
		}
		if s != want {
			t.Errorf("web-api sample\n got %+v\nwant %+v", s, want)
		}
	}
	if pid := byName["web-api"].PID; pid != 4242 {
		t.Errorf("init PID from inspect: %d", pid)
	}
	info, _ := a.Info(ctx)
	if info.NCPU != 12 || info.MemTotal == 0 || info.Images != 12 {
		t.Errorf("info host figures %+v", info)
	}
}
