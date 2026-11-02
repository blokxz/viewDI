package store

import (
	"time"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/updates"
)

// Updates is the update checker's state in a snapshot: the last report,
// the user's snooze and ignore decisions, and what is visible after them.
type Updates struct {
	// Enabled is false when no checker runs (updates.enabled = false).
	Enabled   bool
	Report    updates.Report
	Decisions updates.Decisions
	// Engines and Images are the visible updates (not snoozed, not
	// ignored); they drive the badge, the markers and the alerts.
	Engines []updates.EngineStatus
	Images  []updates.ImageUpdate
	byKey   map[engine.Key]int // instance → index into Images
}

// Count is the number shown in the header badge.
func (u Updates) Count() int { return len(u.Engines) + len(u.Images) }

// ImageUpdate returns the visible image update of an instance, if any.
func (s *Snapshot) ImageUpdate(k engine.Key) (updates.ImageUpdate, bool) {
	if s == nil {
		return updates.ImageUpdate{}, false
	}
	i, ok := s.Updates.byKey[k]
	if !ok {
		return updates.ImageUpdate{}, false
	}
	return s.Updates.Images[i], true
}

// updateState is the store's copy of the checker state.
type updateState struct {
	enabled   bool
	report    updates.Report
	decisions updates.Decisions
	view      Updates
}

// SetUpdatesEnabled records whether a checker runs.
func (s *Store) SetUpdatesEnabled(on bool, now time.Time) {
	s.updates.enabled = on
	s.recomputeUpdates(now)
	s.version++
	s.snap = nil
}

// ApplyUpdates stores a checker report.
func (s *Store) ApplyUpdates(r updates.Report, now time.Time) {
	s.updates.report = r
	s.recomputeUpdates(now)
	s.version++
	s.snap = nil
}

// SetDecisions replaces the snooze and ignore decisions.
func (s *Store) SetDecisions(d updates.Decisions, now time.Time) {
	s.updates.decisions = d
	s.recomputeUpdates(now)
	s.version++
	s.snap = nil
}

// Decisions returns the current snooze and ignore decisions.
func (s *Store) Decisions() updates.Decisions { return s.updates.decisions }

// recomputeUpdates rebuilds the visible updates at now (snoozes expire)
// and reports whether they changed.
func (s *Store) recomputeUpdates(now time.Time) bool {
	u := &s.updates
	v := Updates{Enabled: u.enabled, Report: u.report, Decisions: u.decisions, byKey: map[engine.Key]int{}}
	for _, e := range u.report.Engines {
		if e.HasUpdate() && u.decisions.Visible(e.Key(), e.Available, now) {
			v.Engines = append(v.Engines, e)
		}
	}
	for _, img := range u.report.Images {
		if u.decisions.Visible(img.Key(), img.Available, now) {
			for _, k := range img.Instances {
				v.byKey[k] = len(v.Images)
			}
			v.Images = append(v.Images, img)
		}
	}
	changed := !sameUpdates(u.view, v)
	u.view = v
	return changed
}

// sameUpdates compares the visible sets (by key and version).
func sameUpdates(a, b Updates) bool {
	if a.Enabled != b.Enabled || len(a.Engines) != len(b.Engines) || len(a.Images) != len(b.Images) {
		return false
	}
	for i := range a.Engines {
		if a.Engines[i].Key() != b.Engines[i].Key() || a.Engines[i].Available != b.Engines[i].Available {
			return false
		}
	}
	for i := range a.Images {
		if a.Images[i].Key() != b.Images[i].Key() || a.Images[i].Available != b.Images[i].Available {
			return false
		}
	}
	return true
}
