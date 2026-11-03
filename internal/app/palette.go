package app

import (
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/palette"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	homeview "github.com/blokxz/viewDI/internal/ui/views/home"
)

// Palette item IDs: "view:<n>", "do:<global action>", "density", "m:<key>"
// (a machine), "a:<verb>:<key>" (a verb on a machine), "lib:<entry>".
const (
	palView    = "view:"
	palDo      = "do:"
	palDensity = "density"
	palMachine = "m:"
	palAction  = "a:"
	palLibrary = "lib:"
)

// paletteItems lists what the palette can do now, from the store snapshot and
// the catalog only. --read-only drops the verbs that change something (the
// menu's own filter) and the library, which creates machines.
func (m Model) paletteItems() []palette.Item {
	km := m.keys
	gkey := func(a string) string { return km.Binding(keys.Global, a).Help().Key }
	var items []palette.Item
	for i, a := range keys.ViewActions {
		v := m.views[i]
		items = append(items, palette.Item{ID: palView + strconv.Itoa(i), Label: "Go to " + v.Title(), Group: "Views", Key: gkey(a)})
	}
	do := func(id, label, action string) {
		items = append(items, palette.Item{ID: palDo + id, Label: label, Group: "Settings", Key: gkey(action)})
	}
	do(keys.ActTheme, "Change the theme", keys.ActTheme)
	do(keys.ActCheckUpdates, "Check for updates", keys.ActCheckUpdates)
	do(keys.ActReconnect, "Reconnect to the engines", keys.ActReconnect)
	do(keys.ActHelp, "Show every key", keys.ActHelp)
	items = append(items, palette.Item{ID: palDensity, Label: "Change the Home density", Group: "Settings", Key: km.Binding(keys.Home, "density").Help().Key})
	if !m.opts.ReadOnly {
		do(keys.ActNew, "Create a container or VM", keys.ActNew)
		do(keys.ActImport, "Import an image or backup", keys.ActImport)
	}
	for _, in := range m.store.Snapshot().Instances {
		name := textsafe.Clean(in.Name)
		k := string(in.Key())
		items = append(items, palette.Item{ID: palMachine + k, Label: name, Group: "Machines", Words: in.Engine.Label() + " " + in.State.String()})
		for _, it := range m.menuItems(in) {
			if it.ID == menuDetails {
				continue
			}
			label := strings.TrimSuffix(it.Label, "…") + " · " + name
			items = append(items, palette.Item{ID: palAction + it.ID + ":" + k, Label: label, Group: "Actions", Key: it.Detail, Search: true})
		}
		if !m.opts.ReadOnly && in.State == engine.StateRunning {
			items = append(items, palette.Item{ID: palAction + keys.ActLimits + ":" + k, Label: "Raise a limit of " + name, Group: "Actions", Key: km.Binding(keys.Instance, keys.ActLimits).Help().Key, Search: true})
		}
	}
	if !m.opts.ReadOnly && m.opts.Library != nil {
		for _, e := range m.opts.Library.Entries() {
			items = append(items, palette.Item{ID: palLibrary + e.ID, Label: "Run " + textsafe.Clean(e.Name), Group: "Library", Words: e.Category + " " + string(e.Engine)})
		}
	}
	return items
}

// openPalette opens the command palette.
func (m Model) openPalette() tea.Cmd {
	return modal.Open(palette.New(m.keys, m.paletteItems(), m.state.Palette.Recent))
}

// remember puts id first among the palette's recent items.
func (m *Model) remember(id string) {
	r := slices.DeleteFunc(slices.Clone(m.state.Palette.Recent), func(s string) bool { return s == id })
	r = append([]string{id}, r...)
	if len(r) > config.MaxPaletteRecent {
		r = r[:config.MaxPaletteRecent]
	}
	m.state.Palette.Recent = r
}

// paletteChoice runs what the palette picked through the code the keys use,
// so confirmations and --read-only apply unchanged.
func (m Model) paletteChoice(id string) (tea.Model, tea.Cmd) {
	m.remember(id)
	save := m.saveState()
	model, cmd := m.paletteRun(id)
	return model, tea.Batch(save, cmd)
}

func (m Model) paletteRun(id string) (tea.Model, tea.Cmd) {
	snap := m.store.Snapshot()
	switch {
	case strings.HasPrefix(id, palView):
		i, err := strconv.Atoi(strings.TrimPrefix(id, palView))
		if err != nil || i < 0 || i >= len(m.views) {
			return m, nil
		}
		cmd := m.closeDetail()
		m.switchView(i)
		if i == viewLibrary {
			cmd = tea.Batch(cmd, m.loadLocalImages())
		}
		return m, cmd
	case strings.HasPrefix(id, palDo):
		return m.globalAction(strings.TrimPrefix(id, palDo))
	case id == palDensity:
		hv, ok := m.views[viewHome].(*homeview.View)
		if !ok {
			return m, nil
		}
		return m, tea.Batch(hv.CycleDensity(), toast.Show(theme.LevelInfo, "Home density changed (d on the Home)"))
	case strings.HasPrefix(id, palMachine):
		in, ok := snap.Lookup(engine.Key(strings.TrimPrefix(id, palMachine)))
		if !ok {
			return m, toast.Show(theme.LevelInfo, "That machine no longer exists")
		}
		if m.opts.EnterDetails {
			return m.openDetail(in)
		}
		return m, m.openMenu(in)
	case strings.HasPrefix(id, palAction):
		verb, key, ok := strings.Cut(strings.TrimPrefix(id, palAction), ":")
		if !ok {
			return m, nil
		}
		in, found := snap.Lookup(engine.Key(key))
		if !found {
			return m, toast.Show(theme.LevelInfo, "That machine no longer exists")
		}
		return m.machineAction(in, verb)
	case strings.HasPrefix(id, palLibrary):
		if m.opts.Library == nil || m.opts.ReadOnly {
			return m, nil
		}
		e, ok := m.opts.Library.Get(strings.TrimPrefix(id, palLibrary))
		if !ok {
			return m, nil
		}
		return m.runLibrary(e)
	}
	return m, nil
}
