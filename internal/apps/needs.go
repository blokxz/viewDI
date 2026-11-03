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
var mutating = map[string]bool{ActStart: true, ActRestart: true, ActUpgrade: true}

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

func alertItem(al alerts.Alert) (Item, bool) {
	it := Item{ID: "alert:" + al.ID, Severity: al.Severity, Subject: al.Subject, Machine: al.Key, Engine: al.Engine, Rule: al.Rule, Since: al.Since, Text: shortAlert(al)}
	switch al.Rule {
	case alerts.RuleMemory, alerts.RuleCPU:
		it.Actions = []Action{{ActLogs, "logs"}}
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
