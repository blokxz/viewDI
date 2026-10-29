// Package engine holds the engine-agnostic domain model (instances, networks,
// volumes, events, daemon info) and the Engine contract every adapter
// implements: provider/docker, provider/incus and provider/fake.
//
// The contract is read-only in Stage 1; lifecycle actions are added in
// Stage 4. Every adapter must pass the shared suite in engine/enginetest.
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
