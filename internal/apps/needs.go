package apps

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/blokxz/viewDI/internal/alerts"
	"github.com/blokxz/viewDI/internal/engine"
)

// Action ids of a needs-you item. The UI binds a key to each and the app
// runs it through the code that already exists (jobs, updates, the confirm
// modal), so an item adds no way to change anything.
const (
	ActStart   = "start"   // start a machine
	ActRestart = "restart" // restart a machine
	ActLimits  = "limits"  // raise its memory or CPU limit
	ActLogs    = "logs"    // open its logs
	ActFine    = "fine"    // it is fine: stop reporting this stop
	ActUpgrade = "upgrade" // upgrade an engine (shows the command first)
	ActLater   = "later"   // snooze an update
	ActSee     = "see"     // open the Activity feed
	ActRetry   = "retry"   // reconnect to an engine now
)

// Action is one thing the user can do about an item.
type Action struct {
	ID    string
	Label string // "start it", "logs"
}

// Item is one line of the Home's NEEDS YOU list.
type Item struct {
	ID       string // stable while the condition holds
	Severity alerts.Severity
	Subject  string // machine or engine name
	Text     string
	Machine  engine.Key // "" for engine items
	Engine   engine.ID
	Rule     alerts.Rule // "" for a machine that stopped on its own
	Since    time.Time
	Actions  []Action // the first is what enter does
}

// mutating are the actions that change something on a daemon or run a
// command; --read-only removes them.
var mutating = map[string]bool{ActStart: true, ActRestart: true, ActUpgrade: true, ActLimits: true}

// Needs lists what needs the user, worst first: machines that stopped on
// their own, then alerts (errors, warnings), then updates. It reads the same
// data the Home derived its apps from, and nothing else. readOnly drops the
// actions that would change something.
func Needs(in Inputs, as []App, readOnly bool) []Item {
	names := map[engine.Key]string{}
	for _, m := range in.Instances {
		names[m.Key()] = m.Name
	}
	var out []Item
	for _, a := range as {
		for _, k := range a.Downs {
			out = append(out, Item{
				ID: "down:" + string(k), Severity: alerts.Error, Subject: names[k], Machine: k, Engine: k.Engine(),
				Text:    "stopped and was not asked to",
				Actions: []Action{{ActStart, "start it"}, {ActLogs, "logs"}, {ActFine, "it is fine"}},
			})
		}
	}
	out = append(out, orphanCopies(in.Instances)...)
	for _, al := range in.Alerts {
		if it, ok := alertItem(al); ok {
			out = append(out, it)
		}
	}
	slices.SortStableFunc(out, func(a, b Item) int {
		return cmp.Or(cmp.Compare(b.Severity, a.Severity), a.Since.Compare(b.Since), strings.Compare(a.ID, b.ID))
	})
	if readOnly {
		for i := range out {
			out[i].Actions = slices.DeleteFunc(slices.Clone(out[i].Actions), func(a Action) bool { return mutating[a.ID] })
		}
	}
	return out
}

// copyInfix is what the Incus adapter puts in the name of a rebuild's copy
// (incus.CopyInfix).
const copyInfix = "-pre-rebuild-"

// orphanCopies reports the copies of a rebuild whose instance is gone: the
// rebuild failed after it deleted the original and the copy could not take
// its name back, so the data is only in the copy.
func orphanCopies(ms []engine.Instance) []Item {
	type at struct {
		e engine.ID
		p string
		n string
	}
	have := make(map[at]bool, len(ms))
	for _, m := range ms {
		have[at{m.Engine, m.Project, m.Name}] = true
	}
	var out []Item
	for _, m := range ms {
		if m.Engine != engine.Incus {
			continue
		}
		base, _, ok := strings.Cut(m.Name, copyInfix)
		if !ok || base == "" || have[at{m.Engine, m.Project, base}] {
			continue
		}
		out = append(out, Item{
			ID: "orphan:" + string(m.Key()), Severity: alerts.Error, Subject: m.Name, Machine: m.Key(), Engine: m.Engine,
			Text:    base + " is gone and its data is in this copy · incus rename " + m.Name + " " + base,
			Actions: []Action{{ActSee, "see it"}},
		})
	}
	return out
}

func alertItem(al alerts.Alert) (Item, bool) {
	it := Item{ID: "alert:" + al.ID, Severity: al.Severity, Subject: al.Subject, Machine: al.Key, Engine: al.Engine, Rule: al.Rule, Since: al.Since, Text: shortAlert(al)}
	switch al.Rule {
	case alerts.RuleMemory, alerts.RuleCPU:
		it.Actions = []Action{{ActLogs, "logs"}, {ActLimits, "raise the limit"}}
	case alerts.RuleUnhealthy, alerts.RuleRestartLoop:
		it.Actions = []Action{{ActLogs, "logs"}, {ActRestart, "restart"}}
	case alerts.RuleEngineUnavailable, alerts.RuleEngineDegraded:
		it.Actions = []Action{{ActRetry, "reconnect now"}}
	case alerts.RuleEngineUpdate:
		it.Actions = []Action{{ActUpgrade, "upgrade now"}, {ActLater, "later"}}
	case alerts.RuleImageUpdates:
		it.Actions = []Action{{ActSee, "see them"}}
	default:
		if al.Severity < alerts.Warn {
			return Item{}, false
		}
		it.Actions = []Action{{ActLogs, "logs"}}
	}
	if al.Severity < alerts.Warn && al.Rule != alerts.RuleEngineUpdate && al.Rule != alerts.RuleImageUpdates {
		return Item{}, false
	}
	return it, true
}
