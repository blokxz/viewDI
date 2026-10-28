package theme

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/blokxz/viewDI/internal/config"
)

// maxThemeSize caps user theme files.
const maxThemeSize = 64 << 10

// Registry holds every loadable theme, sorted by display name.
type Registry struct {
	themes []*Theme
	byID   map[string]*Theme
}

// LoadRegistry parses the built-in themes from builtin (flat directory of
// *.toml) and then the user themes from userDir, which override built-ins
// with the same ID. Invalid user themes are skipped with a warning; a missing
// userDir is not an error. Built-in themes must parse (covered by tests).
func LoadRegistry(builtin fs.FS, userDir string) (*Registry, []config.Warning) {
	r := &Registry{byID: map[string]*Theme{}}
	var warns []config.Warning

	entries, err := fs.ReadDir(builtin, ".")
	if err != nil {
		warns = append(warns, config.Warning{Source: "built-in themes", Msg: err.Error()})
	}
	for _, e := range entries {
		id, ok := themeID(e.Name())
		if !ok || e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(builtin, e.Name())
		if err == nil {
			var t *Theme
			if t, err = Parse(id, data); err == nil {
				t.Source = "built-in"
				r.byID[id] = t
				continue
			}
		}
		warns = append(warns, config.Warning{Source: "built-in theme " + id, Msg: err.Error()})
	}

	if userDir != "" {
		warns = append(warns, r.loadUser(userDir)...)
	}

	for _, t := range r.byID {
		r.themes = append(r.themes, t)
	}
	slices.SortFunc(r.themes, func(a, b *Theme) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return r, warns
}

func (r *Registry) loadUser(dir string) []config.Warning {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []config.Warning{{Source: dir, Msg: err.Error()}}
	}
	var warns []config.Warning
	for _, e := range entries {
		id, ok := themeID(e.Name())
		if !ok || e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := config.ReadCapped(path, maxThemeSize)
		if err != nil {
			warns = append(warns, config.Warning{Source: path, Msg: fmt.Sprintf("theme skipped: %v", err)})
			continue
		}
		t, err := Parse(id, data)
		if err != nil {
			warns = append(warns, config.Warning{Source: path, Msg: fmt.Sprintf("theme skipped: %v", err)})
			continue
		}
		t.Source = path
		r.byID[id] = t
	}
	return warns
}

func themeID(name string) (string, bool) {
	id, ok := strings.CutSuffix(name, ".toml")
	return id, ok && id != "" && !strings.HasPrefix(id, ".")
}

// Themes returns all themes sorted by display name.
func (r *Registry) Themes() []*Theme { return r.themes }

// Get returns a theme by ID.
func (r *Registry) Get(id string) (*Theme, bool) {
	t, ok := r.byID[id]
	return t, ok
}

// Resolve picks the first known ID from candidates (highest precedence first,
// empty strings skipped), falling back to config.DefaultTheme and then to the
// first theme. A requested but unknown ID produces a warning.
func (r *Registry) Resolve(candidates ...string) (*Theme, []config.Warning) {
	var warns []config.Warning
	for _, id := range candidates {
		if id == "" {
			continue
		}
		if t, ok := r.byID[id]; ok {
			return t, warns
		}
		warns = append(warns, config.Warning{Source: "theme", Msg: fmt.Sprintf("unknown theme %q, falling back", id)})
	}
	if t, ok := r.byID[config.DefaultTheme]; ok {
		return t, warns
	}
	if len(r.themes) > 0 {
		return r.themes[0], warns
	}
	return Fallback(), warns
}

// Fallback is a minimal hard-coded theme used only if no theme file could be
// loaded at all, so the UI can always render.
func Fallback() *Theme {
	t, err := Parse("fallback", []byte(`
name = "Fallback"
[tokens]
bg = "#000000"
fg = "#c0c0c0"
accent = "#00afaf"
ok = "#00af00"
warn = "#afaf00"
err = "#af0000"
`))
	if err != nil {
		panic(err) // static input; covered by tests
	}
	return t
}
