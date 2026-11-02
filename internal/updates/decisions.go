package updates

import (
	"maps"
	"time"
)

// SnoozeFor is how long z hides an update.
const SnoozeFor = 7 * 24 * time.Hour

// MaxDecisions caps each decision map (state.json stays small).
const MaxDecisions = 500

// Decisions are the user's choices about updates, kept in state.json:
// snoozed keys (until when) and ignored versions per key. An ignored
// version hides that version only: a newer one shows again.
type Decisions struct {
	Snoozed map[string]time.Time `json:"snoozed,omitempty"`
	Ignored map[string]string    `json:"ignored,omitempty"`
}

// Hidden reports why an update is hidden at now: snoozed, ignored, or
// neither.
func (d Decisions) Hidden(key, version string, now time.Time) (snoozed, ignored bool) {
	if until, ok := d.Snoozed[key]; ok && now.Before(until) {
		snoozed = true
	}
	if v, ok := d.Ignored[key]; ok && v == version {
		ignored = true
	}
	return snoozed, ignored
}

// Visible reports whether an update counts (badge, markers, alerts, toasts).
func (d Decisions) Visible(key, version string, now time.Time) bool {
	s, i := d.Hidden(key, version, now)
	return !s && !i
}

// Snooze returns a copy of d with key snoozed until now+SnoozeFor, or
// unsnoozed when it already was.
func (d Decisions) Snooze(key string, now time.Time) Decisions {
	out := d.clone()
	if until, ok := out.Snoozed[key]; ok && now.Before(until) {
		delete(out.Snoozed, key)
		return out
	}
	if out.Snoozed == nil {
		out.Snoozed = map[string]time.Time{}
	}
	out.Snoozed[key] = now.Add(SnoozeFor)
	return out.trim(now)
}

// Ignore returns a copy of d with version of key ignored, or no longer
// ignored when it already was.
func (d Decisions) Ignore(key, version string) Decisions {
	out := d.clone()
	if v, ok := out.Ignored[key]; ok && v == version {
		delete(out.Ignored, key)
		return out
	}
	if out.Ignored == nil {
		out.Ignored = map[string]string{}
	}
	out.Ignored[key] = version
	if len(out.Ignored) > MaxDecisions {
		for k := range out.Ignored {
			if k != key {
				delete(out.Ignored, k)
				break
			}
		}
	}
	return out
}

func (d Decisions) clone() Decisions {
	return Decisions{Snoozed: maps.Clone(d.Snoozed), Ignored: maps.Clone(d.Ignored)}
}

// trim drops expired snoozes, and the oldest ones beyond MaxDecisions.
func (d Decisions) trim(now time.Time) Decisions {
	for k, until := range d.Snoozed {
		if !now.Before(until) {
			delete(d.Snoozed, k)
		}
	}
	for len(d.Snoozed) > MaxDecisions {
		var oldest string
		for k, until := range d.Snoozed {
			if oldest == "" || until.Before(d.Snoozed[oldest]) {
				oldest = k
			}
		}
		delete(d.Snoozed, oldest)
	}
	return d
}
