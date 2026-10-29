package fake

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
)

// World is a pair of fake engines sharing one clock.
type World struct {
	Clock  clock.Clock
	Docker *Engine
	Incus  *Engine
}

// NewWorld returns two empty engines on clk.
func NewWorld(clk clock.Clock) *World {
	if clk == nil {
		clk = clock.Real{}
	}
	return &World{Clock: clk, Docker: New(engine.Docker, clk), Incus: New(engine.Incus, clk)}
}

// Engine returns the fake for id.
func (w *World) Engine(id engine.ID) *Engine {
	if id == engine.Incus {
		return w.Incus
	}
	return w.Docker
}

// Engines returns both engines as the contract interface, Docker first.
func (w *World) Engines() []engine.Engine { return []engine.Engine{w.Docker, w.Incus} }

// imageID is a stable fake image digest.
func imageID(ref string) string {
	sum := sha256.Sum256([]byte("image:" + ref))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ago(clk clock.Clock, d time.Duration) time.Time { return clk.Now().Add(-d) }

const day = 24 * time.Hour

// Homelab populates the reference world used by the default --mock demo, the
// golden frames and the contract suite (docs/03-mockups.md).
func Homelab(w *World) {
	d, c := w.Docker, w.Clock
	for _, n := range []engine.Network{
		{Name: "bridge", Driver: "bridge", Scope: "local", Subnets: []string{"172.17.0.0/16"}, Gateway: "172.17.0.1", Managed: true},
		{Name: "host", Driver: "host", Scope: "local", Managed: true},
		{Name: "none", Driver: "null", Scope: "local", Managed: true},
		{Name: "web_default", Driver: "bridge", Scope: "local", Subnets: []string{"172.20.0.0/16"}, Gateway: "172.20.0.1", Managed: true},
		{Name: "monitoring", Driver: "bridge", Scope: "local", Subnets: []string{"172.21.0.0/16"}, Gateway: "172.21.0.1", Managed: true},
	} {
		d.AddNetwork(n)
	}
	for _, v := range []engine.Volume{
		{Name: "web_dbdata", Driver: "local", Mountpoint: "/var/lib/docker/volumes/web_dbdata/_data", UsedBy: []string{"web-db"}},
		{Name: "grafana-data", Driver: "local", Mountpoint: "/var/lib/docker/volumes/grafana-data/_data", UsedBy: []string{"grafana"}},
		{Name: "pgdev", Driver: "local", Mountpoint: "/var/lib/docker/volumes/pgdev/_data"},
	} {
		d.AddVolume(v)
	}
	compose := func(svc string) map[string]string {
		return map[string]string{"com.docker.compose.project": "web", "com.docker.compose.service": svc}
	}
	dockerCtr := func(name, image string, state engine.State, up time.Duration, health engine.Health, nets []engine.Attachment, labels map[string]string, extra engine.DockerExtra) engine.Instance {
		extra.ImageID = imageID(image)
		in := engine.Instance{
			Name: name, Image: image, State: state, Health: health, Kind: engine.KindContainer,
			Created: ago(c, up+time.Hour), Networks: nets, Labels: labels, Extra: &extra,
		}
		if state == engine.StateRunning {
			in.Started = ago(c, up)
		}
		return in
	}
	for _, in := range []engine.Instance{
		dockerCtr("web-frontend", "node:22-alpine", engine.StateRunning, 2*day, engine.HealthNone,
			[]engine.Attachment{{Network: "web_default", IPv4: "172.20.0.2"}}, compose("frontend"),
			engine.DockerExtra{ComposeProject: "web", ComposeService: "frontend", Command: "node server.js", RestartPolicy: "unless-stopped"}),
		dockerCtr("web-api", "ghcr.io/me/api:1.4", engine.StateRunning, 2*day+4*time.Hour, engine.HealthHealthy,
			[]engine.Attachment{{Network: "web_default", IPv4: "172.20.0.3"}, {Network: "monitoring", IPv4: "172.21.0.5"}}, compose("api"),
			engine.DockerExtra{ComposeProject: "web", ComposeService: "api", Command: "/app/server --port 8080", RestartPolicy: "unless-stopped"}),
		dockerCtr("web-db", "postgres:17", engine.StateRunning, 2*day, engine.HealthUnhealthy,
			[]engine.Attachment{{Network: "web_default", IPv4: "172.20.0.4"}}, compose("db"),
			engine.DockerExtra{ComposeProject: "web", ComposeService: "db", Command: "postgres", RestartPolicy: "unless-stopped"}),
		dockerCtr("redis-cache", "redis:7", engine.StateRunning, 5*day, engine.HealthNone,
			[]engine.Attachment{{Network: "bridge", IPv4: "172.17.0.2"}}, nil, engine.DockerExtra{Command: "redis-server"}),
		dockerCtr("nginx-proxy", "nginx:1.27", engine.StateRunning, 3*time.Hour, engine.HealthNone,
			[]engine.Attachment{{Network: "bridge", IPv4: "172.17.0.3"}}, nil, engine.DockerExtra{Command: "nginx -g 'daemon off;'", RestartPolicy: "always"}),
		dockerCtr("grafana", "grafana/grafana:11.3", engine.StateRunning, 12*day, engine.HealthNone,
			[]engine.Attachment{{Network: "monitoring", IPv4: "172.21.0.2"}}, nil, engine.DockerExtra{RestartPolicy: "unless-stopped"}),
		dockerCtr("postgres-dev", "postgres:16", engine.StateStopped, 0, engine.HealthNone, nil, nil, engine.DockerExtra{Command: "postgres"}),
		dockerCtr("old-backup", "alpine:3.20", engine.StateStopped, 0, engine.HealthNone, nil, nil, engine.DockerExtra{Command: "sh /backup.sh", ExitCode: 1}),
		dockerCtr("builder", "golang:1.26", engine.StateCreated, 0, engine.HealthNone, nil, nil, engine.DockerExtra{Command: "go build ./..."}),
	} {
		d.Add(in)
	}
	d.Update("web-api", func(in *engine.Instance) {
		in.Ports = []engine.Port{{HostIP: "0.0.0.0", HostPort: 8080, ContainerPort: 8080, Protocol: "tcp"}}
		in.Mounts = []engine.Mount{{Type: "bind", Source: "./config", Destination: "/app/config", ReadOnly: true}}
		in.Labels["org.opencontainers.image.description"] = "REST API for the web stack"
		in.Labels["tier"] = "backend"
		in.Env = []string{"PORT=8080", "DB_HOST=web-db", "DB_PASSWORD=s3cr3t-pw", "LOG_LEVEL=info"}
	})
	d.Update("web-db", func(in *engine.Instance) {
		in.Mounts = []engine.Mount{{Type: "volume", Source: "web_dbdata", Destination: "/var/lib/postgresql/data"}}
		in.Env = []string{"POSTGRES_USER=app", "POSTGRES_PASSWORD=pg-secret", "PGDATA=/var/lib/postgresql/data"}
	})
	d.Update("grafana", func(in *engine.Instance) {
		in.Mounts = []engine.Mount{{Type: "volume", Source: "grafana-data", Destination: "/var/lib/grafana"}}
		in.Env = []string{"GF_SECURITY_ADMIN_PASSWORD=admin", "GF_SERVER_ROOT_URL=http://grafana.lan"}
	})
	d.Update("nginx-proxy", func(in *engine.Instance) {
		in.Ports = []engine.Port{
			{HostIP: "0.0.0.0", HostPort: 80, ContainerPort: 80, Protocol: "tcp"},
			{HostIP: "0.0.0.0", HostPort: 443, ContainerPort: 443, Protocol: "tcp"},
		}
	})

	const mib, gib = 1 << 20, 1 << 30
	d.SetProfile("web-db", Profile{Shape: Spike, Period: 45 * time.Second, CPU: 0.21, Mem: 1200 * mib, MemLimit: 2 * gib, Net: 40 << 10, Blk: 8 << 20})
	d.SetProfile("web-api", Profile{Shape: Sine, Period: 30 * time.Second, CPU: 0.078, Mem: 340 * mib, MemLimit: gib, Net: 40 << 10, Blk: 120 << 10})
	d.SetProfile("nginx-proxy", Profile{Shape: Sine, Period: 20 * time.Second, CPU: 0.062, Mem: 48 * mib, Net: 2300 << 10})
	d.SetLogProfile("web-api", LogProfile{Rate: 2, ErrEvery: 25})
	d.SetLogProfile("web-db", LogProfile{Rate: 0.5, ErrEvery: 4})
	d.SetLogProfile("nginx-proxy", LogProfile{Rate: 5})

	i := w.Incus
	i.Configure(func(info *engine.Info) { info.Projects = []string{"default", "lab"} })
	i.AddNetwork(engine.Network{Name: "incusbr0", Driver: "bridge", Subnets: []string{"10.10.0.1/24"}, Gateway: "10.10.0.1", Managed: true})
	i.AddNetwork(engine.Network{Name: "macvlan0", Driver: "macvlan", Managed: false})
	i.AddVolume(engine.Volume{Name: "backups", Pool: "default", Project: "default", Driver: "zfs"})
	i.AddVolume(engine.Volume{Name: "isos", Pool: "local", Project: "default", Driver: "dir", UsedBy: []string{"web-win11"}})
	incusInst := func(project, name, image string, kind engine.Kind, state engine.State, up time.Duration, net, ip string, profiles ...string) engine.Instance {
		in := engine.Instance{
			Name: name, Project: project, Image: image, Kind: kind, State: state,
			Created: ago(c, up+day),
			Extra:   &engine.IncusExtra{Profiles: profiles, Architecture: "x86_64", Location: "none"},
		}
		if net != "" {
			in.Networks = []engine.Attachment{{Network: net, IPv4: ip}}
		}
		if state == engine.StateRunning {
			in.Started = ago(c, up)
		}
		return in
	}
	for _, in := range []engine.Instance{
		incusInst("default", "web-lab", "images:debian/12", engine.KindContainer, engine.StateRunning, 6*time.Hour, "incusbr0", "10.10.0.21", "default"),
		incusInst("default", "k3s-node1", "images:ubuntu/24.04", engine.KindVM, engine.StateRunning, 9*day, "incusbr0", "10.10.0.30", "default", "vm-large"),
		incusInst("default", "test-alpine", "images:alpine/3.20", engine.KindContainer, engine.StateStopped, 0, "", "", "default"),
		incusInst("default", "web-win11", "(local) win11", engine.KindVM, engine.StateStopped, 0, "", "", "default", "windows"),
		incusInst("lab", "dev-arch", "images:archlinux", engine.KindContainer, engine.StateRunning, 30*time.Minute, "incusbr0", "10.10.0.40", "default"),
		incusInst("lab", "ci-runner", "images:debian/12", engine.KindContainer, engine.StatePaused, 0, "incusbr0", "10.10.0.41", "default"),
	} {
		i.Add(in)
	}
	// The VM runs close to its memory limit: the Admin view shows the alert.
	i.SetProfile("k3s-node1", Profile{Shape: Sine, Period: 40 * time.Second, CPU: 0.6, CPULimit: 2, Mem: 3700 * mib, MemLimit: 4 * gib, Net: 110 << 10, Blk: 900 << 10})
	i.Update("web-lab", func(in *engine.Instance) {
		in.Labels = map[string]string{"owner": "homelab", "purpose": "web lab"}
		in.Env = []string{"APP_ENV=lab", "API_TOKEN=tok-123"}
		in.Mounts = []engine.Mount{{Name: "data", Type: "disk", Source: "default/backups", Destination: "/srv/backups"}}
		x := in.Extra.(*engine.IncusExtra)
		x.Description = "Web lab sandbox"
		x.ImageDescription = "Debian bookworm amd64 (20261020_05:24)"
	})
}

// Large populates n instances (≈60 % Docker) over 20 networks and 4 Incus
// projects, for the performance budgets (docs/05-quality-plan.md §4.1).
// The layout is deterministic for a given n.
func Large(w *World, n int) {
	rng := rand.New(rand.NewPCG(uint64(n), 42))
	images := []string{"nginx:1.27", "postgres:17", "redis:7", "node:22-alpine", "ghcr.io/me/api:1.4", "alpine:3.20", "grafana/grafana:11.3"}
	states := []engine.State{engine.StateRunning, engine.StateRunning, engine.StateRunning, engine.StateStopped, engine.StatePaused, engine.StateCreated}
	projects := []string{"default", "lab", "ci", "prod"}
	w.Incus.Configure(func(info *engine.Info) { info.Projects = projects })
	for k := range 20 {
		w.Docker.AddNetwork(engine.Network{Name: fmt.Sprintf("net%02d", k), Driver: "bridge", Subnets: []string{fmt.Sprintf("172.%d.0.0/16", 20+k)}, Managed: true})
		if k < 4 {
			w.Incus.AddNetwork(engine.Network{Name: fmt.Sprintf("incusbr%d", k), Driver: "bridge", Subnets: []string{fmt.Sprintf("10.%d.0.1/24", 10+k)}, Managed: true})
		}
	}
	for k := range n {
		state := states[rng.IntN(len(states))]
		up := time.Duration(rng.IntN(30*24)) * time.Hour
		in := engine.Instance{State: state, Created: ago(w.Clock, up+time.Hour)}
		if state == engine.StateRunning {
			in.Started = ago(w.Clock, up)
		}
		if k%5 < 3 {
			net := fmt.Sprintf("net%02d", rng.IntN(20))
			in.Name = fmt.Sprintf("svc-%03d", k)
			in.Image = images[rng.IntN(len(images))]
			in.Networks = []engine.Attachment{{Network: net, IPv4: fmt.Sprintf("172.%d.0.%d", 20+rng.IntN(20), 2+k%250)}}
			in.Extra = &engine.DockerExtra{}
			w.Docker.Add(in)
			continue
		}
		in.Name = fmt.Sprintf("inst-%03d", k)
		in.Project = projects[rng.IntN(len(projects))]
		in.Image = "images:debian/12"
		if rng.IntN(4) == 0 {
			in.Kind = engine.KindVM
		}
		in.Extra = &engine.IncusExtra{Profiles: []string{"default"}}
		w.Incus.Add(in)
	}
}
