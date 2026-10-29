package engine

import "time"

// Action is a lifecycle action on one instance.
type Action string

// Lifecycle actions. Pause is Docker pause / Incus freeze.
const (
	Start   Action = "start"
	Stop    Action = "stop"
	Restart Action = "restart"
	Pause   Action = "pause"
	Unpause Action = "unpause"
)

// Actions lists the lifecycle actions.
var Actions = []Action{Start, Stop, Restart, Pause, Unpause}

// Expected is the state an action leads to (Restart leads back to running).
func (a Action) Expected() State {
	switch a {
	case Stop:
		return StateStopped
	case Pause:
		return StatePaused
	}
	return StateRunning
}

// Progressive is the "-ing" form for the UI ("stopping").
func (a Action) Progressive() string {
	switch a {
	case Stop:
		return "stopping"
	case Pause:
		return "pausing"
	case Unpause:
		return "resuming"
	case Restart:
		return "restarting"
	}
	return "starting"
}

// Past is the past form for the UI ("stopped").
func (a Action) Past() string {
	switch a {
	case Stop:
		return "stopped"
	case Pause:
		return "paused"
	case Unpause:
		return "resumed"
	case Restart:
		return "restarted"
	}
	return "started"
}

// DefaultStopTimeout is how long stop and restart wait for a graceful exit
// before killing (config [actions] stop_timeout).
const DefaultStopTimeout = 10 * time.Second

// ActionOptions tunes an action.
type ActionOptions struct {
	// Timeout is the grace period of stop and restart before the instance
	// is killed; 0 uses DefaultStopTimeout.
	Timeout time.Duration
	// Force kills without a grace period (stop, restart).
	Force bool
}

// DeleteOptions tunes a delete.
type DeleteOptions struct {
	// Force deletes a running Docker container (killing it).
	Force bool
	// Volumes also removes the container's anonymous volumes (Docker).
	// Named volumes are never removed.
	Volumes bool
	// Stop stops a running instance before deleting it (Incus, which
	// refuses to delete running instances).
	Stop bool
}

// ActionTimeout bounds a whole action call: the grace period plus time for
// the daemon to do the work.
func ActionTimeout(opts ActionOptions) time.Duration {
	t := opts.Timeout
	if t <= 0 {
		t = DefaultStopTimeout
	}
	return t + 30*time.Second
}
