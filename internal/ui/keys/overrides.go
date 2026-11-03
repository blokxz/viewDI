package keys

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/blokxz/viewDI/internal/config"
)

const maxKeysFileSize = 64 << 10

// Override replaces the keys of one action. An empty Keys list unbinds it.
type Override struct {
	Scope  Scope
	Action string
	Keys   []string
}

// LoadOverrides reads keys.toml. A missing file yields no overrides.
//
// Format — one table per scope, each value a key or a list of keys:
//
//	[global]
//	theme = "ctrl+t"
//	[instance]
//	pause = ["F", "ctrl+f"]
//	[view.table]
//	sort = "o"
func LoadOverrides(path string) ([]Override, []config.Warning) {
	data, err := config.ReadCapped(path, maxKeysFileSize)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []config.Warning{{Source: path, Msg: fmt.Sprintf("cannot read, using default keys: %v", err)}}
	}
	return ParseOverrides(path, data)
}

// ParseOverrides decodes keys.toml content.
func ParseOverrides(source string, data []byte) ([]Override, []config.Warning) {
	var raw map[string]any
	if err := toml.NewDecoder(bytes.NewReader(data)).Decode(&raw); err != nil {
		return nil, []config.Warning{{Source: source, Msg: fmt.Sprintf("invalid TOML, using default keys: %v", err)}}
	}
	var out []Override
	var warns []config.Warning
	warn := func(format string, a ...any) {
		warns = append(warns, config.Warning{Source: source, Msg: fmt.Sprintf(format, a...)})
	}

	// Flatten nested tables: [view.table] decodes as view → table → actions.
	var walk func(prefix string, tbl map[string]any)
	walk = func(prefix string, tbl map[string]any) {
		for _, name := range sortedKeys(tbl) {
			v := tbl[name]
			full := name
			if prefix != "" {
				full = prefix + "." + name
			}
			if sub, ok := v.(map[string]any); ok {
				walk(full, sub)
				continue
			}
			if prefix == "" {
				warn("%q: bindings must be inside a scope table such as [global]", name)
				continue
			}
			keys, ok := toKeys(v)
			if !ok {
				warn("%s: value must be a key string or a list of key strings", full)
				continue
			}
			out = append(out, Override{Scope: Scope(prefix), Action: name, Keys: keys})
		}
	}
	walk("", raw)
	out, warns = migrateLegacy(source, out, warns)
	return out, warns
}

// legacyViewActions are the view-switch actions Stage 9 renamed, by their new
// names: the tabs Resources, Jobs and Updates folded into Home and Activity.
var legacyViewActions = map[string]string{
	"view_admin":     ActViewHome,
	"view_resources": ActViewHome,
	"view_jobs":      ActViewActivity,
	"view_updates":   ActViewActivity,
}

// legacyScopes are the view scopes that became [view.activity]. Their actions
// kept their names, except the ones listed as gone.
var legacyScopes = map[Scope]map[string]bool{
	"view.jobs":    {"up": true, "down": true, "home": true, "end": true, "cancel": true},
	"view.updates": {"up": true, "down": true, "home": true, "end": true, "pull": true, "refresh": true, "recreate": true, "upgrade": true, "snooze": true, "ignore": true},
}

// migrateLegacy rewrites overrides written for the pre-Stage-9 view and
// action names so an old keys.toml keeps working; each one gets a warning
// that names the new place.
func migrateLegacy(source string, in []Override, warns []config.Warning) ([]Override, []config.Warning) {
	warn := func(format string, a ...any) {
		warns = append(warns, config.Warning{Source: source, Msg: fmt.Sprintf(format, a...)})
	}
	out := in[:0:0]
	for _, o := range in {
		switch {
		case o.Scope == Global && legacyViewActions[o.Action] != "":
			warn("[global] %s was renamed %s; update keys.toml", o.Action, legacyViewActions[o.Action])
			o.Action = legacyViewActions[o.Action]
		case legacyScopes[o.Scope] != nil:
			if !legacyScopes[o.Scope][o.Action] {
				warn("[%s] %s no longer exists: the Activity view replaced it", o.Scope, o.Action)
				continue
			}
			warn("[%s] moved to [view.activity]; update keys.toml", o.Scope)
			o.Scope = Activity
		}
		out = append(out, o)
	}
	return out, warns
}

func toKeys(v any) ([]string, bool) {
	switch v := v.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, false
		}
		return []string{v}, true
	case []any:
		keys := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, false
			}
			keys = append(keys, s)
		}
		return keys, true
	default:
		return nil, false
	}
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

// Apply builds a Map from base plus overrides. Each override is applied in
// order and kept only if the resulting map still validates; rejected overrides
// are reported and the previous binding stays. base must be valid.
func Apply(base []Def, overrides []Override) (*Map, []config.Warning) {
	defs := slices.Clone(base)
	var warns []config.Warning
	for _, o := range overrides {
		if _, ok := ScopeByName(o.Scope); !ok {
			warns = append(warns, config.Warning{Source: "keys.toml", Msg: fmt.Sprintf("unknown scope [%s] ignored", o.Scope)})
			continue
		}
		i := slices.IndexFunc(defs, func(d Def) bool { return d.Scope == o.Scope && d.Action == o.Action })
		if i < 0 {
			warns = append(warns, config.Warning{Source: "keys.toml", Msg: fmt.Sprintf("unknown action %s.%s ignored", o.Scope, o.Action)})
			continue
		}
		prev := defs[i].Keys
		defs[i].Keys = o.Keys
		if errs := Validate(defs); len(errs) > 0 {
			defs[i].Keys = prev
			warns = append(warns, config.Warning{Source: "keys.toml", Msg: fmt.Sprintf("override %s.%s rejected: %v", o.Scope, o.Action, errs[0])})
		}
	}
	m, err := New(defs)
	if err != nil { // unreachable when base is valid
		return MustDefault(), append(warns, config.Warning{Source: "keys.toml", Msg: err.Error()})
	}
	return m, warns
}
