package fake

import (
	"context"
	"errors"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

// SetActionDelay makes every action and delete take d (on the engine's
// clock), so the UI's pending state is visible and a daemon can fail in
// the middle of an operation.
func (e *Engine) SetActionDelay(d time.Duration) {
	e.mu.Lock()
	e.actionDelay = d
	e.mu.Unlock()
}

// work waits for the action delay, then applies faults again: a daemon
// that died or hung meanwhile fails the operation.
func (e *Engine) work(ctx context.Context, op string) error {
	e.mu.Lock()
	d := e.actionDelay
	e.mu.Unlock()
	if d > 0 {
		select {
		case <-e.clk.After(d):
		case <-ctx.Done():
			return engine.Wrap(op, ctx.Err())
		}
	}
	return e.enter(ctx, op)
}

func conflict(op, why string) error {
	return engine.Errorf(engine.ClassConflict, op, errors.New(why))
}

// Act implements engine.Engine with Docker-like semantics.
func (e *Engine) Act(ctx context.Context, id string, action engine.Action, _ engine.ActionOptions) error {
	op := string(action)
	if err := e.enter(ctx, op); err != nil {
		return err
	}
	e.mu.Lock()
	in, ok := e.instances[id]
	var name string
	var state engine.State
	if ok {
		name, state = in.Name, in.State
	}
	e.mu.Unlock()
	if !ok {
		return engine.NotFound(op, "instance "+id)
	}
	var target engine.State
	switch action {
	case engine.Start:
		switch state {
		case engine.StateRunning:
			return nil
		case engine.StatePaused:
			return conflict(op, "instance is paused: unpause it instead")
		}
		target = engine.StateRunning
	case engine.Stop:
		if state != engine.StateRunning && state != engine.StatePaused && state != engine.StateRestarting {
			return nil
		}
		target = engine.StateStopped
	case engine.Restart:
		if state == engine.StatePaused {
			return conflict(op, "instance is paused: unpause it first")
		}
		target = engine.StateRunning
	case engine.Pause:
		switch state {
		case engine.StatePaused:
			return nil
		case engine.StateRunning:
		default:
			return conflict(op, "instance is not running")
		}
		target = engine.StatePaused
	case engine.Unpause:
		switch state {
		case engine.StateRunning:
			return nil
		case engine.StatePaused:
		default:
			return conflict(op, "instance is not paused")
		}
		target = engine.StateRunning
	default:
		return engine.Errorf(engine.ClassConflict, op, errors.New("unknown action"))
	}
	if err := e.work(ctx, op); err != nil {
		return err
	}
	if action == engine.Restart && state == engine.StateRunning {
		e.SetState(name, engine.StateStopped)
	}
	if !e.SetState(name, target) {
		return engine.NotFound(op, "instance "+id) // removed meanwhile
	}
	return nil
}

// Delete implements engine.Engine.
func (e *Engine) Delete(ctx context.Context, id string, opts engine.DeleteOptions) error {
	if err := e.enter(ctx, "delete"); err != nil {
		return err
	}
	e.mu.Lock()
	in, ok := e.instances[id]
	var name string
	var running bool
	if ok {
		name, running = in.Name, in.State == engine.StateRunning || in.State == engine.StatePaused
	}
	e.mu.Unlock()
	if !ok {
		return engine.NotFound("delete", "instance "+id)
	}
	if running && !opts.Force && !opts.Stop {
		return conflict("delete", "instance is running: stop it first")
	}
	if err := e.work(ctx, "delete"); err != nil {
		return err
	}
	if running && opts.Stop {
		e.SetState(name, engine.StateStopped)
	}
	if !e.Remove(name) {
		return engine.NotFound("delete", "instance "+id)
	}
	return nil
}
