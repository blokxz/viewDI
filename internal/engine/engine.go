// Package engine holds the engine-agnostic domain model (instances, networks,
// volumes, events, daemon info) and the Engine contract every adapter
// implements: provider/docker, provider/incus and provider/fake.
//
// The contract grew by stage: reads (Stage 1), Stats (Stage 2), the
// detail-view streams (Stage 3) and lifecycle actions (Stage 4). Every adapter must pass the shared suite in engine/enginetest.
package engine

import (
	"context"
	"time"
)

// DefaultTimeout bounds every daemon call whose context has no deadline.
const DefaultTimeout = 5 * time.Second

// Engine is the read-only contract of a container engine.
//
// Every method takes a context and must return promptly once it is done;
// adapters apply DefaultTimeout when the context has no deadline. Errors are
// wrapped so that Classify can sort them into transient, permission,
// not-found and missing-socket failures.
type Engine interface {
	// ID identifies the engine kind.
	ID() ID
	// Ping is the cheap health check used by the supervisor watchdog.
	Ping(ctx context.Context) error
	// Info describes the daemon and what the connection may do.
	Info(ctx context.Context) (Info, error)
	// ListInstances lists containers and VMs. For engines with projects,
	// opts.Project selects one project; "" lists every project the
	// connection can see.
	ListInstances(ctx context.Context, opts ListOptions) ([]Instance, error)
	// Inspect returns one instance by its engine-local ID (Instance.ID). A
	// missing instance yields an error classified as ClassNotFound.
	Inspect(ctx context.Context, id string) (Instance, error)
	// Events subscribes to instance lifecycle events. It returns once the
	// subscription is established, so a list taken afterwards cannot miss an
	// event. The feed closes when ctx ends or the stream fails; Feed.Err
	// then reports why.
	Events(ctx context.Context) (*Feed, error)
	// Networks lists networks.
	Networks(ctx context.Context, opts ListOptions) ([]Network, error)
	// Volumes lists volumes (Docker volumes, Incus custom storage volumes).
	Volumes(ctx context.Context, opts ListOptions) ([]Volume, error)
	// Stats reads the resource counters of the given instances (engine-local
	// IDs) in one pass. Every running instance gets a sample; unknown ones
	// are left out, and stopped or paused ones may be. An error means the
	// whole read failed.
	Stats(ctx context.Context, ids []string) ([]Sample, error)
	// InspectRaw returns the daemon's own inspect document for one
	// instance, as JSON (the detail view's Inspect tab).
	InspectRaw(ctx context.Context, id string) ([]byte, error)
	// Logs streams an instance's output: opts.Tail past lines, then new
	// lines while opts.Follow. It returns once the stream is established; a
	// missing instance yields ClassNotFound. Without Follow the stream ends
	// cleanly after the history; with Follow it ends when ctx does, when
	// the daemon fails (Err set), or cleanly once the instance stops.
	Logs(ctx context.Context, id string, opts LogOptions) (*Stream[LogLine], error)
	// WatchStats streams samples of one instance about every interval
	// (Docker pushes one per second whatever the interval). Samples carry
	// per-interface counters, and disk usage where the engine reports it.
	// A missing instance yields ClassNotFound; the stream ends when ctx
	// does or the daemon fails, and sends nothing while the instance is
	// not running.
	WatchStats(ctx context.Context, id string, interval time.Duration) (*Stream[Sample], error)
	// Act runs a lifecycle action and returns once the daemon finished it
	// (Incus operations are waited for). An action already in effect
	// succeeds (starting a running instance is a no-op). A state that does
	// not allow the action (pausing a stopped instance) yields
	// ClassConflict; a missing instance ClassNotFound.
	Act(ctx context.Context, id string, action Action, opts ActionOptions) error
	// Delete removes an instance. A running one yields ClassConflict unless
	// opts.Force (Docker) or opts.Stop (Incus) allows it.
	Delete(ctx context.Context, id string, opts DeleteOptions) error
	// Close releases the connection. Calls after Close fail.
	Close() error
}

// ListOptions scopes list calls.
type ListOptions struct {
	Project string // Incus project; "" = every visible project. Ignored by Docker.
}

// WithDefaultTimeout applies DefaultTimeout to ctx unless it already has a deadline.
func WithDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, DefaultTimeout)
}
