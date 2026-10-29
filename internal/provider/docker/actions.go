package docker

import (
	"context"
	"errors"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"github.com/blokxz/viewDI/internal/engine"
)

// Act implements engine.Engine. Docker answers 304 for an action already in
// effect (start a running container), which the client reports as success;
// pausing an already paused container is a 409 that is treated the same.
func (a *Adapter) Act(ctx context.Context, id string, action engine.Action, opts engine.ActionOptions) error {
	ctx, cancel := context.WithTimeout(ctx, engine.ActionTimeout(opts))
	defer cancel()
	op := string(action) + " " + short(id)
	timeout := int(max(opts.Timeout, 0) / time.Second)
	if opts.Timeout <= 0 {
		timeout = int(engine.DefaultStopTimeout / time.Second)
	}
	if opts.Force {
		timeout = 0
	}
	var err error
	switch action {
	case engine.Start:
		_, err = a.cli.ContainerStart(ctx, id, client.ContainerStartOptions{})
	case engine.Stop:
		_, err = a.cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &timeout})
	case engine.Restart:
		_, err = a.cli.ContainerRestart(ctx, id, client.ContainerRestartOptions{Timeout: &timeout})
	case engine.Pause:
		_, err = a.cli.ContainerPause(ctx, id, client.ContainerPauseOptions{})
		if alreadyIn(err, "is already paused") {
			err = nil
		}
	case engine.Unpause:
		_, err = a.cli.ContainerUnpause(ctx, id, client.ContainerUnpauseOptions{})
		if alreadyIn(err, "is not paused") {
			err = nil
		}
	default:
		return engine.Errorf(engine.ClassConflict, op, errors.New("unknown action"))
	}
	a.forgetStarted(id) // the next list re-reads the start time
	return classify(op, err)
}

// alreadyIn reports a conflict whose message says the action is in effect.
func alreadyIn(err error, msg string) bool {
	return err != nil && cerrdefs.IsConflict(err) && containsFold(err.Error(), msg)
}

// Delete implements engine.Engine. Named volumes are never removed: v=1
// only removes the container's anonymous volumes.
func (a *Adapter) Delete(ctx context.Context, id string, opts engine.DeleteOptions) error {
	ctx, cancel := context.WithTimeout(ctx, engine.ActionTimeout(engine.ActionOptions{}))
	defer cancel()
	_, err := a.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: opts.Force || opts.Stop, RemoveVolumes: opts.Volumes})
	a.forgetStarted(id)
	return classify("delete "+short(id), err)
}
