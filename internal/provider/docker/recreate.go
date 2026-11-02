package docker

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"

	"github.com/blokxz/viewDI/internal/engine"
)

// PullImage implements engine.Engine: it pulls c.Ref with progress.
func (a *Adapter) PullImage(ctx context.Context, c engine.ImageCheck, progress engine.ProgressFunc) (string, error) {
	if progress == nil {
		progress = func(engine.Progress) {}
	}
	if err := engine.ValidateImage(c.Ref); err != nil {
		return "", engine.Errorf(engine.ClassConflict, "pull", err)
	}
	if err := a.pull(ctx, c.Ref, progress); err != nil {
		return "", err
	}
	return c.Ref, nil
}

// RefreshImage implements engine.Engine: Docker images are pulled.
func (a *Adapter) RefreshImage(context.Context, engine.ImageCheck, engine.ProgressFunc) (string, error) {
	return "", engine.Unsupported("refresh image", "Docker images are pulled (p)")
}

// Recreate implements engine.Engine (see the contract for the steps).
func (a *Adapter) Recreate(ctx context.Context, id string, opts engine.RecreateOptions, progress engine.ProgressFunc) (string, error) {
	if progress == nil {
		progress = func(engine.Progress) {}
	}
	op := "recreate " + short(id)
	ictx, cancel := engine.WithDefaultTimeout(ctx)
	res, err := a.cli.ContainerInspect(ictx, id, client.ContainerInspectOptions{})
	cancel()
	if err != nil {
		return "", classify(op, err)
	}
	old := res.Container
	if old.Config == nil || old.HostConfig == nil {
		return "", engine.Errorf(engine.ClassTransient, op, errors.New("incomplete inspect answer"))
	}
	name := strings.TrimPrefix(old.Name, "/")
	op = "recreate " + name
	if old.Config.Labels[engine.ComposeProjectLabel] != "" {
		return "", engine.Unsupported(op, "Compose manages this container: "+engine.ComposeHint(engine.Instance{Labels: old.Config.Labels}))
	}
	ref := old.Config.Image
	if ref == "" || strings.HasPrefix(ref, "sha256:") {
		return "", engine.Unsupported(op, "the container was created from an image ID, not a reference")
	}
	var oldImg *dockerspec.DockerOCIImageConfig
	ictx, cancel = engine.WithDefaultTimeout(ctx)
	if img, err := a.cli.ImageInspect(ictx, old.Image); err == nil {
		oldImg = img.Config
	}
	cancel()
	if opts.Pull {
		if err := a.pull(ctx, ref, progress); err != nil {
			return "", err
		}
	}
	cfg, host, netcfg := recreateConfig(old, oldImg)
	wasRunning := old.State != nil && (old.State.Running || old.State.Paused || old.State.Restarting)
	r := &recreate{a: a, op: op, oldID: old.ID, name: name, wasRunning: wasRunning}

	if wasRunning {
		progress(engine.Progress{Phase: engine.PhaseStopping})
		if err := a.Act(ctx, old.ID, engine.Stop, engine.ActionOptions{Timeout: opts.Timeout}); err != nil {
			return "", err
		}
		r.stopped = true
	}
	progress(engine.Progress{Phase: engine.PhaseRenaming})
	if err := r.rename(ctx, name+engine.OldSuffix); err != nil {
		return "", r.rollback(ctx, err)
	}
	r.renamed = true
	progress(engine.Progress{Phase: engine.PhaseCreating})
	cctx, cancel := engine.WithDefaultTimeout(ctx)
	created, err := a.cli.ContainerCreate(cctx, client.ContainerCreateOptions{Config: cfg, HostConfig: host, NetworkingConfig: netcfg, Name: name})
	cancel()
	if err != nil {
		return "", r.rollback(ctx, classify(op, err))
	}
	r.newID = created.ID
	progress(engine.Progress{Phase: engine.PhaseStarting})
	if err := a.Act(ctx, created.ID, engine.Start, engine.ActionOptions{}); err != nil {
		return "", r.rollback(ctx, err)
	}
	if err := r.running(ctx); err != nil {
		return "", r.rollback(ctx, err)
	}
	progress(engine.Progress{Phase: engine.PhaseRemoving})
	if err := a.Delete(ctx, old.ID, engine.DeleteOptions{Force: true}); err != nil {
		// The new container runs; the old one is left as <name>-old.
		return created.ID, engine.Errorf(engine.Classify(err), op,
			fmt.Errorf("the new container runs, but %s%s could not be removed: %w", name, engine.OldSuffix, err))
	}
	return created.ID, nil
}

// recreate tracks what a recreate did, so a failure can be undone.
type recreate struct {
	a          *Adapter
	op         string
	oldID      string
	name       string
	newID      string
	wasRunning bool
	stopped    bool
	renamed    bool
}

func (r *recreate) rename(ctx context.Context, to string) error {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	_, err := r.a.cli.ContainerRename(ctx, r.oldID, client.ContainerRenameOptions{NewName: to})
	return classify(r.op, err)
}

// startedWait is how long the new container must stay up before the old
// one is removed: a container that exits at once is a failed recreate.
var startedWait = 2 * time.Second

// running checks that the new container is still running a moment after
// it started.
func (r *recreate) running(ctx context.Context) error {
	select {
	case <-time.After(startedWait):
	case <-ctx.Done():
		return engine.Wrap(r.op, ctx.Err())
	}
	ictx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	res, err := r.a.cli.ContainerInspect(ictx, r.newID, client.ContainerInspectOptions{})
	if err != nil {
		return classify(r.op, err)
	}
	if st := res.Container.State; st == nil || !st.Running || st.Restarting {
		code := 0
		if st != nil {
			code = st.ExitCode
		}
		return engine.Errorf(engine.ClassConflict, r.op, fmt.Errorf("the new container exited (code %d)", code))
	}
	return nil
}

// rollback undoes a failed recreate: it removes the new container, gives
// the old one its name back and restarts it if it was running. It returns
// cause, annotated when the rollback itself failed. It runs on a fresh
// context: the job's may be the reason it failed.
func (r *recreate) rollback(parent context.Context, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), engine.ActionTimeout(engine.ActionOptions{}))
	defer cancel()
	var errs []error
	if r.newID != "" {
		if err := r.a.Delete(ctx, r.newID, engine.DeleteOptions{Force: true}); err != nil {
			errs = append(errs, err)
		}
	}
	if r.renamed {
		if err := r.rename(ctx, r.name); err != nil {
			errs = append(errs, err)
		}
	}
	if r.stopped {
		if err := r.a.Act(ctx, r.oldID, engine.Start, engine.ActionOptions{}); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return engine.Errorf(engine.Classify(cause), r.op, fmt.Errorf("%w; rollback failed: %w", cause, errors.Join(errs...)))
	}
	return cause
}

// recreateConfig derives the new container's configuration from the old
// one's. Docker's inspect answer merges the image's defaults into Config,
// so values equal to the old image's are dropped and the new image's
// apply instead (the new build may change its CMD, ENV or labels).
// Anonymous volumes are mounted again by name so their data carries over.
func recreateConfig(old container.InspectResponse, img *dockerspec.DockerOCIImageConfig) (*container.Config, *container.HostConfig, *network.NetworkingConfig) {
	cfg := *old.Config
	host := *old.HostConfig
	if img != nil {
		ic := img.ImageConfig
		cfg.Env = slices.DeleteFunc(slices.Clone(cfg.Env), func(e string) bool { return slices.Contains(ic.Env, e) })
		if slices.Equal(cfg.Cmd, ic.Cmd) {
			cfg.Cmd = nil
		}
		if slices.Equal(cfg.Entrypoint, ic.Entrypoint) {
			cfg.Entrypoint = nil
		}
		if cfg.WorkingDir == ic.WorkingDir {
			cfg.WorkingDir = ""
		}
		if cfg.User == ic.User {
			cfg.User = ""
		}
		if cfg.StopSignal == ic.StopSignal {
			cfg.StopSignal = ""
		}
		cfg.Labels = maps.Clone(cfg.Labels)
		for k, v := range ic.Labels {
			if cfg.Labels[k] == v {
				delete(cfg.Labels, k)
			}
		}
		cfg.ExposedPorts = maps.Clone(cfg.ExposedPorts)
		for p := range ic.ExposedPorts {
			if pp, err := network.ParsePort(p); err == nil {
				if _, bound := host.PortBindings[pp]; !bound {
					delete(cfg.ExposedPorts, pp)
				}
			}
		}
		cfg.Volumes = maps.Clone(cfg.Volumes)
		for v := range ic.Volumes {
			delete(cfg.Volumes, v)
		}
		if h := img.Healthcheck; h != nil && cfg.Healthcheck != nil && slices.Equal(cfg.Healthcheck.Test, h.Test) {
			cfg.Healthcheck = nil
		}
	}
	if len(old.ID) >= 12 && cfg.Hostname == old.ID[:12] {
		cfg.Hostname = "" // Docker's default: the new container's own ID
	}
	// Anonymous volumes (an image VOLUME, or -v /path) are not in the
	// host config by name: mount the old ones explicitly.
	named := map[string]bool{}
	for _, b := range host.Binds {
		named[strings.SplitN(b, ":", 3)[0]] = true
	}
	for _, m := range host.Mounts {
		named[m.Source] = true
	}
	host.Mounts = slices.Clone(host.Mounts)
	for _, mp := range old.Mounts {
		if mp.Type != mount.TypeVolume || mp.Name == "" || named[mp.Name] {
			continue
		}
		host.Mounts = append(host.Mounts, mount.Mount{Type: mount.TypeVolume, Source: mp.Name, Target: mp.Destination, ReadOnly: !mp.RW})
		delete(cfg.Volumes, mp.Destination)
	}
	if len(host.Mounts) == 0 {
		host.Mounts = nil
	}
	var netcfg *network.NetworkingConfig
	if mode := host.NetworkMode; old.NetworkSettings != nil && !mode.IsHost() && !mode.IsNone() && !mode.IsContainer() {
		netcfg = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{}}
		for name, ep := range old.NetworkSettings.Networks {
			if ep == nil {
				continue
			}
			// Configuration only: addresses and IDs belong to the old endpoint.
			netcfg.EndpointsConfig[name] = &network.EndpointSettings{
				IPAMConfig: ep.IPAMConfig, Links: ep.Links, DriverOpts: ep.DriverOpts, GwPriority: ep.GwPriority,
				Aliases: slices.DeleteFunc(slices.Clone(ep.Aliases), func(s string) bool { return len(old.ID) >= 12 && s == old.ID[:12] }),
			}
		}
	}
	return &cfg, &host, netcfg
}
