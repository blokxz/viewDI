package fake

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/blokxz/viewDI/internal/engine"
)

// FailRecreates makes the next recreates fail when the new container
// starts (err != nil), so they roll back; nil makes them work again.
func (e *Engine) FailRecreates(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.recreateErr = err
}

// localImage returns the local image ID ref resolves to, or "".
func (e *Engine) localImage(ref string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, img := range e.images {
		if slices.Contains(img.Refs, ref) {
			return img.ID
		}
	}
	return ""
}

// download simulates fetching ref's newest build in four steps (half the
// action delay each) and makes it the local image of ref. It returns the
// new image ID.
func (e *Engine) download(ctx context.Context, op, phase, ref string, vm bool, progress engine.ProgressFunc) (string, error) {
	e.mu.Lock()
	r, set := e.remote[ref]
	e.mu.Unlock()
	const steps = 4
	for i := range int64(steps) {
		if err := e.step(ctx, op, phase, (i+1)*25<<20, steps*25<<20, 0.5, progress); err != nil {
			return "", err
		}
	}
	id := r.digest
	if !set || id == "" {
		id = e.localImage(ref)
		if id == "" {
			id = imageID(ref)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.images { // the tag moves to the new build
		e.images[i].Refs = slices.DeleteFunc(e.images[i].Refs, func(s string) bool { return s == ref })
	}
	e.images = slices.DeleteFunc(e.images, func(img engine.Image) bool { return len(img.Refs) == 0 && img.ID != id })
	for i := range e.images {
		if e.images[i].ID == id {
			e.images[i].Refs = append(e.images[i].Refs, ref)
			return id, nil
		}
	}
	e.images = append(e.images, engine.Image{
		Engine: e.id, ID: id, Refs: []string{ref}, Size: steps * 25 << 20, Created: e.clk.Now(), VM: vm,
	})
	return id, nil
}

// PullImage implements engine.Engine (Docker): the tag moves to the
// source's newest build; containers keep the image they run.
func (e *Engine) PullImage(ctx context.Context, c engine.ImageCheck, progress engine.ProgressFunc) (string, error) {
	op := "pull " + c.Ref
	if err := e.enter(ctx, op); err != nil {
		return "", err
	}
	if e.id != engine.Docker {
		return "", engine.Unsupported(op, "Incus images are refreshed (f)")
	}
	if err := engine.ValidateImage(c.Ref); err != nil {
		return "", engine.Errorf(engine.ClassConflict, op, err)
	}
	if _, err := e.download(ctx, op, "pulling", c.Ref, false, progress); err != nil {
		return "", err
	}
	return c.Ref, nil
}

// RefreshImage implements engine.Engine (Incus): the newest build of the
// alias joins the image store; instances keep their base image.
func (e *Engine) RefreshImage(ctx context.Context, c engine.ImageCheck, progress engine.ProgressFunc) (string, error) {
	op := "refresh image " + c.Ref
	if err := e.enter(ctx, op); err != nil {
		return "", err
	}
	if e.id != engine.Incus {
		return "", engine.Unsupported(op, "Docker images are pulled (p)")
	}
	if _, alias, ok := strings.Cut(c.Ref, ":"); !ok || alias == "" {
		return "", engine.Unsupported(op, "the image has no remote source to refresh from")
	}
	id, err := e.download(ctx, op, "downloading", c.Ref, c.VM, progress)
	if err != nil {
		return "", err
	}
	return id[:min(12, len(id))], nil
}

// Recreate implements engine.Engine (Docker) with the adapter's steps:
// stop, rename to <name>-old, create, start, remove the old one; a failed
// start (FailRecreates) rolls back.
func (e *Engine) Recreate(ctx context.Context, id string, opts engine.RecreateOptions, progress engine.ProgressFunc) (string, error) {
	if progress == nil {
		progress = func(engine.Progress) {}
	}
	op := "recreate"
	if err := e.enter(ctx, op); err != nil {
		return "", err
	}
	if e.id != engine.Docker {
		return e.rebuild(ctx, id, opts, progress)
	}
	e.mu.Lock()
	in, ok := e.instances[id]
	var old engine.Instance
	if ok {
		old = clone(*in)
	}
	e.mu.Unlock()
	if !ok {
		return "", engine.NotFound(op, "instance "+id)
	}
	op = "recreate " + old.Name
	if hint := engine.ComposeHint(old); hint != "" {
		return "", engine.Unsupported(op, "Compose manages this container: "+hint)
	}
	if _, taken := e.Instance(old.Name + engine.OldSuffix); taken {
		return "", engine.Errorf(engine.ClassConflict, op, fmt.Errorf("a container named %s%s already exists", old.Name, engine.OldSuffix))
	}
	if opts.Pull {
		if _, err := e.download(ctx, op, engine.PhasePulling, old.Image, false, progress); err != nil {
			return "", err
		}
	}
	wasRunning := old.State == engine.StateRunning || old.State == engine.StatePaused || old.State == engine.StateRestarting
	if wasRunning {
		progress(engine.Progress{Phase: engine.PhaseStopping})
		if err := e.work(ctx, op); err != nil {
			return "", err
		}
		e.SetState(old.Name, engine.StateStopped)
	}
	progress(engine.Progress{Phase: engine.PhaseRenaming})
	oldName := old.Name + engine.OldSuffix
	e.Update(old.Name, func(in *engine.Instance) { in.Name = oldName })
	rollback := func(cause error, newName string) error {
		progress(engine.Progress{Phase: engine.PhaseRollingBack})
		if newName != "" {
			e.Remove(newName)
		}
		e.Update(oldName, func(in *engine.Instance) { in.Name = old.Name })
		if wasRunning {
			e.SetState(old.Name, engine.StateRunning)
		}
		return cause
	}
	progress(engine.Progress{Phase: engine.PhaseCreating})
	if err := e.work(ctx, op); err != nil {
		return "", rollback(err, "")
	}
	n := old
	n.ID = e.InstanceID("", old.Name+"@"+e.clk.Now().String())
	n.State, n.Created, n.Started, n.Status = engine.StateCreated, e.clk.Now(), old.Started, ""
	x := *old.Extra.(*engine.DockerExtra)
	x.ImageID = e.localImage(old.Image)
	n.Extra = &x
	e.Add(n)
	progress(engine.Progress{Phase: engine.PhaseStarting})
	e.mu.Lock()
	failure := e.recreateErr
	e.mu.Unlock()
	if err := e.work(ctx, op); err != nil {
		return "", rollback(err, n.Name)
	}
	if failure != nil {
		e.SetState(n.Name, engine.StateStopped)
		return "", rollback(engine.Errorf(engine.ClassConflict, op, fmt.Errorf("the new container exited: %w", failure)), n.Name)
	}
	e.SetState(n.Name, engine.StateRunning)
	progress(engine.Progress{Phase: engine.PhaseRemoving})
	if !e.Remove(oldName) {
		return n.ID, engine.Errorf(engine.ClassTransient, op, errors.New("the old container vanished"))
	}
	return n.ID, nil
}

// rebuild is Recreate on Incus with the adapter's steps: snapshot, stop,
// rebuild from the source's newest build, start; a failure after the
// snapshot (FailRecreates) puts the instance back as it was. It returns the
// snapshot's name.
func (e *Engine) rebuild(ctx context.Context, id string, opts engine.RecreateOptions, progress engine.ProgressFunc) (string, error) {
	op := "rebuild " + id
	if _, _, ok := strings.Cut(opts.Source, ":"); !ok {
		return "", engine.Unsupported(op, "the instance has no image source to rebuild from")
	}
	e.mu.Lock()
	in, ok := e.instances[id]
	var old engine.Instance
	if ok {
		old = clone(*in)
	}
	e.mu.Unlock()
	if !ok {
		return "", engine.NotFound(op, "instance "+id)
	}
	wasRunning := old.State == engine.StateRunning || old.State == engine.StatePaused
	snap := "viewdi-pre-rebuild-" + e.clk.Now().UTC().Format("20060102-150405")
	progress(engine.Progress{Phase: engine.PhaseSnapshotting})
	if err := e.work(ctx, op); err != nil {
		return "", err
	}
	back := func(cause error) error {
		progress(engine.Progress{Phase: engine.PhaseRollingBack})
		e.Update(old.Name, func(in *engine.Instance) { in.Extra = old.Extra })
		if wasRunning {
			e.SetState(old.Name, engine.StateRunning)
		} else {
			e.SetState(old.Name, old.State)
		}
		return cause
	}
	if wasRunning {
		progress(engine.Progress{Phase: engine.PhaseStopping})
		if err := e.work(ctx, op); err != nil {
			return "", back(err)
		}
		e.SetState(old.Name, engine.StateStopped)
	}
	progress(engine.Progress{Phase: engine.PhaseRebuilding})
	if err := e.work(ctx, op); err != nil {
		return "", back(err)
	}
	e.mu.Lock()
	failure, r := e.recreateErr, e.remote[opts.Source]
	e.mu.Unlock()
	if failure != nil {
		return "", back(engine.Errorf(engine.ClassConflict, op, fmt.Errorf("the rebuilt instance did not come up: %w", failure)))
	}
	e.Update(old.Name, func(in *engine.Instance) {
		if x, ok := in.Extra.(*engine.IncusExtra); ok {
			nx := *x
			if r.digest != "" {
				nx.BaseImage = r.digest
			}
			if r.serial != "" {
				nx.ImageSerial = r.serial
			}
			in.Extra = &nx
		}
	})
	if wasRunning {
		progress(engine.Progress{Phase: engine.PhaseStarting})
		if err := e.work(ctx, op); err != nil {
			return "", back(err)
		}
		e.SetState(old.Name, engine.StateRunning)
	}
	return snap, nil
}
