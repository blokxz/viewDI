// Package keys defines viewDI's layered key map (docs/03-mockups.md, Key map).
//
// Keys resolve through four layers, first match wins: modal → component → view
// → global. Global and instance-action keys are reserved: a view or component
// scope may never bind them, so a key means the same thing everywhere it is
// reserved. The rule is enforced by Validate, for the built-in map and for
// user overrides from keys.toml alike.
package keys

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Scope names a set of bindings.
type Scope string

// Kind classifies scopes for conflict detection.
type Kind int

// Scope kinds.
const (
	KindGlobal    Kind = iota // reserved everywhere
	KindInstance              // reserved in scopes that show instances
	KindView                  // layer 3
	KindComponent             // layer 2
	KindModal                 // layer 1: captures every key, exempt from reservation
)

// ScopeInfo describes a scope.
type ScopeInfo struct {
	Name           Scope
	Kind           Kind
	Title          string // heading in the help overlay
	ShowsInstances bool   // instance-action keys are reserved here too
}

// Built-in scopes.
const (
	Global    Scope = "global"
	Instance  Scope = "instance"
	Table     Scope = "view.table"
	Resources Scope = "view.resources"
	Tree      Scope = "view.tree"
	Updates   Scope = "view.updates"
	Filter    Scope = "component.filter"
	Logs      Scope = "component.logs"
	Overview  Scope = "component.overview"
	Input     Scope = "modal.input"
	Confirm   Scope = "modal.confirm"
	Picker    Scope = "modal.picker"
	Help      Scope = "modal.help"
)

// Scopes lists every scope in help-overlay order.
var Scopes = []ScopeInfo{
	{Name: Global, Kind: KindGlobal, Title: "Global"},
	{Name: Instance, Kind: KindInstance, Title: "Instance actions"},
	{Name: Table, Kind: KindView, Title: "Table", ShowsInstances: true},
	{Name: Resources, Kind: KindView, Title: "Resources"},
	{Name: Tree, Kind: KindView, Title: "Tree", ShowsInstances: true},
	{Name: Updates, Kind: KindView, Title: "Updates"},
	{Name: Filter, Kind: KindComponent, Title: "Filter (Tree, Logs, Inspect)", ShowsInstances: true},
	{Name: Logs, Kind: KindComponent, Title: "Logs tab", ShowsInstances: true},
	{Name: Overview, Kind: KindComponent, Title: "Overview tab", ShowsInstances: true},
	{Name: Input, Kind: KindModal, Title: "Text input (filter prompt)"},
	{Name: Confirm, Kind: KindModal, Title: "Confirm dialog"},
	{Name: Picker, Kind: KindModal, Title: "Picker"},
	{Name: Help, Kind: KindModal, Title: "Help"},
}

// ScopeByName returns a scope's metadata.
func ScopeByName(s Scope) (ScopeInfo, bool) {
	i := slices.IndexFunc(Scopes, func(si ScopeInfo) bool { return si.Name == s })
	if i < 0 {
		return ScopeInfo{}, false
	}
	return Scopes[i], true
}

// Def is one binding definition.
type Def struct {
	Scope  Scope
	Action string
	Keys   []string
	Desc   string
}

// Map is a validated, immutable key map.
type Map struct {
	bindings map[Scope]map[string]key.Binding
	order    map[Scope][]string // action order for help
}

// New builds a Map from defs. It returns an error if defs violate the
// reservation rules (see Validate).
func New(defs []Def) (*Map, error) {
	if errs := Validate(defs); len(errs) > 0 {
		return nil, fmt.Errorf("key map: %w", errs[0])
	}
	m := &Map{bindings: map[Scope]map[string]key.Binding{}, order: map[Scope][]string{}}
	for _, d := range defs {
		if m.bindings[d.Scope] == nil {
			m.bindings[d.Scope] = map[string]key.Binding{}
		}
		opts := []key.BindingOpt{key.WithKeys(d.Keys...), key.WithHelp(helpLabel(d.Keys), d.Desc)}
		if len(d.Keys) == 0 {
			opts = append(opts, key.WithDisabled())
		}
		m.bindings[d.Scope][d.Action] = key.NewBinding(opts...)
		m.order[d.Scope] = append(m.order[d.Scope], d.Action)
	}
	return m, nil
}

// MustDefault returns the built-in map. It panics only if the built-in
// definitions are inconsistent, which TestDefaultMapIsValid prevents.
func MustDefault() *Map {
	m, err := New(Defaults())
	if err != nil {
		panic(err)
	}
	return m
}

// Binding returns a binding; unknown actions yield a disabled binding.
func (m *Map) Binding(s Scope, action string) key.Binding {
	b, ok := m.bindings[s][action]
	if !ok {
		return key.NewBinding(key.WithDisabled())
	}
	return b
}

// Matches reports whether msg triggers action in scope.
func (m *Map) Matches(msg tea.KeyPressMsg, s Scope, action string) bool {
	return key.Matches(msg, m.Binding(s, action))
}

// Action returns the action bound to msg in scope, or "".
func (m *Map) Action(msg tea.KeyPressMsg, s Scope) string {
	for _, a := range m.order[s] {
		if key.Matches(msg, m.bindings[s][a]) {
			return a
		}
	}
	return ""
}

// Bindings returns a scope's enabled bindings in definition order.
func (m *Map) Bindings(s Scope) []key.Binding {
	var out []key.Binding
	for _, a := range m.order[s] {
		if b := m.bindings[s][a]; b.Enabled() {
			out = append(out, b)
		}
	}
	return out
}

// Defs returns the definitions this map was built from, for overriding.
func (m *Map) Defs() []Def {
	var out []Def
	for _, si := range Scopes {
		for _, a := range m.order[si.Name] {
			b := m.bindings[si.Name][a]
			out = append(out, Def{Scope: si.Name, Action: a, Keys: b.Keys(), Desc: b.Help().Desc})
		}
	}
	return out
}

func helpLabel(keys []string) string {
	labels := make([]string, len(keys))
	for i, k := range keys {
		switch k {
		case "up":
			k = "↑"
		case "down":
			k = "↓"
		case "left":
			k = "←"
		case "right":
			k = "→"
		}
		labels[i] = k
	}
	return strings.Join(labels, "/")
}

// Validate checks definitions against the scoping rules:
//   - every Def names a known scope, and actions are unique per scope;
//   - within a scope, a key triggers at most one action;
//   - global and instance keys never overlap;
//   - view and component scopes never bind a global key, nor an instance key
//     if they show instances;
//   - quit always keeps ctrl+c.
func Validate(defs []Def) []error {
	var errs []error
	keyOwner := map[Scope]map[string]string{} // scope → key → action
	seen := map[Scope]map[string]bool{}
	for _, d := range defs {
		if _, ok := ScopeByName(d.Scope); !ok {
			errs = append(errs, fmt.Errorf("unknown scope %q", d.Scope))
			continue
		}
		if seen[d.Scope] == nil {
			seen[d.Scope] = map[string]bool{}
			keyOwner[d.Scope] = map[string]string{}
		}
		if seen[d.Scope][d.Action] {
			errs = append(errs, fmt.Errorf("%s: duplicate action %q", d.Scope, d.Action))
		}
		seen[d.Scope][d.Action] = true
		for _, k := range d.Keys {
			if prev, ok := keyOwner[d.Scope][k]; ok {
				errs = append(errs, fmt.Errorf("%s: key %q bound to both %q and %q", d.Scope, k, prev, d.Action))
			}
			keyOwner[d.Scope][k] = d.Action
		}
		if d.Scope == Global && d.Action == ActQuit && !slices.Contains(d.Keys, "ctrl+c") {
			errs = append(errs, fmt.Errorf("global: quit must keep ctrl+c"))
		}
	}

	reserved := func(s Scope) map[string]string { return keyOwner[s] }
	for k, a := range reserved(Instance) {
		if g, ok := reserved(Global)[k]; ok {
			errs = append(errs, fmt.Errorf("key %q is both global %q and instance %q", k, g, a))
		}
	}
	for _, si := range Scopes {
		if si.Kind != KindView && si.Kind != KindComponent {
			continue
		}
		for k, a := range keyOwner[si.Name] {
			if g, ok := reserved(Global)[k]; ok {
				errs = append(errs, fmt.Errorf("%s: %q uses key %q reserved by global %q", si.Name, a, k, g))
			}
			if si.ShowsInstances {
				if g, ok := reserved(Instance)[k]; ok {
					errs = append(errs, fmt.Errorf("%s: %q uses key %q reserved by instance %q", si.Name, a, k, g))
				}
			}
		}
	}
	slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	return errs
}
