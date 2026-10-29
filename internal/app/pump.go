package app

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/store"
)

// Source feeds engine changes to the UI and takes the user's connection
// requests. supervisor.Group implements it; main wires it.
type Source interface {
	Changes() <-chan store.Change
	Reconnect()
	SetProject(project string)
}

// ChangesMsg carries a batch of store changes from the pump.
type ChangesMsg struct{ Changes []store.Change }

// Coalescing bounds (docs/05-quality-plan.md §4.2): changes arriving within
// coalesceWindow of the first one are delivered as one message.
const (
	coalesceWindow = 50 * time.Millisecond
	maxBatch       = 1024
)

// pump returns the re-arming command that waits for the next changes. It
// blocks in its own goroutine (never in Update) and ends when ctx does.
func pump(ctx context.Context, ch <-chan store.Change) tea.Cmd {
	return func() tea.Msg {
		var batch []store.Change
		select {
		case <-ctx.Done():
			return nil
		case c, ok := <-ch:
			if !ok {
				return nil
			}
			batch = append(batch, c)
		}
		window := time.NewTimer(coalesceWindow)
		defer window.Stop()
		for len(batch) < maxBatch {
			select {
			case c, ok := <-ch:
				if !ok {
					return ChangesMsg{Changes: batch}
				}
				batch = append(batch, c)
			case <-window.C:
				return ChangesMsg{Changes: batch}
			case <-ctx.Done():
				return ChangesMsg{Changes: batch}
			}
		}
		return ChangesMsg{Changes: batch}
	}
}

// ClockTickMsg advances the render clock (uptimes, countdowns).
type ClockTickMsg time.Time

func clockTick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return ClockTickMsg(t) })
}
