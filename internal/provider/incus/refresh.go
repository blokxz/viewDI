package incus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/blokxz/viewDI/internal/engine"
)

// PullImage implements engine.Engine: Incus images are refreshed.
func (a *Adapter) PullImage(context.Context, engine.ImageCheck, engine.ProgressFunc) (string, error) {
	return "", engine.Unsupported("pull image", "Incus images are refreshed (f)")
}

// SnapshotPrefix starts the name of the snapshot a rebuild takes first.
const SnapshotPrefix = "viewdi-pre-rebuild-"

// Recreate implements engine.Engine: it rebuilds the instance from the
// newest build of opts.Source ("images:ubuntu/noble"), which replaces the
// root filesystem and keeps the configuration, devices and attached
// volumes. A snapshot comes first (no snapshot, no rebuild); the instance
// is stopped for the rebuild and started again if it was running. When a
// step after the snapshot fails the snapshot is restored, so the instance
// is back as it was. It returns the snapshot's name, which stays until the
// user deletes it.
func (a *Adapter) Recreate(ctx context.Context, id string, opts engine.RecreateOptions, progress engine.ProgressFunc) (string, error) {
	if progress == nil {
		progress = func(engine.Progress) {}
	}
	op := "rebuild " + id
	remote, alias, ok := strings.Cut(opts.Source, ":")
	server := a.server(remote)
	if !ok || alias == "" || server == "" {
		return "", engine.Unsupported(op, "the instance has no image source to rebuild from")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	project, name := a.split(id)
	c := a.client(ctx, project)
	_, _, err := c.GetInstance(name)
	if err != nil {
		return "", classify(ctx, op, err)
	}
	st, _, err := c.GetInstanceState(name)
	if err != nil {
		return "", classify(ctx, op, err)
	}
	wasRunning := st.StatusCode == api.Running || st.StatusCode == api.Frozen

	snap := SnapshotPrefix + time.Now().UTC().Format("20060102-150405")
	progress(engine.Progress{Phase: engine.PhaseSnapshotting})
	o, err := c.CreateInstanceSnapshot(name, api.InstanceSnapshotsPost{Name: snap})
	if err != nil {
		return "", classify(ctx, op, err)
	}
	if err := a.wait(ctx, op, o); err != nil {
		return "", err
	}

	// back puts the instance as it was: the snapshot restored, started again
	// if it ran. It uses a fresh context, so a canceled job still rolls back.
	back := func(cause error) error {
		progress(engine.Progress{Phase: engine.PhaseRollingBack})
		bctx, bcancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		defer bcancel()
		bc := a.client(bctx, project)
		if cur, _, err := bc.GetInstanceState(name); err == nil && (cur.StatusCode == api.Running || cur.StatusCode == api.Frozen) {
			if so, err := bc.UpdateInstanceState(name, api.InstanceStatePut{Action: "stop", Timeout: -1, Force: true}, ""); err == nil {
				_ = a.wait(bctx, op, so)
			}
		}
		ro, err := bc.UpdateInstance(name, api.InstancePut{Restore: snap}, "")
		if err == nil {
			err = a.wait(bctx, op, ro)
		}
		if err != nil {
			return fmt.Errorf("%w (and restoring snapshot %s failed: %v)", cause, snap, err)
		}
		if wasRunning {
			if so, err := bc.UpdateInstanceState(name, api.InstanceStatePut{Action: "start"}, ""); err == nil {
				_ = a.wait(bctx, op, so)
			}
		}
		return cause
	}

	if wasRunning {
		progress(engine.Progress{Phase: engine.PhaseStopping})
		timeout := engine.DefaultStopTimeout
		if opts.Timeout > 0 {
			timeout = opts.Timeout
		}
		so, err := c.UpdateInstanceState(name, api.InstanceStatePut{Action: "stop", Timeout: int(timeout / time.Second), Force: st.StatusCode == api.Frozen}, "")
		if err == nil {
			err = a.wait(ctx, op, so)
		} else {
			err = classify(ctx, op, err)
		}
		if err != nil {
			return "", back(err)
		}
	}

	progress(engine.Progress{Phase: engine.PhaseRebuilding})
	src := api.InstanceSource{Type: "image", Alias: alias, Server: server, Protocol: "simplestreams"}
	ro, err := c.RebuildInstance(name, api.InstanceRebuildPost{Source: src})
	if err == nil {
		_, err = a.waitProgress(ctx, op, ro, progress)
	} else {
		err = classify(ctx, op, err)
	}
	if err != nil {
		return "", back(err)
	}

	if wasRunning {
		progress(engine.Progress{Phase: engine.PhaseStarting})
		so, err := c.UpdateInstanceState(name, api.InstanceStatePut{Action: "start"}, "")
		if err == nil {
			err = a.wait(ctx, op, so)
		} else {
			err = classify(ctx, op, err)
		}
		if err != nil {
			return "", back(err)
		}
	}
	return snap, nil
}

// RefreshImage implements engine.Engine: the daemon downloads the newest
// build of c.Ref ("images:debian/12") from its simplestreams server into
// the image store, cached and auto-updating like an image an instance
// creation downloads, so the next instance created from the alias starts
// from it.
func (a *Adapter) RefreshImage(ctx context.Context, c engine.ImageCheck, progress engine.ProgressFunc) (string, error) {
	if progress == nil {
		progress = func(engine.Progress) {}
	}
	op := "refresh image " + c.Ref
	remote, alias, ok := strings.Cut(c.Ref, ":")
	server := a.server(remote)
	if !ok || alias == "" || server == "" {
		return "", engine.Unsupported(op, "the image has no remote source to refresh from")
	}
	kind := string(api.InstanceTypeContainer)
	if c.VM {
		kind = string(api.InstanceTypeVM)
	}
	post := api.ImagesPost{
		ImagePut: api.ImagePut{AutoUpdate: true},
		Source: &api.ImagesPostSource{
			ImageSource: api.ImageSource{Alias: alias, Server: server, Protocol: "simplestreams", ImageType: kind},
			Type:        "image", Mode: "pull",
		},
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	progress(engine.Progress{Phase: "downloading"})
	o, err := a.client(ctx, "").CreateImage(post, nil)
	if err != nil {
		return "", classify(ctx, op, err)
	}
	done, err := a.waitProgress(ctx, op, o, progress)
	if err != nil {
		return "", err
	}
	fp, _ := done.Metadata["fingerprint"].(string)
	if fp == "" {
		return "", engine.Errorf(engine.ClassTransient, op, errors.New("the daemon reported no fingerprint"))
	}
	return fp[:min(12, len(fp))], nil
}
