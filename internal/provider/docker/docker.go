// Package docker is the Docker adapter: it maps the Docker Engine API
// (github.com/moby/moby/client) onto the engine contract.
package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"github.com/blokxz/viewDI/internal/engine"
)

// Source says where the daemon address came from.
type Source string

// Discovery sources, in precedence order.
const (
	SourceFlag     Source = "flag"
	SourceEnv      Source = "env"
	SourceConfig   Source = "config"
	SourceRootless Source = "rootless"
	SourceRootful  Source = "rootful"
)

// RootfulSocket is the system daemon socket.
const RootfulSocket = "/var/run/docker.sock"

// Discover resolves the daemon address: --docker-host flag → DOCKER_HOST →
// config.toml [docker] host → rootless socket in runtimeDir (if present) →
// rootful socket. It never dials.
func Discover(flagHost, cfgHost, runtimeDir string, getenv func(string) string) (string, Source) {
	switch {
	case flagHost != "":
		return flagHost, SourceFlag
	case getenv("DOCKER_HOST") != "":
		return getenv("DOCKER_HOST"), SourceEnv
	case cfgHost != "":
		return cfgHost, SourceConfig
	}
	if runtimeDir != "" {
		p := filepath.Join(runtimeDir, "docker.sock")
		if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return "unix://" + p, SourceRootless
		}
	}
	return "unix://" + RootfulSocket, SourceRootful
}

// startedTTL bounds how long a container's inspected start time is reused
// across list calls; events invalidate entries sooner.
const startedTTL = 5 * time.Minute

// maxInspect bounds concurrent inspect calls (docs/05-quality-plan.md §4.2).
const maxInspect = 8

// Adapter implements engine.Engine for Docker.
type Adapter struct {
	cli  *client.Client
	host string

	// started caches inspected start times: the list endpoint only reports
	// creation times.
	mu      sync.Mutex
	started map[string]startedEntry
	now     func() time.Time
}

type startedEntry struct {
	at, fetched time.Time
	pid         int
}

var _ engine.Engine = (*Adapter)(nil)

// New returns an adapter for host (a DOCKER_HOST style URI). It does not
// dial; the first call does.
func New(host string) (*Adapter, error) {
	cli, err := client.New(client.WithHost(host)) // negotiates the API version on first use
	if err != nil {
		return nil, fmt.Errorf("docker: %w", err)
	}
	return &Adapter{cli: cli, host: host, started: map[string]startedEntry{}, now: time.Now}, nil
}

// ID implements engine.Engine.
func (a *Adapter) ID() engine.ID { return engine.Docker }

// Close implements engine.Engine.
func (a *Adapter) Close() error { return a.cli.Close() }

// Ping implements engine.Engine.
func (a *Adapter) Ping(ctx context.Context) error {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	_, err := a.cli.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
	return classify("ping", err)
}

// Info implements engine.Engine.
func (a *Adapter) Info(ctx context.Context) (engine.Info, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	if _, err := a.cli.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		return engine.Info{}, classify("info", err)
	}
	res, err := a.cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return engine.Info{}, classify("info", err)
	}
	in := res.Info
	info := engine.Info{
		Engine:        engine.Docker,
		Version:       in.ServerVersion,
		APIVersion:    a.cli.ClientVersion(),
		OS:            in.OperatingSystem,
		Arch:          in.Architecture,
		Kernel:        in.KernelVersion,
		StorageDriver: in.Driver,
		CgroupVersion: in.CgroupVersion,
		Socket:        a.host,
		Host:          in.Name,
		NCPU:          in.NCPU,
		MemTotal:      uint64(max(0, in.MemTotal)),
		Images:        in.Images,
		Capabilities:  engine.Capabilities{AllProjects: true},
	}
	for _, opt := range in.SecurityOptions {
		if strings.Contains(opt, "name=rootless") {
			info.Rootless = true
		}
	}
	return info, nil
}

// ListInstances implements engine.Engine. Start times of running containers
// come from bounded, cached inspect calls.
func (a *Adapter) ListInstances(ctx context.Context, _ engine.ListOptions) ([]engine.Instance, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	res, err := a.cli.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, classify("list containers", err)
	}
	out := make([]engine.Instance, 0, len(res.Items))
	var need []int
	for _, s := range res.Items {
		in := fromSummary(s)
		if in.State == engine.StateRunning {
			if e, ok := a.cachedStart(in.ID); ok {
				in.Started, in.PID = e.at, e.pid
			} else {
				need = append(need, len(out))
			}
		}
		out = append(out, in)
	}
	a.fillStarted(ctx, out, need)
	a.pruneStarted(out)
	return out, nil
}

func (a *Adapter) cachedStart(id string) (startedEntry, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.started[id]
	if !ok || a.now().Sub(e.fetched) > startedTTL {
		return startedEntry{}, false
	}
	return e, true
}

// fillStarted inspects out[idx] (at most maxInspect at a time). A failed
// inspect leaves the start time unknown rather than failing the list (F7).
func (a *Adapter) fillStarted(ctx context.Context, out []engine.Instance, idx []int) {
	sem := make(chan struct{}, maxInspect)
	var wg sync.WaitGroup
	for _, i := range idx {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			res, err := a.cli.ContainerInspect(ctx, out[i].ID, client.ContainerInspectOptions{})
			if err != nil || res.Container.State == nil {
				return
			}
			at, pid := parseTime(res.Container.State.StartedAt), res.Container.State.Pid
			out[i].Started, out[i].PID = at, pid
			a.mu.Lock()
			a.started[out[i].ID] = startedEntry{at: at, pid: pid, fetched: a.now()}
			a.mu.Unlock()
		}()
	}
	wg.Wait()
}

// pruneStarted drops cache entries of containers that are gone or no longer running.
func (a *Adapter) pruneStarted(list []engine.Instance) {
	running := make(map[string]bool, len(list))
	for _, in := range list {
		running[in.ID] = in.State == engine.StateRunning
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for id := range a.started {
		if !running[id] {
			delete(a.started, id)
		}
	}
}

func (a *Adapter) forgetStarted(id string) {
	a.mu.Lock()
	delete(a.started, id)
	a.mu.Unlock()
}

// Inspect implements engine.Engine.
func (a *Adapter) Inspect(ctx context.Context, id string) (engine.Instance, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	res, err := a.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return engine.Instance{}, classify("inspect "+short(id), err)
	}
	in := fromInspect(res.Container)
	if in.State == engine.StateRunning {
		a.mu.Lock()
		a.started[in.ID] = startedEntry{at: in.Started, pid: in.PID, fetched: a.now()}
		a.mu.Unlock()
	}
	return in, nil
}

// Networks implements engine.Engine.
func (a *Adapter) Networks(ctx context.Context, _ engine.ListOptions) ([]engine.Network, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	res, err := a.cli.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return nil, classify("list networks", err)
	}
	out := make([]engine.Network, 0, len(res.Items))
	for _, n := range res.Items {
		net := engine.Network{
			ID: n.ID, Engine: engine.Docker, Name: n.Name, Driver: n.Driver, Scope: n.Scope,
			Managed: true, Internal: n.Internal,
		}
		for _, c := range n.IPAM.Config {
			if c.Subnet.IsValid() {
				net.Subnets = append(net.Subnets, c.Subnet.String())
			}
			if c.Gateway.IsValid() && net.Gateway == "" {
				net.Gateway = c.Gateway.String()
			}
		}
		out = append(out, net)
	}
	return out, nil
}

// Volumes implements engine.Engine.
func (a *Adapter) Volumes(ctx context.Context, _ engine.ListOptions) ([]engine.Volume, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	res, err := a.cli.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return nil, classify("list volumes", err)
	}
	out := make([]engine.Volume, 0, len(res.Items))
	for _, v := range res.Items {
		out = append(out, engine.Volume{
			Engine: engine.Docker, Name: v.Name, Driver: v.Driver,
			Mountpoint: v.Mountpoint, Created: parseTime(v.CreatedAt),
		})
	}
	return out, nil
}

// classify wraps a client error with its engine class. The client reports
// HTTP statuses as containerd errdefs and drops the errno of permission
// errors, keeping only a message.
func classify(op string, err error) error {
	switch {
	case err == nil:
		return nil
	case cerrdefs.IsNotFound(err):
		return engine.Errorf(engine.ClassNotFound, op, fmt.Errorf("%w: %w", engine.ErrNotFound, err))
	case cerrdefs.IsPermissionDenied(err), cerrdefs.IsUnauthorized(err):
		return engine.Errorf(engine.ClassPermission, op, err)
	case client.IsErrConnectionFailed(err) && strings.Contains(err.Error(), "permission denied"):
		return engine.Errorf(engine.ClassPermission, op, err)
	case errors.Is(err, context.Canceled):
		return engine.Errorf(engine.ClassCanceled, op, err)
	}
	return engine.Wrap(op, err)
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
