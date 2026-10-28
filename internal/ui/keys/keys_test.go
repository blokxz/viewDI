package keys

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestDefaultMapIsValid(t *testing.T) {
	if errs := Validate(Defaults()); len(errs) > 0 {
		t.Fatalf("built-in key map violates the scoping rules: %v", errs)
	}
	m := MustDefault()
	if len(m.Bindings(Global)) == 0 || len(m.Bindings(Instance)) != 5 {
		t.Fatalf("unexpected binding counts")
	}
}

func TestValidateDetectsConflicts(t *testing.T) {
	tests := []struct {
		name string
		defs []Def
		want string
	}{
		{"view binds global key", []Def{{Global, ActQuit, []string{"q", "ctrl+c"}, ""}, {Table, "x", []string{"q"}, ""}}, "reserved by global"},
		{"instance view binds instance key", []Def{{Instance, ActStart, []string{"s"}, ""}, {Tree, "y", []string{"s"}, ""}}, "reserved by instance"},
		{"global and instance overlap", []Def{{Global, ActHelp, []string{"s"}, ""}, {Instance, ActStart, []string{"s"}, ""}}, "both global"},
		{"duplicate key in scope", []Def{{Logs, "a", []string{"p"}, ""}, {Logs, "b", []string{"p"}, ""}}, "bound to both"},
		{"duplicate action", []Def{{Logs, "a", []string{"p"}, ""}, {Logs, "a", []string{"w"}, ""}}, "duplicate action"},
		{"unknown scope", []Def{{"view.nope", "a", []string{"p"}, ""}}, "unknown scope"},
		{"quit without ctrl+c", []Def{{Global, ActQuit, []string{"q"}, ""}}, "ctrl+c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := Validate(tt.defs)
			if len(errs) == 0 || !strings.Contains(errs[0].Error(), tt.want) {
				t.Fatalf("errs = %v, want %q", errs, tt.want)
			}
		})
	}
}

func TestNonInstanceViewMayReuseInstanceKeys(t *testing.T) {
	defs := []Def{{Instance, ActDelete, []string{"d"}, ""}, {Updates, "detail", []string{"d"}, ""}}
	if errs := Validate(defs); len(errs) != 0 {
		t.Fatalf("Updates does not show instances; got %v", errs)
	}
}

func TestModalScopesAreExempt(t *testing.T) {
	defs := []Def{{Global, ActQuit, []string{"q", "ctrl+c"}, ""}, {Help, ActClose, []string{"q"}, ""}}
	if errs := Validate(defs); len(errs) != 0 {
		t.Fatalf("modals capture all keys; got %v", errs)
	}
}

func press(s string) tea.KeyPressMsg {
	switch s {
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func TestMatchingRealKeyMessages(t *testing.T) {
	m := MustDefault()
	tests := []struct {
		key    string
		scope  Scope
		action string
	}{
		{"q", Global, ActQuit},
		{"ctrl+c", Global, ActQuit},
		{"T", Global, ActTheme},
		{"tab", Global, ActNextPane},
		{"shift+tab", Global, ActPrevPane},
		{"F", Instance, ActPause},
		{"space", Table, "mark"},
		{"4", Global, ActViewTree},
	}
	for _, tt := range tests {
		if got := m.Action(press(tt.key), tt.scope); got != tt.action {
			t.Errorf("%q in %s = %q, want %q", tt.key, tt.scope, got, tt.action)
		}
	}
	if m.Action(press("t"), Global) != "" {
		t.Error("lowercase t must not open the theme picker")
	}
}

func TestParseAndApplyOverrides(t *testing.T) {
	doc := `
[global]
theme = "ctrl+t"
quit = ["Q"]          # rejected: must keep ctrl+c
[instance]
pause = "p"           # rejected: p is used by the Logs tab
[view.table]
sort = ["o", "ctrl+o"]
mark = "s"            # rejected: s is an instance key
nonsense = "z"        # unknown action
[global.oops]
x = 1                 # invalid value type
toplevel = "k"        # belongs to [global.oops]: unknown scope
`
	ovs, warns := ParseOverrides("keys.toml", []byte(doc))
	if len(warns) != 1 || !strings.Contains(warns[0].Msg, "global.oops.x") {
		t.Fatalf("parse warnings = %v", warns)
	}
	m, warns := Apply(Defaults(), ovs)
	var msgs []string
	for _, w := range warns {
		msgs = append(msgs, w.Msg)
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{"global.quit rejected", "instance.pause rejected", "view.table.mark rejected", "unknown action view.table.nonsense", "unknown scope [global.oops]"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning %q in:\n%s", want, joined)
		}
	}
	if got := m.Binding(Global, ActTheme).Keys(); len(got) != 1 || got[0] != "ctrl+t" {
		t.Errorf("theme keys = %v", got)
	}
	if got := m.Binding(Instance, ActPause).Keys(); got[0] != "F" {
		t.Errorf("rejected override must keep the default, got %v", got)
	}
	if got := m.Binding(Table, "sort").Keys(); len(got) != 2 {
		t.Errorf("sort keys = %v", got)
	}
}

func TestUnbindViaEmptyList(t *testing.T) {
	ovs, warns := ParseOverrides("k", []byte("[global]\nimport = []\n"))
	if len(warns) != 0 {
		t.Fatal(warns)
	}
	m, warns := Apply(Defaults(), ovs)
	if len(warns) != 0 || m.Binding(Global, ActImport).Enabled() {
		t.Fatalf("import should be disabled; warns=%v", warns)
	}
}

func TestInvalidTOML(t *testing.T) {
	if _, warns := ParseOverrides("k", []byte("[global")); len(warns) != 1 {
		t.Fatalf("warns = %v", warns)
	}
}

func FuzzParseOverrides(f *testing.F) {
	f.Add([]byte("[global]\ntheme = \"ctrl+t\"\n[view.table]\nsort=[\"o\"]\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		ovs, _ := ParseOverrides("fuzz", data)
		m, _ := Apply(Defaults(), ovs)
		if errs := Validate(m.Defs()); len(errs) != 0 {
			t.Fatalf("overrides produced an invalid map: %v", errs)
		}
	})
}
