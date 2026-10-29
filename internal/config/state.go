package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// StateVersion is bumped when the state.json schema changes incompatibly.
const StateVersion = 1

const maxStateSize = 4 << 20

// State is the app-owned UI state persisted in state.json. Unknown fields from
// newer versions are dropped; missing fields keep their zero value.
type State struct {
	Version      int        `json:"version"`
	Theme        string     `json:"theme,omitempty"`         // last theme confirmed in the picker
	LastView     int        `json:"last_view,omitempty"`     // 1–6
	IncusProject string     `json:"incus_project,omitempty"` // "" = all projects
	Table        TableState `json:"table,omitzero"`
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
	return st, nil
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
