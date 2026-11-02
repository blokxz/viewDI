// Package config resolves viewDI's XDG paths and loads the user configuration
// (config.toml, human-edited) and the UI state (state.json, app-owned).
//
// Nothing in this package aborts startup: missing or invalid files fall back to
// defaults and are reported as Warnings for the UI to display.
package config

import (
	"path/filepath"

	"github.com/adrg/xdg"
)

// AppDir is the directory name used under every XDG base directory.
const AppDir = "viewDI"

// Paths holds the base directories viewDI reads and writes. It is a plain value
// so tests can point every path at a temporary directory; adrg/xdg resolves the
// environment once at process start, which makes t.Setenv ineffective.
type Paths struct {
	ConfigDir string // ~/.config/viewDI
	StateDir  string // ~/.local/state/viewDI
	CacheDir  string // ~/.cache/viewDI
	DataDir   string // ~/.local/share/viewDI
	// RuntimeDir is $XDG_RUNTIME_DIR (not viewDI's own): rootless Docker
	// puts its socket there.
	RuntimeDir string
	// Home is the user's home directory (the file picker's ~).
	Home string
}

// DefaultPaths returns the XDG locations for the current user.
func DefaultPaths() Paths {
	return Paths{
		ConfigDir: filepath.Join(xdg.ConfigHome, AppDir),
		StateDir:  filepath.Join(xdg.StateHome, AppDir),
		CacheDir:  filepath.Join(xdg.CacheHome, AppDir),
		DataDir:   filepath.Join(xdg.DataHome, AppDir),

		RuntimeDir: xdg.RuntimeDir,
		Home:       xdg.Home,
	}
}

// PathsUnder returns Paths rooted at dir, mirroring the XDG layout. Used by tests.
func PathsUnder(dir string) Paths {
	return Paths{
		ConfigDir: filepath.Join(dir, "config", AppDir),
		StateDir:  filepath.Join(dir, "state", AppDir),
		CacheDir:  filepath.Join(dir, "cache", AppDir),
		DataDir:   filepath.Join(dir, "data", AppDir),

		RuntimeDir: filepath.Join(dir, "runtime"),
		Home:       filepath.Join(dir, "home"),
	}
}

// ConfigFile is the human-edited configuration file.
func (p Paths) ConfigFile() string { return filepath.Join(p.ConfigDir, "config.toml") }

// ThemesDir holds user themes that extend or override the built-in ones.
func (p Paths) ThemesDir() string { return filepath.Join(p.ConfigDir, "themes") }

// KeysFile holds optional key binding overrides.
func (p Paths) KeysFile() string { return filepath.Join(p.ConfigDir, "keys.toml") }

// StateFile is the app-owned UI state.
func (p Paths) StateFile() string { return filepath.Join(p.StateDir, "state.json") }

// LabsFile holds the user's labs: blocks of machines and the links
// declared between them.
func (p Paths) LabsFile() string { return filepath.Join(p.DataDir, "labs.json") }

// UpdatesCache holds the last update check results, the release feeds'
// ETags and the updates already seen (it is a cache: losing it only
// costs a re-check).
func (p Paths) UpdatesCache() string { return filepath.Join(p.CacheDir, "updates.json") }

// LogFile is the debug log, written only with --debug.
func (p Paths) LogFile() string { return filepath.Join(p.StateDir, "viewdi.log") }

// TraceFile returns the default message trace path for the given timestamp suffix.
func (p Paths) TraceFile(stamp string) string {
	return filepath.Join(p.StateDir, "trace-"+stamp+".log")
}
