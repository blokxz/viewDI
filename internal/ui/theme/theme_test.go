package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/blokxz/viewDI/themes"
)

// Built-in theme IDs documented in docs/01-research.md §3.3.
var builtinIDs = []string{
	"catppuccin-latte", "catppuccin-mocha", "dracula", "everforest-dark", "everforest-light",
	"gruvbox", "monokai", "nord", "tokyo-night",
}

func TestBuiltinThemesAreCompleteAndExplicit(t *testing.T) {
	r, warns := LoadRegistry(themes.FS, "")
	if len(warns) != 0 {
		t.Fatalf("built-in themes produced warnings: %v", warns)
	}
	if got := len(r.Themes()); got != len(builtinIDs) {
		t.Fatalf("got %d themes, want %d", got, len(builtinIDs))
	}
	for _, id := range builtinIDs {
		th, ok := r.Get(id)
		if !ok {
			t.Errorf("missing built-in theme %q", id)
			continue
		}
		// Built-ins must set every token explicitly rather than rely on fallbacks.
		data, _ := themes.FS.ReadFile(id + ".toml")
		for _, tok := range Tokens() {
			if th.Color(tok) == nil {
				t.Errorf("%s: token %s unresolved", id, tok)
			}
			if !strings.Contains(string(data), "\n"+tok.String()+" = ") {
				t.Errorf("%s: token %s not set explicitly", id, tok)
			}
		}
	}
	if th, _ := r.Get("catppuccin-latte"); th.Dark {
		t.Error("catppuccin-latte must be a light theme")
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantErr string
		check   func(t *testing.T, th *Theme)
	}{
		{
			name: "minimal theme uses fallbacks",
			doc:  "[tokens]\nbg=\"#000\"\nfg=\"#fff\"\naccent=\"#123456\"\nok=\"#0f0\"\nwarn=\"#ff0\"\nerr=\"#f00\"\n",
			check: func(t *testing.T, th *Theme) {
				if th.Hex(BorderFocus) != "#123456" || th.Hex(ChartDisk) != "#ffff00" || th.Hex(BgAlt) != "#000000" {
					t.Errorf("fallbacks: focus=%s disk=%s bg_alt=%s", th.Hex(BorderFocus), th.Hex(ChartDisk), th.Hex(BgAlt))
				}
				if th.Name != "x" || !th.Dark {
					t.Errorf("name=%q dark=%v", th.Name, th.Dark)
				}
			},
		},
		{
			name: "palette references",
			doc:  "name=\"P\"\ndark=false\n[palette]\ng=\"#a7c080\"\n[tokens]\nbg=\"#fff\"\nfg=\"#000\"\naccent=\"g\"\nok=\"g\"\nwarn=\"g\"\nerr=\"g\"\n",
			check: func(t *testing.T, th *Theme) {
				if th.Hex(Accent) != "#a7c080" || th.Dark || th.Name != "P" {
					t.Errorf("got accent=%s dark=%v name=%q", th.Hex(Accent), th.Dark, th.Name)
				}
			},
		},
		{name: "missing required", doc: "[tokens]\nbg=\"#000\"\n", wantErr: `missing required token "fg"`},
		{name: "unknown token", doc: "[tokens]\nbg=\"#000\"\nfg=\"#fff\"\naccent=\"#fff\"\nok=\"#fff\"\nwarn=\"#fff\"\nerr=\"#fff\"\nshiny=\"#fff\"\n", wantErr: `unknown token "shiny"`},
		{name: "bad hex", doc: "[tokens]\nbg=\"#00000g\"\n", wantErr: "invalid color"},
		{name: "unknown palette name", doc: "[tokens]\nbg=\"nope\"\n", wantErr: "neither a hex color"},
		{name: "bad palette", doc: "[palette]\nx=\"red\"\n", wantErr: "palette.x"},
		{name: "unknown top-level key", doc: "colour=\"x\"\n", wantErr: "colour"},
		{name: "not toml", doc: "{{", wantErr: "theme x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			th, err := Parse("x", []byte(tt.doc))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, th)
		})
	}
}

func TestFallbackOrder(t *testing.T) {
	resolved := map[Token]bool{}
	for _, tok := range required {
		resolved[tok] = true
	}
	for _, fb := range fallbacks {
		if !resolved[fb.from] {
			t.Errorf("%s falls back to %s which is not resolved yet", fb.tok, fb.from)
		}
		resolved[fb.tok] = true
	}
	for _, tok := range Tokens() {
		if !resolved[tok] {
			t.Errorf("token %s has neither a requirement nor a fallback", tok)
		}
	}
}

func TestUserThemesOverrideAndInvalidAreSkipped(t *testing.T) {
	builtin := fstest.MapFS{
		"base.toml": {Data: []byte("name=\"Base\"\n[tokens]\nbg=\"#000\"\nfg=\"#fff\"\naccent=\"#111\"\nok=\"#0f0\"\nwarn=\"#ff0\"\nerr=\"#f00\"\n")},
	}
	dir := t.TempDir()
	write := func(name, data string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("base.toml", "name=\"Mine\"\n[tokens]\nbg=\"#000\"\nfg=\"#fff\"\naccent=\"#222\"\nok=\"#0f0\"\nwarn=\"#ff0\"\nerr=\"#f00\"\n")
	write("broken.toml", "[tokens]\nbg=1\n")
	write("notes.txt", "ignored")

	r, warns := LoadRegistry(builtin, dir)
	if len(warns) != 1 || !strings.Contains(warns[0].String(), "broken.toml") {
		t.Fatalf("warnings = %v", warns)
	}
	th, ok := r.Get("base")
	if !ok || th.Name != "Mine" || th.Hex(Accent) != "#222222" {
		t.Fatalf("user theme did not override: %+v", th)
	}
	if _, ok := r.Get("broken"); ok {
		t.Error("broken theme must not be registered")
	}
}

func TestResolvePrecedence(t *testing.T) {
	r, _ := LoadRegistry(themes.FS, "")
	th, warns := r.Resolve("", "nord", "dracula")
	if th.ID != "nord" || len(warns) != 0 {
		t.Fatalf("got %s %v", th.ID, warns)
	}
	th, warns = r.Resolve("nope", "dracula")
	if th.ID != "dracula" || len(warns) != 1 {
		t.Fatalf("got %s %v", th.ID, warns)
	}
	th, _ = r.Resolve("nope")
	if th.ID != "everforest-dark" {
		t.Fatalf("default = %s", th.ID)
	}
	empty, _ := LoadRegistry(fstest.MapFS{}, "")
	if th, _ := empty.Resolve(); th.ID != "fallback" {
		t.Fatalf("empty registry resolved %s", th.ID)
	}
}

func TestNewStylesBuildsForEveryTheme(t *testing.T) {
	r, _ := LoadRegistry(themes.FS, "")
	for i, th := range r.Themes() {
		s := NewStyles(th, uint64(i))
		if s.Version != uint64(i) || s.Theme != th {
			t.Fatalf("styles not bound to theme %s", th.ID)
		}
		if s.Toast(Level(99)).String() != s.Toast(LevelInfo).String() {
			t.Error("out-of-range level must clamp to info")
		}
	}
}

func FuzzParse(f *testing.F) {
	data, _ := themes.FS.ReadFile("nord.toml")
	f.Add(data)
	f.Add([]byte("[tokens]\nbg=\"#000\""))
	f.Fuzz(func(t *testing.T, data []byte) {
		th, err := Parse("fuzz", data)
		if err != nil {
			return
		}
		for _, tok := range Tokens() {
			if th.Color(tok) == nil {
				t.Fatalf("parsed theme has unresolved token %s", tok)
			}
		}
	})
}
