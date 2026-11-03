package incus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"

	"github.com/blokxz/viewDI/internal/engine"
)

// PullImage implements engine.Engine: Incus images are refreshed.
func (a *Adapter) PullImage(context.Context, engine.ImageCheck, engine.ProgressFunc) (string, error) {
	return "", engine.Unsupported("pull image", "Incus images are refreshed (f)")
}

// CopyInfix sits between an instance's name and the date in the name of the
// copy a rebuild keeps ("web-pre-rebuild-20261103-174755").
const CopyInfix = "-pre-rebuild-"

// maxSnapshotNames is how many snapshot names a refusal lists.
const maxSnapshotNames = 5

// copyName is the name of the copy kept before a rebuild: name, the infix and
// the date, shortened (the name, not the date) to the 63 characters of a
// host name.
func copyName(name string, now time.Time) string {
	suffix := CopyInfix + now.UTC().Format("20060102-150405")
	if over := len(name) + len(suffix) - 63; over > 0 {
		name = strings.TrimRight(name[:len(name)-over], "-")
	}
	return name + suffix
}

// Recreate implements engine.Engine: it rebuilds the instance from the
// newest build of opts.Source ("images:ubuntu/noble"), which replaces the
// root filesystem and keeps the configuration, devices and attached volumes.
//
// Incus will not rebuild an instance that has snapshots (it deletes the root
// volume), so the instance is refused before anything is touched when it has
// any, and the safety net is a copy, not a snapshot. With opts.Copy the
// stopped instance is copied (without snapshots) to a new instance named
// after it and the date, the rebuild runs on the original, and the copy
// stays. When a step fails after the copy exists the original is deleted and
// the copy takes its name, so the instance is back as it was; without a copy
// a failure leaves what the daemon left. A running instance is stopped for
// the rebuild and started again. It returns the copy's name ("" without one).
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
	inst, _, err := c.GetInstance(name)
	if err != nil {
		return "", classify(ctx, op, err)
	}
	snaps, err := c.GetInstanceSnapshots(name)
	if err != nil {
		return "", classify(ctx, op, err)
	}
	if len(snaps) > 0 {
		names := make([]string, 0, maxSnapshotNames)
		for i, sn := range snaps {
			if i == maxSnapshotNames {
				names = append(names, fmt.Sprintf("and %d more", len(snaps)-i))
				break
			}
			names = append(names, sn.Name)
		}
		return "", engine.Errorf(engine.ClassConflict, op, fmt.Errorf(
			"%s has %d snapshots (%s) and Incus will not rebuild an instance that has snapshots: delete them first",
			name, len(snaps), strings.Join(names, ", ")))
	}
	st, _, err := c.GetInstanceState(name)
	if err != nil {
		return "", classify(ctx, op, err)
	}
	wasRunning := st.StatusCode == api.Running || st.StatusCode == api.Frozen
	if opts.Copy {
		if err := a.checkSpace(ctx, c, op, inst, st); err != nil {
			return "", err
		}
	}

	if wasRunning {
		progress(engine.Progress{Phase: engine.PhaseStopping})
		if err := a.stopFor(ctx, c, op, name, opts.Timeout, st.StatusCode == api.Frozen); err != nil {
			return "", a.startBack(ctx, project, op, name, wasRunning, err)
		}
	}

	var keep string
	if opts.Copy {
		keep = copyName(name, time.Now())
		progress(engine.Progress{Phase: engine.PhaseCopying})
		if err := a.copyInstance(ctx, c, op, inst, keep); err != nil {
			a.discard(ctx, project, op, keep) // a half-made copy helps nobody
			return "", a.startBack(ctx, project, op, name, wasRunning, err)
		}
	}

	// back puts the instance as it was. It uses a context of its own, so a
	// canceled job still cleans up.
	back := func(cause error) error {
		progress(engine.Progress{Phase: engine.PhaseRollingBack})
		if keep == "" {
			return a.startBack(ctx, project, op, name, wasRunning, cause)
		}
		bctx, bcancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		defer bcancel()
		bc := a.client(bctx, project)
		a.discard(bctx, project, op, name)
		err := a.restoreDevices(bctx, bc, op, keep, inst.Devices)
		if err == nil {
			var ro incus.Operation
			if ro, err = bc.RenameInstance(keep, api.InstancePost{Name: name}); err == nil {
				err = a.wait(bctx, op, ro)
			}
		}
		if err != nil {
			return fmt.Errorf("%w (and putting the copy %s back failed: %v; your data is in the copy)", cause, keep, err)
		}
		return a.startBack(ctx, project, op, name, wasRunning, cause)
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
	return keep, nil
}

// checkSpace refuses a copy that the root pool cannot hold. Pools that copy
// on write (zfs, btrfs, ceph) take almost nothing, so they are not checked,
// and neither is an instance whose usage the daemon does not report.
func (a *Adapter) checkSpace(ctx context.Context, c incus.InstanceServer, op string, inst *api.Instance, st *api.InstanceState) error {
	pool := inst.ExpandedDevices["root"]["pool"]
	used := uint64(0)
	if d, ok := st.Disk["root"]; ok && d.Usage > 0 {
		used = uint64(d.Usage)
	}
	if pool == "" || used == 0 {
		return nil
	}
	p, _, err := c.GetStoragePool(pool)
	if err != nil {
		return classify(ctx, op, err)
	}
	switch p.Driver {
	case "zfs", "btrfs", "ceph", "cephfs":
		return nil
	}
	res, err := c.GetStoragePoolResources(pool)
	if err != nil {
		return classify(ctx, op, err)
	}
	if free := res.Space.Total - res.Space.Used; res.Space.Total > 0 && free < used {
		return engine.Errorf(engine.ClassConflict, op, fmt.Errorf(
			"a copy of %s needs %s free in the pool %s and it has %s: free some space, or rebuild without a copy",
			inst.Name, size(used), pool, size(free)))
	}
	return nil
}

// size formats bytes for a message.
func size(n uint64) string {
	const unit = 1 << 10
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// stopFor stops an instance, forcibly when it is frozen.
func (a *Adapter) stopFor(ctx context.Context, c incus.InstanceServer, op, name string, grace time.Duration, force bool) error {
	timeout := engine.DefaultStopTimeout
	if grace > 0 {
		timeout = grace
	}
	so, err := c.UpdateInstanceState(name, api.InstanceStatePut{Action: "stop", Timeout: int(timeout / time.Second), Force: force}, "")
	if err != nil {
		return classify(ctx, op, err)
	}
	return a.wait(ctx, op, so)
}

// copyInstance copies inst, without snapshots, to a new instance named to.
// The copy keeps the configuration and devices (not the volatile keys,
// except the base image) and the profiles.
func (a *Adapter) copyInstance(ctx context.Context, c incus.InstanceServer, op string, inst *api.Instance, to string) error {
	cfg := make(map[string]string, len(inst.Config))
	for k, v := range inst.Config {
		if !strings.HasPrefix(k, "volatile.") || k == "volatile.base_image" {
			cfg[k] = v
		}
	}
	if raw, err := json.Marshal(inst.Devices); err == nil && len(raw) <= maxSavedDevices {
		cfg[DevicesKey] = string(raw) // so the copy can be put back by hand
	}
	post := api.InstancesPost{
		InstancePut: api.InstancePut{Config: cfg, Devices: copyDevices(inst.Devices), Profiles: inst.Profiles, Description: inst.Description},
		Name:        to,
		Type:        api.InstanceType(inst.Type),
		Source:      api.InstanceSource{Type: "copy", Source: inst.Name, InstanceOnly: true},
	}
	o, err := c.CreateInstance(post)
	if err != nil {
		return classify(ctx, op, err)
	}
	return a.wait(ctx, op, o)
}

// DevicesKey is the config key of a copy that holds the devices of the
// instance it was made from (JSON), kept apart so that the copy never
// claims what the rebuilt instance needs.
const DevicesKey = "user.viewdi.devices"

// maxSavedDevices caps the JSON saved under DevicesKey.
const maxSavedDevices = 16 << 10

// clashing are the NIC keys that must be unique among instances.
var clashing = []string{"ipv4.address", "ipv6.address", "hwaddr"}

// copyDevices returns the devices a copy gets: the root disk and the NICs
// without their fixed addresses. Anything else points at something that
// only one instance can have (a custom volume, a proxy's port, a GPU, a USB
// device), and Incus would refuse to start the rebuilt instance or to create
// the copy while the copy holds it.
func copyDevices(devices map[string]map[string]string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for name, dev := range devices {
		switch dev["type"] {
		case "disk":
			if dev["path"] != "/" {
				continue
			}
		case "nic":
		default:
			continue
		}
		d := make(map[string]string, len(dev))
		for k, v := range dev {
			d[k] = v
		}
		if d["type"] == "nic" {
			for _, k := range clashing {
				delete(d, k)
			}
		}
		out[name] = d
	}
	return out
}

// restoreDevices gives the copy name the devices of the instance it
// replaces, with the addresses the copy was made without.
func (a *Adapter) restoreDevices(ctx context.Context, c incus.InstanceServer, op, name string, devices map[string]map[string]string) error {
	cur, etag, err := c.GetInstance(name)
	if err != nil {
		return classify(ctx, op, err)
	}
	put := cur.Writable()
	put.Devices = devices
	delete(put.Config, DevicesKey)
	o, err := c.UpdateInstance(name, put, etag)
	if err != nil {
		return classify(ctx, op, err)
	}
	return a.wait(ctx, op, o)
}

// discard deletes an instance (stopping it first), best effort, with a
// context that outlives a canceled job.
func (a *Adapter) discard(ctx context.Context, project, op, name string) {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer cancel()
	dc := a.client(dctx, project)
	if cur, _, err := dc.GetInstanceState(name); err == nil && (cur.StatusCode == api.Running || cur.StatusCode == api.Frozen) {
		if so, err := dc.UpdateInstanceState(name, api.InstanceStatePut{Action: "stop", Timeout: -1, Force: true}, ""); err == nil {
			_ = a.wait(dctx, op, so)
		}
	}
	if o, err := dc.DeleteInstance(name); err == nil {
		_ = a.wait(dctx, op, o)
	}
}

// startBack starts the instance again when it was running, best effort, and
// returns cause.
func (a *Adapter) startBack(ctx context.Context, project, op, name string, wasRunning bool, cause error) error {
	if !wasRunning {
		return cause
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer cancel()
	if so, err := a.client(sctx, project).UpdateInstanceState(name, api.InstanceStatePut{Action: "start"}, ""); err == nil {
		_ = a.wait(sctx, op, so)
	}
	return cause
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
