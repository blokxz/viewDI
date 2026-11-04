package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/blokxz/viewDI/internal/updates"
)

// StateVersion is bumped when the state.json schema changes incompatibly.
const StateVersion = 1

const maxStateSize = 4 << 20

// State is the app-owned UI state persisted in state.json. Unknown fields from
// newer versions are dropped; missing fields keep their zero value.
type State struct {
	Version      int        `json:"version"`
	Theme        string     `json:"theme,omitempty"`         // last theme confirmed in the picker
	LastView     int        `json:"last_view,omitempty"`     // before Stage 9: 1–7; read only, see StartView
	View         string     `json:"view,omitempty"`          // "home", "machines", "library" or "activity"
	IncusProject string     `json:"incus_project,omitempty"` // "" = all projects
	Table        TableState `json:"table,omitzero"`
	// DetailTabs is the last detail tab used per engine ("docker" → "Logs").
	DetailTabs map[string]string `json:"detail_tabs,omitempty"`
	Tree       TreeState         `json:"tree,omitzero"`
	// Updates are the snoozed and ignored updates.
	Updates updates.Decisions `json:"updates,omitzero"`
	Home    HomeState         `json:"home,omitzero"`
	// Machines is the Machines view's mode.
	Machines MachinesState `json:"machines,omitzero"`
	// Palette remembers what the command palette ran last.
	Palette PaletteState `json:"palette,omitzero"`
	// Hub holds the user's Docker Hub favourites.
	Hub HubState `json:"hub,omitzero"`
}

// Views of the app, as state.json names them.
const (
	ViewHome     = "home"
	ViewMachines = "machines"
	ViewLibrary  = "library"
	ViewActivity = "activity"
)

// legacyViews maps the numbered views of releases before Stage 9 (1 Admin,
// 2 Resources, 3 Table, 4 Tree, 5 Jobs, 6 Updates, 7 Library) to the views
// that replaced them.
var legacyViews = map[int]string{
	1: ViewHome, 2: ViewHome, 3: ViewMachines, 4: ViewMachines, 5: ViewActivity, 6: ViewActivity, 7: ViewLibrary,
}

// StartView is the view to open on: the saved one, or the one that replaced
// the numbered view an older state.json remembers. "" means the default.
func (s State) StartView() string {
	if s.View != "" {
		return s.View
	}
	return legacyViews[s.LastView]
}

// PaletteState is the palette's memory: item IDs, most recent first.
type PaletteState struct {
	Recent []string `json:"recent,omitempty"`
}

// MaxPaletteRecent caps the remembered palette items.
const MaxPaletteRecent = 20

// MaxHubFavourites caps the Docker Hub favourites.
const MaxHubFavourites = 100

// maxHubRef is the longest image name kept as a favourite.
const maxHubRef = 255

// HubState is the Docker Hub browser's memory: favourite images, in the
// order they were added.
type HubState struct {
	Favourites []string `json:"favourites,omitempty"`
}

// MachinesState is the Machines view's persisted mode ("tree", the default,
// or "list").
type MachinesState struct {
	Mode string `json:"mode,omitempty"`
}

// MaxPinnedApps caps the apps pinned to the top of the Home.
const MaxPinnedApps = 50

// HomeState is the Home's persisted card density and pinned apps.
type HomeState struct {
	Density string   `json:"density,omitempty"` // "" = automatic, else cards, rows or mosaic
	Pinned  []string `json:"pinned,omitempty"`  // app IDs, in the order pinned
}

// MaxTreeToggled caps the remembered expansion changes per grouping.
const MaxTreeToggled = 2000

// TreeState is the Tree view's grouping and, per grouping, the nodes whose
// expansion the user changed from the default.
type TreeState struct {
	Grouping string              `json:"grouping,omitempty"`
	Toggled  map[string][]string `json:"toggled,omitempty"` // grouping → node IDs
}

// TableState is the Table view's persisted sort and engine filter.
type TableState struct {
	Sort    string `json:"sort,omitempty"` // column ID
	Reverse bool   `json:"reverse,omitempty"`
	Engine  string `json:"engine,omitempty"` // "" = all
}

// LoadState reads state.json. A missing file yields a zero State. A corrupt
// file is renamed to state.json.bak (F15) and a warning is returned.
func LoadState(path string) (State, []Warning) {
	data, err := ReadCapped(path, maxStateSize)
	if errors.Is(err, fs.ErrNotExist) {
		return State{Version: StateVersion}, nil
	}
	if err != nil {
		return State{Version: StateVersion}, []Warning{{Source: path, Msg: fmt.Sprintf("cannot read state: %v", err)}}
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		w := Warning{Source: path, Msg: "corrupt state file, starting fresh"}
		if rerr := os.Rename(path, path+".bak"); rerr == nil {
			w.Msg += " (saved as " + filepath.Base(path) + ".bak)"
		}
		return State{Version: StateVersion}, []Warning{w}
	}
	st.Version = StateVersion
	st.Hub.Favourites = cleanFavourites(st.Hub.Favourites)
	if len(st.Home.Pinned) > MaxPinnedApps {
		st.Home.Pinned = st.Home.Pinned[:MaxPinnedApps]
	}
	return st, nil
}

// cleanFavourites drops empty, oversized and repeated names and keeps at most
// MaxHubFavourites; the browser checks the rest before using a name.
func cleanFavourites(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range in {
		if f == "" || len(f) > maxHubRef || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
		if len(out) == MaxHubFavourites {
			break
		}
	}
	return out
}

// SaveState writes state.json atomically: temp file in the same directory,
// fsync, rename. The directory is created with 0700 if needed.
func SaveState(path string, st State) error {
	st.Version = StateVersion
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return WriteFileAtomic(path, append(data, '\n'), 0o600)
}

// WriteFileAtomic replaces path with data so readers see either the old or the
// new content, never a partial write.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
