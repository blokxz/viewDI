// Package incus is the Incus adapter: it maps the Incus REST API
// (github.com/lxc/incus/v6/client) onto the engine contract.
//
// The upstream client keeps the request context in the shared client value
// (WithContext mutates it), so the adapter derives a fresh client for every
// call with UseProject and only then attaches the call's context. Events
// are read directly from the events websocket, in order: the client's
// listener dispatches each event on its own goroutine.
package incus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"

	"github.com/blokxz/viewDI/internal/engine"
)

// Source says where the socket path came from.
type Source string

// Discovery sources, in precedence order.
const (
	SourceFlag   Source = "flag"
	SourceEnv    Source = "env"
	SourceConfig Source = "config"
	SourceFull   Source = "unix.socket"
	SourceUser   Source = "unix.socket.user"
)

// Probe reports whether a path exists and whether it is writable by us.
type Probe func(path string) (exists, writable bool)

// StatProbe is the real Probe.
func StatProbe(path string) (exists, writable bool) {
	if _, err := os.Stat(path); err != nil {
		return false, false
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err == nil {
		_ = f.Close()
		return true, true
	}
	// Sockets cannot be opened; check the permission bits instead.
	return true, accessW(path)
}

// Discover resolves the socket: --incus-socket → INCUS_SOCKET → config.toml
// [incus] socket → $INCUS_DIR (or /var/lib/incus, /run/incus)/unix.socket →
// unix.socket.user. The full-access socket wins when usable; the restricted
// user socket (incus group) is used when only it is writable. It never dials.
func Discover(flagSock, cfgSock string, getenv func(string) string, probe Probe) (string, Source) {
	switch {
	case flagSock != "":
		return flagSock, SourceFlag
	case getenv("INCUS_SOCKET") != "":
		return getenv("INCUS_SOCKET"), SourceEnv
	case cfgSock != "":
		return cfgSock, SourceConfig
	}
	dirs := []string{"/var/lib/incus", "/run/incus"}
	if d := getenv("INCUS_DIR"); d != "" {
		dirs = []string{d}
	}
	var firstFull, firstUser string
	for _, d := range dirs {
		full, user := filepath.Join(d, "unix.socket"), filepath.Join(d, "unix.socket.user")
		fe, fw := probe(full)
		ue, uw := probe(user)
		switch {
		case fw:
			return full, SourceFull
		case uw:
			return user, SourceUser
		}
		if fe && firstFull == "" {
			firstFull = full
		}
		if ue && firstUser == "" {
			firstUser = user
		}
	}
	switch {
	case firstFull != "": // exists but not writable: the permission hint explains
		return firstFull, SourceFull
	case firstUser != "":
		return firstUser, SourceUser
	}
	return filepath.Join(dirs[0], "unix.socket"), SourceFull
}

// IsUserSocket reports whether path is the restricted incus-user socket.
func IsUserSocket(path string) bool { return filepath.Base(path) == "unix.socket.user" }

// Adapter implements engine.Engine for Incus.
type Adapter struct {
	srv    incus.InstanceServer
	socket string
	user   bool   // restricted incus-user socket
	userPj string // the per-user project of the restricted socket
}

var _ engine.Engine = (*Adapter)(nil)

// New returns an adapter for the Unix socket at path. It does not dial.
func New(path string) (*Adapter, error) {
	srv, err := incus.ConnectIncusUnixWithContext(context.Background(), path, &incus.ConnectionArgs{
		UserAgent:     "viewDI",
		SkipGetServer: true,
		SkipGetEvents: true,
	})
	if err != nil {
		return nil, fmt.Errorf("incus: %w", err)
	}
	a := &Adapter{srv: srv, socket: path, user: IsUserSocket(path)}
	if a.user {
		a.userPj = "user-" + strconv.Itoa(os.Geteuid())
	}
	return a, nil
}

type withContext interface {
	WithContext(ctx context.Context) incus.InstanceServer
}

// client returns a client of its own for one call (see the package comment).
func (a *Adapter) client(ctx context.Context, project string) incus.InstanceServer {
	if project == "" && a.user {
		project = a.userPj
	}
	c := a.srv.UseProject(project)
	if wc, ok := c.(withContext); ok {
		return wc.WithContext(ctx)
	}
	return c
}

// ID implements engine.Engine.
func (a *Adapter) ID() engine.ID { return engine.Incus }

// Close implements engine.Engine.
func (a *Adapter) Close() error {
	a.srv.Disconnect()
	return nil
}

// Ping implements engine.Engine.
func (a *Adapter) Ping(ctx context.Context) error {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	_, _, err := a.client(ctx, "").GetServer()
	return classify(ctx, "ping", err)
}

// Info implements engine.Engine.
func (a *Adapter) Info(ctx context.Context) (engine.Info, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	srv, _, err := a.client(ctx, "").GetServer()
	if err != nil {
		return engine.Info{}, classify(ctx, "info", err)
	}
	env := srv.Environment
	info := engine.Info{
		Engine:        engine.Incus,
		Version:       env.ServerVersion,
		APIVersion:    srv.APIVersion,
		OS:            strings.TrimSpace(env.OSName + " " + env.OSVersion),
		Arch:          env.KernelArchitecture,
		Kernel:        env.KernelVersion,
		StorageDriver: env.Storage,
		Socket:        a.socket,
		Host:          env.ServerName,
		Capabilities: engine.Capabilities{
			AllProjects: slices.Contains(srv.APIExtensions, "instance_all_projects") && !a.user,
			VMs:         strings.Contains(env.Driver, "qemu"),
		},
	}
	switch {
	case a.user:
		info.Capabilities.Restricted = true
		info.Capabilities.Reason = "restricted incus-user socket (project " + a.userPj + ")"
	case srv.Auth != "trusted":
		info.Capabilities.Restricted = true
		info.Capabilities.Reason = "client is not trusted by the server"
	}
	names, err := a.client(ctx, "").GetProjectNames()
	switch {
	case err == nil:
		slices.Sort(names)
		info.Projects = names
	case a.user:
		info.Projects = []string{a.userPj}
	default:
		info.Projects = []string{api.ProjectDefaultName}
	}
	return info, nil
}

// ListInstances implements engine.Engine. An empty project lists every
// project when the connection may, and falls back to the default (or
// per-user) project otherwise.
func (a *Adapter) ListInstances(ctx context.Context, opts engine.ListOptions) ([]engine.Instance, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	var list []api.InstanceFull
	var err error
	if opts.Project == "" && !a.user {
		list, err = a.client(ctx, "").GetInstancesFullAllProjects(api.InstanceTypeAny)
		if err != nil && fallBack(err) {
			list, err = a.client(ctx, api.ProjectDefaultName).GetInstancesFull(api.InstanceTypeAny)
		}
	} else {
		list, err = a.client(ctx, opts.Project).GetInstancesFull(api.InstanceTypeAny)
	}
	if err != nil {
		return nil, classify(ctx, "list instances", err)
	}
	out := make([]engine.Instance, 0, len(list))
	for _, in := range list {
		out = append(out, fromInstance(in, a.defaultProject()))
	}
	return out, nil
}

// fallBack reports whether an all-projects call should be retried on a
// single project: restricted clients get 403, old servers 400.
func fallBack(err error) bool {
	return api.StatusErrorCheck(err, http.StatusForbidden, http.StatusBadRequest) ||
		strings.Contains(err.Error(), "missing the required")
}

func (a *Adapter) defaultProject() string {
	if a.user {
		return a.userPj
	}
	return api.ProjectDefaultName
}

// Inspect implements engine.Engine. id is "<project>/<name>".
func (a *Adapter) Inspect(ctx context.Context, id string) (engine.Instance, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	project, name, ok := strings.Cut(id, "/")
	if !ok {
		project, name = a.defaultProject(), id
	}
	in, _, err := a.client(ctx, project).GetInstanceFull(name)
	if err != nil {
		return engine.Instance{}, classify(ctx, "inspect "+id, err)
	}
	return fromInstance(*in, project), nil
}

// Networks implements engine.Engine. Unmanaged host interfaces that no
// instance uses (lo, eth0, docker0…) are left out.
func (a *Adapter) Networks(ctx context.Context, opts engine.ListOptions) ([]engine.Network, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	var nets []api.Network
	var err error
	if opts.Project == "" && !a.user {
		nets, err = a.client(ctx, "").GetNetworksAllProjects()
		if err != nil && fallBack(err) {
			nets, err = a.client(ctx, api.ProjectDefaultName).GetNetworks()
		}
	} else {
		nets, err = a.client(ctx, opts.Project).GetNetworks()
	}
	if err != nil {
		return nil, classify(ctx, "list networks", err)
	}
	out := make([]engine.Network, 0, len(nets))
	for _, n := range nets {
		if !n.Managed && len(n.UsedBy) == 0 {
			continue
		}
		net := engine.Network{
			ID: n.Project + "/" + n.Name, Engine: engine.Incus, Name: n.Name, Project: n.Project,
			Driver: n.Type, Managed: n.Managed,
		}
		for _, k := range []string{"ipv4.address", "ipv6.address"} {
			if v := n.Config[k]; v != "" && v != "none" && v != "auto" {
				net.Subnets = append(net.Subnets, v)
				if net.Gateway == "" {
					net.Gateway, _, _ = strings.Cut(v, "/")
				}
			}
		}
		out = append(out, net)
	}
	return out, nil
}

// Volumes implements engine.Engine: custom storage volumes of every pool.
func (a *Adapter) Volumes(ctx context.Context, opts engine.ListOptions) ([]engine.Volume, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	pools, err := a.client(ctx, opts.Project).GetStoragePools()
	if err != nil {
		return nil, classify(ctx, "list storage pools", err)
	}
	var out []engine.Volume
	for _, p := range pools {
		var vols []api.StorageVolume
		if opts.Project == "" && !a.user {
			vols, err = a.client(ctx, "").GetStoragePoolVolumesAllProjects(p.Name)
			if err != nil && fallBack(err) {
				vols, err = a.client(ctx, api.ProjectDefaultName).GetStoragePoolVolumes(p.Name)
			}
		} else {
			vols, err = a.client(ctx, opts.Project).GetStoragePoolVolumes(p.Name)
		}
		if err != nil {
			return nil, classify(ctx, "list volumes of "+p.Name, err)
		}
		for _, v := range vols {
			if v.Type != "custom" {
				continue
			}
			vol := engine.Volume{
				Engine: engine.Incus, Name: v.Name, Project: v.Project, Pool: p.Name,
				Driver: p.Driver, Created: v.CreatedAt,
			}
			for _, u := range v.UsedBy {
				vol.UsedBy = append(vol.UsedBy, lastSegment(u))
			}
			out = append(out, vol)
		}
	}
	return out, nil
}

// classify wraps a client error with its engine class.
func classify(ctx context.Context, op string, err error) error {
	switch {
	case err == nil:
		return nil
	case api.StatusErrorCheck(err, http.StatusNotFound):
		return engine.Errorf(engine.ClassNotFound, op, fmt.Errorf("%w: %w", engine.ErrNotFound, err))
	case api.StatusErrorCheck(err, http.StatusForbidden, http.StatusUnauthorized):
		return engine.Errorf(engine.ClassPermission, op, err)
	case errors.Is(err, context.Canceled) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)):
		return engine.Errorf(engine.ClassCanceled, op, err)
	case ctx.Err() != nil && !engine.IsTimeout(err):
		// The client sometimes reports an expired context as a plain error.
		return engine.Wrap(op, fmt.Errorf("%w: %w", ctx.Err(), err))
	}
	return engine.Wrap(op, err)
}

// lastSegment returns the name in an API URL such as
// "/1.0/instances/web?project=lab".
func lastSegment(u string) string {
	u, _, _ = strings.Cut(u, "?")
	return u[strings.LastIndexByte(u, '/')+1:]
}
