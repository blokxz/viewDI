package apps

import "github.com/blokxz/viewDI/internal/engine"

// Tracker remembers which machines stopped on their own while viewDI was
// watching. A machine that is already stopped when viewDI starts is simply
// off: nothing says it was meant to run. A machine seen running that then
// stops, with no viewDI job having stopped it, is down until it runs again
// or disappears. This is a heuristic and the docs say so: it cannot tell a
// stop typed in another terminal from a crash.
type Tracker struct {
	running map[engine.Key]bool
	down    map[engine.Key]bool
}

// NewTracker returns an empty tracker.
func NewTracker() *Tracker {
	return &Tracker{running: map[engine.Key]bool{}, down: map[engine.Key]bool{}}
}

// Observe records a new view of the machines. asked are the machines a
// viewDI job is stopping or deleting (or just did), whose stop is on purpose.
func (t *Tracker) Observe(instances []engine.Instance, asked map[engine.Key]bool) {
	seen := map[engine.Key]bool{}
	for _, m := range instances {
		k := m.Key()
		seen[k] = true
		switch m.State {
		case engine.StateRunning, engine.StatePaused, engine.StateRestarting:
			t.running[k] = true
			delete(t.down, k)
		case engine.StateStopped, engine.StateDead, engine.StateError:
			if t.running[k] && !asked[k] {
				t.down[k] = true
			}
			delete(t.running, k)
		}
	}
	for k := range t.running {
		if !seen[k] {
			delete(t.running, k)
		}
	}
	for k := range t.down {
		if !seen[k] {
			delete(t.down, k)
		}
	}
}

// Down returns the machines that stopped on their own. The map is the
// tracker's own: callers must not change it.
func (t *Tracker) Down() map[engine.Key]bool { return t.down }

// Forget drops a machine from the down set (the user said it is fine).
func (t *Tracker) Forget(k engine.Key) { delete(t.down, k) }
