package docker

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/blokxz/viewDI/internal/engine"
)

const oldID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func oldContainer() container.InspectResponse {
	return container.InspectResponse{
		ID: oldID, Name: "/web", Image: "sha256:oldimage",
		State: &container.State{Running: true, Status: "running"},
		Config: &container.Config{
			Hostname: oldID[:12], Image: "nginx:1.27",
			Env:    []string{"PATH=/usr/bin", "NGINX_VERSION=1.27.2", "APP_MODE=prod"},
			Cmd:    []string{"nginx", "-g", "daemon off;"},
			Labels: map[string]string{"maintainer": "nginx", "tier": "web"},
			ExposedPorts: network.PortSet{
				network.MustParsePort("80/tcp"): {}, network.MustParsePort("443/tcp"): {},
			},
			Volumes: map[string]struct{}{"/var/cache/nginx": {}},
		},
		HostConfig: &container.HostConfig{
			Binds:         []string{"/srv/www:/usr/share/nginx/html:ro"},
			NetworkMode:   "web_net",
			RestartPolicy: container.RestartPolicy{Name: "always"},
			PortBindings:  network.PortMap{network.MustParsePort("80/tcp"): {{HostPort: "8080"}}},
		},
		Mounts: []container.MountPoint{
			{Type: mount.TypeBind, Source: "/srv/www", Destination: "/usr/share/nginx/html"},
			{Type: mount.TypeVolume, Name: "3f9a0c", Destination: "/var/cache/nginx", RW: true},
		},
		NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{
			"web_net": {Aliases: []string{"proxy", oldID[:12]}, EndpointID: "ep1", NetworkID: "n1"},
		}},
	}
}

func oldImage() *dockerspec.DockerOCIImageConfig {
	return &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{
		Env:          []string{"PATH=/usr/bin", "NGINX_VERSION=1.27.2"},
		Cmd:          []string{"nginx", "-g", "daemon off;"},
		Labels:       map[string]string{"maintainer": "nginx"},
		ExposedPorts: map[string]struct{}{"80/tcp": {}, "443/tcp": {}},
		Volumes:      map[string]struct{}{"/var/cache/nginx": {}},
	}}
}

func TestRecreateConfig(t *testing.T) {
	cfg, host, netcfg := recreateConfig(oldContainer(), oldImage())
	p80, p443 := network.MustParsePort("80/tcp"), network.MustParsePort("443/tcp")
	_, has80 := cfg.ExposedPorts[p80]
	_, has443 := cfg.ExposedPorts[p443]
	switch {
	case !slices.Equal(cfg.Env, []string{"APP_MODE=prod"}):
		t.Errorf("env %v: the image's defaults must go, the user's stay", cfg.Env)
	case cfg.Cmd != nil:
		t.Errorf("cmd %v: equal to the image's, the new image's applies", cfg.Cmd)
	case cfg.Labels["tier"] != "web" || cfg.Labels["maintainer"] != "":
		t.Errorf("labels %v", cfg.Labels)
	case !has80 || has443:
		t.Errorf("exposed %v: bound ports stay, image-only ones go", cfg.ExposedPorts)
	case cfg.Hostname != "":
		t.Errorf("hostname %q: the default (old ID) must not carry over", cfg.Hostname)
	case cfg.Image != "nginx:1.27":
		t.Errorf("image %q", cfg.Image)
	case string(host.RestartPolicy.Name) != "always" || len(host.PortBindings[p80]) != 1 || len(host.Binds) != 1:
		t.Errorf("host %+v", host)
	case len(host.Mounts) != 1 || host.Mounts[0].Source != "3f9a0c" || host.Mounts[0].Target != "/var/cache/nginx" || host.Mounts[0].ReadOnly:
		t.Errorf("anonymous volume not carried over: %+v", host.Mounts)
	case netcfg == nil || netcfg.EndpointsConfig["web_net"] == nil:
		t.Fatalf("networks %+v", netcfg)
	}
	ep := netcfg.EndpointsConfig["web_net"]
	if !slices.Equal(ep.Aliases, []string{"proxy"}) || ep.EndpointID != "" {
		t.Errorf("endpoint %+v", ep)
	}
	// The input is not modified.
	if old := oldContainer(); len(old.Config.Env) != 3 {
		t.Error("recreateConfig changed its input")
	}
	// Host networking has no endpoints to configure.
	o := oldContainer()
	o.HostConfig.NetworkMode = "host"
	if _, _, n := recreateConfig(o, nil); n != nil {
		t.Errorf("host network: %+v", n)
	}
}

// fakeDaemon is a tiny stateful Docker API for the recreate steps.
type fakeDaemon struct {
	mu         sync.Mutex
	ctrs       map[string]*container.InspectResponse // by ID
	created    *container.CreateRequest
	failCreate bool
	exitAtOnce bool // the new container exits right after start
	calls      []string
}

var verPrefix = regexp.MustCompile(`^/v[0-9.]+`)

func (d *fakeDaemon) find(ref string) *container.InspectResponse {
	if c, ok := d.ctrs[ref]; ok {
		return c
	}
	for _, c := range d.ctrs {
		if c.Name == "/"+ref {
			return c
		}
	}
	return nil
}

func (d *fakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	path := verPrefix.ReplaceAllString(r.URL.Path, "")
	d.calls = append(d.calls, r.Method+" "+path)
	w.Header().Set("Api-Version", "1.52")
	w.Header().Set("Content-Type", "application/json")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	notFound := func() { w.WriteHeader(http.StatusNotFound); _, _ = w.Write([]byte(`{"message":"No such container"}`)) }
	switch {
	case path == "/_ping":
		_, _ = w.Write([]byte("OK"))
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "images":
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:oldimage", "Config": oldImage()})
	case r.Method == http.MethodPost && path == "/containers/create":
		name := r.URL.Query().Get("name")
		if d.failCreate || d.find(name) != nil {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"Conflict. The container name is already in use"}`))
			return
		}
		var req container.CreateRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		d.created = &req
		id := strings.Repeat("f", 64)
		d.ctrs[id] = &container.InspectResponse{ID: id, Name: "/" + name, State: &container.State{Status: "created"}, Config: req.Config, HostConfig: req.HostConfig}
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": id})
	case len(parts) >= 2 && parts[0] == "containers":
		c := d.find(parts[1])
		if c == nil {
			notFound()
			return
		}
		switch {
		case r.Method == http.MethodGet && len(parts) == 3 && parts[2] == "json":
			_ = json.NewEncoder(w).Encode(c)
		case r.Method == http.MethodPost && parts[2] == "stop":
			c.State = &container.State{Status: "exited"}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && parts[2] == "start":
			c.State = &container.State{Running: true, Status: "running"}
			if d.exitAtOnce && c.ID != oldID {
				c.State = &container.State{Status: "exited", ExitCode: 1}
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && parts[2] == "rename":
			c.Name = "/" + r.URL.Query().Get("name")
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete:
			delete(d.ctrs, c.ID)
			w.WriteHeader(http.StatusNoContent)
		default:
			notFound()
		}
	default:
		notFound()
	}
}

func daemon(t *testing.T, d *fakeDaemon) *Adapter {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "docker.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(d)
	srv.Listener = l
	srv.Start()
	a, err := New("unix://" + sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(); srv.Close() })
	return a
}

func TestRecreate(t *testing.T) {
	startedWait = 10 * time.Millisecond
	t.Cleanup(func() { startedWait = 2 * time.Second })
	newDaemon := func() *fakeDaemon {
		c := oldContainer()
		return &fakeDaemon{ctrs: map[string]*container.InspectResponse{oldID: &c}}
	}
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		d := newDaemon()
		a := daemon(t, d)
		var phases []string
		id, err := a.Recreate(ctx, oldID, engine.RecreateOptions{Timeout: time.Second}, func(p engine.Progress) { phases = append(phases, p.Phase) })
		if err != nil {
			t.Fatal(err)
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		n := d.find("web")
		switch {
		case n == nil || n.ID != id || !n.State.Running:
			t.Errorf("new container %+v (id %s)", n, id)
		case d.ctrs[oldID] != nil:
			t.Error("the old container was not removed")
		case d.created == nil || !slices.Equal(d.created.Env, []string{"APP_MODE=prod"}):
			t.Errorf("create request %+v", d.created)
		}
		want := []string{engine.PhaseStopping, engine.PhaseRenaming, engine.PhaseCreating, engine.PhaseStarting, engine.PhaseRemoving}
		if !slices.Equal(phases, want) {
			t.Errorf("phases %v, want %v", phases, want)
		}
	})

	t.Run("new container exits: rollback", func(t *testing.T) {
		d := newDaemon()
		d.exitAtOnce = true
		a := daemon(t, d)
		_, err := a.Recreate(ctx, oldID, engine.RecreateOptions{}, nil)
		if engine.Classify(err) != engine.ClassConflict || !strings.Contains(err.Error(), "exited") {
			t.Fatalf("err %v", err)
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		old := d.find("web")
		switch {
		case old == nil || old.ID != oldID:
			t.Errorf("the original does not have its name back: %+v", old)
		case !old.State.Running:
			t.Errorf("the original is not running again: %+v", old.State)
		case len(d.ctrs) != 1:
			t.Errorf("the new container was not removed: %d containers", len(d.ctrs))
		}
	})

	t.Run("create fails: rollback", func(t *testing.T) {
		d := newDaemon()
		d.failCreate = true
		a := daemon(t, d)
		if _, err := a.Recreate(ctx, oldID, engine.RecreateOptions{}, nil); err == nil {
			t.Fatal("recreate succeeded")
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if old := d.find("web"); old == nil || old.ID != oldID || !old.State.Running {
			t.Errorf("original after rollback: %+v", old)
		}
	})

	t.Run("compose-managed", func(t *testing.T) {
		d := newDaemon()
		d.ctrs[oldID].Config.Labels = map[string]string{engine.ComposeProjectLabel: "web", engine.ComposeServiceLabel: "proxy"}
		a := daemon(t, d)
		_, err := a.Recreate(ctx, oldID, engine.RecreateOptions{}, nil)
		if !errors.Is(err, engine.ErrUnsupported) || !strings.Contains(err.Error(), "docker compose -p web up -d proxy") {
			t.Fatalf("err %v", err)
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		for _, c := range d.calls {
			if strings.HasPrefix(c, "POST") {
				t.Errorf("a compose container was touched: %s", c)
			}
		}
	})
}

func TestRefreshImageUnsupported(t *testing.T) {
	var a Adapter
	if _, err := a.RefreshImage(context.Background(), engine.ImageCheck{Ref: "nginx"}, nil); !errors.Is(err, engine.ErrUnsupported) {
		t.Errorf("err %v", err)
	}
}
