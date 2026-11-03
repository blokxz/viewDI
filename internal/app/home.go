package app

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/apps"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/theme"
	homeview "github.com/blokxz/viewDI/internal/ui/views/home"
	"github.com/blokxz/viewDI/internal/ui/views/table"
	updatesview "github.com/blokxz/viewDI/internal/ui/views/updates"
)

// askedFor is how long a machine counts as stopped on purpose after a viewDI
// job last had an operation on it: the engine reports the new state a little
// after the job ends.
const askedFor = 30 * time.Second

// homeViewIndex is the Home's place among the views.
const homeViewIndex = 0

// updateHome derives the apps and the needs-you items from the store and
// gives them to the Home. It is cheap and runs on every data change.
func (m *Model) updateHome() {
	snap := m.store.Snapshot()
	if m.tracker == nil {
		m.tracker = apps.NewTracker()
		m.askedUntil = map[engine.Key]time.Time{}
	}
	for _, in := range snap.Instances {
		if _, pending := snap.Pending(in.Key()); pending {
			m.askedUntil[in.Key()] = m.now.Add(askedFor)
		}
	}
	asked := map[engine.Key]bool{}
	for k, until := range m.askedUntil {
		if until.After(m.now) {
			asked[k] = true
		} else {
			delete(m.askedUntil, k)
		}
	}
	m.tracker.Observe(snap.Instances, asked)
	in := apps.Inputs{
		Instances: snap.Instances, Alerts: snap.Alerts, Labs: m.labs, Points: snap.Points(), Down: m.tracker.Down(), Fine: m.tracker.Fine(),
	}
	as := apps.Derive(in)
	m.views[homeViewIndex], _ = m.views[homeViewIndex].Update(homeview.DerivedMsg{Apps: as, Needs: apps.Needs(in, as, m.opts.ReadOnly)})
}

// homeAction runs an action of a needs-you item through the code the other
// views already use, so it asks for the same confirmations and respects
// --read-only the same way.
func (m Model) homeAction(msg homeview.ActionMsg) (tea.Model, tea.Cmd) {
	it := msg.Item
	snap := m.store.Snapshot()
	in, haveMachine := snap.Lookup(it.Machine)
	switch msg.Action {
	case apps.ActStart:
		if !haveMachine {
			return m, nil
		}
		return m.submitActions(engine.Start, []engine.Instance{in})
	case apps.ActRestart:
		if !haveMachine {
			return m, nil
		}
		return m.submitActions(engine.Restart, []engine.Instance{in})
	case apps.ActLogs:
		if !haveMachine {
			return m, nil
		}
		return m.openDetail(in)
	case apps.ActFine:
		if m.tracker != nil {
			m.tracker.Forget(it.Machine)
		}
		m.updateHome()
		return m, nil
	case apps.ActUpgrade:
		for _, e := range snap.Updates.Engines {
			if e.Engine == it.Engine {
				st := e
				return m.upgradeEngine(updatesview.UpgradeMsg{Engine: &st})
			}
		}
		return m, toast.Show(theme.LevelInfo, "No upgrade is pending for "+it.Engine.Label())
	case apps.ActLater:
		for _, e := range snap.Updates.Engines {
			if e.Engine == it.Engine {
				return m.decide(updatesview.SnoozeMsg{Key: e.Key(), Text: it.Subject})
			}
		}
		return m, nil
	case apps.ActSee:
		m.switchView(5) // the Updates view, until the Activity feed exists
		return m, nil
	case apps.ActRetry:
		src := m.opts.Source
		if src == nil {
			return m, toast.Show(theme.LevelInfo, "No engines to reconnect")
		}
		return m, tea.Batch(func() tea.Msg { src.Reconnect(); return nil }, toast.Show(theme.LevelInfo, "Reconnecting / resyncing engines…"))
	}
	return m, nil
}

// openApp shows the machines of an app: the Table, restricted to them.
func (m Model) openApp(a apps.App) (tea.Model, tea.Cmd) {
	closeCmd := m.closeDetail()
	m.switchView(2)
	if t, ok := m.views[2].(*table.View); ok {
		t.SetOnly(a.Machines, a.Name)
	}
	return m, closeCmd
}
