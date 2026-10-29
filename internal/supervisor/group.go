package supervisor

import (
	"context"
	"sync"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
)

// Buffer is the capacity of the shared change channel.
const Buffer = 256

// Group runs one supervisor per engine and merges their changes.
type Group struct {
	sups []*Supervisor
	out  chan store.Change
	wg   sync.WaitGroup
}

// Target is one engine to supervise.
type Target struct {
	Engine   engine.Engine
	Endpoint string // socket path or URI, for hints
}

// NewGroup returns a group for targets; Start runs it.
func NewGroup(opts Options, targets ...Target) *Group {
	g := &Group{out: make(chan store.Change, Buffer)}
	for i, t := range targets {
		o := opts
		if o.Seed != 0 {
			o.Seed += uint64(i)
		}
		g.sups = append(g.sups, New(t.Engine, t.Endpoint, g.out, o))
	}
	return g
}

// Start runs every supervisor until ctx ends.
func (g *Group) Start(ctx context.Context) {
	for _, s := range g.sups {
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			s.Run(ctx)
		}()
	}
}

// Wait blocks until every supervisor stopped (after ctx ended).
func (g *Group) Wait() { g.wg.Wait() }

// Changes is the merged change stream.
func (g *Group) Changes() <-chan store.Change { return g.out }

// Engines lists the supervised engines.
func (g *Group) Engines() []engine.ID {
	ids := make([]engine.ID, len(g.sups))
	for i, s := range g.sups {
		ids[i] = s.ID()
	}
	return ids
}

// Reconnect asks every supervisor for an immediate reconnect or resync.
func (g *Group) Reconnect() {
	for _, s := range g.sups {
		s.Reconnect()
	}
}

// SetProject scopes the Incus supervisor to project ("" = all).
func (g *Group) SetProject(project string) {
	for _, s := range g.sups {
		if s.ID() == engine.Incus {
			s.SetProject(project)
		}
	}
}

// SetInitialProject scopes Incus before Start.
func (g *Group) SetInitialProject(project string) {
	for _, s := range g.sups {
		if s.ID() == engine.Incus {
			s.SetInitialProject(project)
		}
	}
}
