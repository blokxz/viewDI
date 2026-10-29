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
	// Metrics delivers sampler passes (lossy, latest wins); nil without metrics.
	Metrics() <-chan store.MetricsSampled
	Reconnect()
	SetProject(project string)
	// SetInterval changes the metrics sampling interval.
	SetInterval(d time.Duration)
}

// ChangesMsg carries a batch of store changes from the pump.
type ChangesMsg struct{ Changes []store.Change }

// Coalescing bounds (docs/05-quality-plan.md §4.2): changes arriving within
// coalesceWindow of the first one are delivered as one message.
const (
	coalesceWindow = 50 * time.Millisecond
	maxBatch       = 1024
)

// pump returns the re-arming command that waits for the next changes and
// metric passes. It blocks in its own goroutine (never in Update) and ends
// when ctx does.
func pump(ctx context.Context, ch <-chan store.Change, mx <-chan store.MetricsSampled) tea.Cmd {
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
		case m := <-mx:
			batch = append(batch, m)
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
			case m := <-mx:
				batch = append(batch, m)
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

// clockTick waits d, or until ctx ends, so no timer outlives the program.
func clockTick(ctx context.Context) func(time.Duration) tea.Cmd {
	return func(d time.Duration) tea.Cmd {
		return func() tea.Msg {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case now := <-t.C:
				return ClockTickMsg(now)
			case <-ctx.Done():
				return nil
			}
		}
	}
}
