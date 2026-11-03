package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/picker"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	activityview "github.com/blokxz/viewDI/internal/ui/views/activity"
	"github.com/blokxz/viewDI/internal/ui/views/detail"
)

// pickerMenu identifies the action menu of a machine; the machine's key
// follows the "|".
const pickerMenu = "machine-menu"

// Menu choices that are not instance actions.
const (
	menuLogs    = "logs"
	menuDetails = "details"
	menuImage   = "image"
)

// menuItems lists the verbs that make sense for in right now, in plain
// words, each with the direct key that does the same. --read-only keeps only
// the ones that change nothing.
func (m Model) menuItems(in engine.Instance) []picker.Item {
	km := m.keys
	key := func(a string) string { return km.Binding(keys.Instance, a).Help().Key }
	var items []picker.Item
	add := func(id, label, detail string) {
		items = append(items, picker.Item{ID: id, Label: label, Detail: detail})
	}
	running := in.State == engine.StateRunning
	rw := !m.opts.ReadOnly
	if running && len(browseTargets(in, m.opts.BrowsePorts, m.opts.AddressOK)) > 0 {
		add(keys.ActBrowse, "Open in the browser", key(keys.ActBrowse))
	}
	if running && rw {
		add(keys.ActShell, "Open a shell", key(keys.ActShell))
	}
	add(menuLogs, "Read the logs", "")
	if rw {
		switch in.State {
		case engine.StateRunning:
			add(keys.ActRestart, "Restart", key(keys.ActRestart))
			add(keys.ActStop, "Stop", key(keys.ActStop))
			add(keys.ActPause, "Pause", key(keys.ActPause))
		case engine.StatePaused:
			add(keys.ActPause, "Resume", key(keys.ActPause))
			add(keys.ActStop, "Stop", key(keys.ActStop))
		case engine.StateStopped, engine.StateCreated:
			add(keys.ActStart, "Start", key(keys.ActStart))
		}
		if _, ok := m.store.Snapshot().ImageUpdate(in.Key()); ok {
			add(menuImage, "Update the image", "")
		}
	}
	add(menuDetails, "Details", "→")
	if rw {
		add(keys.ActDelete, "Delete…", key(keys.ActDelete))
	}
	return items
}

// openMenu opens the action menu of in.
func (m Model) openMenu(in engine.Instance) tea.Cmd {
	title := textsafe.Clean(in.Name) + " · " + in.Engine.Label()
	return modal.Open(picker.New(m.keys, pickerMenu+"|"+string(in.Key()), title, m.menuItems(in), ""))
}

// menuChoice runs a menu choice through the same code as the direct key, so
// confirmations and --read-only apply unchanged.
func (m Model) menuChoice(k engine.Key, id string) (tea.Model, tea.Cmd) {
	in, ok := m.store.Snapshot().Lookup(k)
	if !ok {
		return m, toast.Show(theme.LevelInfo, "That machine no longer exists")
	}
	return m.machineAction(in, id)
}

// machineAction runs one verb on one machine: the menu and the palette.
func (m Model) machineAction(in engine.Instance, id string) (tea.Model, tea.Cmd) {
	switch id {
	case keys.ActBrowse:
		return m.browseOf(in, false)
	case keys.ActShell:
		return m.openShell(in)
	case menuLogs:
		return m.openDetailTab(in, detail.TabLogs)
	case menuDetails:
		return m.openDetail(in)
	case menuImage:
		img, ok := m.store.Snapshot().ImageUpdate(in.Key())
		if !ok {
			return m, toast.Show(theme.LevelInfo, "No image update is waiting for "+textsafe.Clean(in.Name))
		}
		act := activityview.ActPull
		if in.Engine == engine.Incus {
			act = activityview.ActRefresh
		}
		return m.imageAction(activityview.ImageActionMsg{Action: act, Image: &img})
	case keys.ActLimits:
		return m.raiseLimit(in)
	case keys.ActStart, keys.ActStop, keys.ActRestart, keys.ActPause, keys.ActDelete:
		return m.instanceActionOn(id, []engine.Instance{in})
	}
	return m, nil
}
