package form

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
)

func sample() *Model {
	return New(keys.MustDefault(), "docker", "Create a Docker container", "on docker", "Create", []Field{
		{ID: "image", Label: "Image", Required: true, Suggestions: []string{"nginx:1.27", "postgres:17", "redis:7"}, Searchable: true},
		{ID: "pull", Label: "Pull", Kind: Check, Hint: "pull first"},
		{ID: "name", Label: "Name", Validate: func(s string) error {
			if strings.Contains(s, " ") {
				return errors.New("no spaces")
			}
			return nil
		}},
		{ID: "restart", Label: "Restart", Kind: Select, Options: []string{"no", "always"}},
		{ID: "env", Label: "Env", MaskSecrets: true},
	})
}

func press(m *Model, ks ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range ks {
		_, cmd = m.Update(uitest.Key(k))
	}
	return cmd
}

func typeText(m *Model, s string) {
	for _, r := range s {
		k := string(r)
		if r == ' ' {
			k = "space"
		}
		m.Update(uitest.Key(k))
	}
}

func TestSubmitValidates(t *testing.T) {
	m := sample()
	if cmd := press(m, "enter"); cmd != nil {
		t.Fatal("submitted without the required image")
	}
	if m.Error("image") != "required" || m.Focused() != "image" {
		t.Fatalf("error %q focus %q", m.Error("image"), m.Focused())
	}
	typeText(m, "nginx:1.27")
	press(m, "tab", "space", "tab") // tick pull, go to name
	typeText(m, "my web")
	press(m, "tab") // leaving validates
	if m.Error("name") != "no spaces" {
		t.Fatalf("name error %q", m.Error("name"))
	}
	press(m, "shift+tab", "backspace", "backspace", "backspace", "backspace")
	typeText(m, "-web")
	press(m, "tab", "right", "tab")
	typeText(m, "TOKEN=abc")
	msgs := uitest.Collect(press(m, "enter"))
	if len(msgs) != 2 || msgs[0] != (modal.CloseMsg{}) {
		t.Fatalf("msgs %#v", msgs)
	}
	got := msgs[1].(SubmittedMsg)
	want := Values{"image": "nginx:1.27", "pull": "true", "name": "my-web", "restart": "always", "env": "TOKEN=abc"}
	for k, v := range want {
		if got.Values[k] != v {
			t.Errorf("%s = %q, want %q", k, got.Values[k], v)
		}
	}
	if got.Kind != "docker" || !got.Values.Bool("pull") {
		t.Errorf("submitted %+v", got)
	}
}

func TestEditing(t *testing.T) {
	m := sample()
	typeText(m, "ngin:1")
	press(m, "left", "left")
	typeText(m, "x")
	if v := m.Value("image"); v != "nginx:1" {
		t.Fatalf("insert at cursor: %q", v)
	}
	press(m, "ctrl+e", "ctrl+w")
	if v := m.Value("image"); v != "" {
		t.Fatalf("ctrl+w: %q", v)
	}
	m.Update(tea.PasteMsg{Content: "red\x1b[31mis\n:7"})
	if v := m.Value("image"); v != "redis:7" {
		t.Fatalf("paste: %q", v)
	}
	press(m, "ctrl+a", "delete", "ctrl+k")
	if v := m.Value("image"); v != "" {
		t.Fatalf("ctrl+k: %q", v)
	}
}

func TestSuggestions(t *testing.T) {
	m := sample()
	typeText(m, "s")                       // matches postgres:17, redis:7
	press(m, "ctrl+n", "ctrl+n", "ctrl+n") // wraps to the first
	if v := m.Value("image"); v != "postgres:17" {
		t.Fatalf("cycled to %q", v)
	}
	press(m, "ctrl+p")
	if v := m.Value("image"); v != "redis:7" {
		t.Fatalf("back to %q", v)
	}
	// Remote results come first; the query is what was typed.
	msgs := uitest.Collect(press(m, "ctrl+f"))
	if len(msgs) != 1 || msgs[0] != (SearchMsg{Kind: "docker", Field: "image", Query: "s"}) {
		t.Fatalf("search %#v", msgs)
	}
	m.Update(SuggestionsMsg{Kind: "docker", Field: "image", Remote: true, Items: []string{"bitnami/redis", "\x1b[2Jevil"}, Note: "2 results"})
	press(m, "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace")
	typeText(m, "redis")
	press(m, "ctrl+n")
	if v := m.Value("image"); v != "bitnami/redis" {
		t.Fatalf("remote first: %q", v)
	}
	// Another form's suggestions are ignored.
	m.Update(SuggestionsMsg{Kind: "incus", Field: "image", Items: []string{"x"}})
	if f := m.field("image"); len(f.Suggestions) != 3 {
		t.Fatalf("suggestions %v", f.Suggestions)
	}
	if f := m.field("image"); f.remote[1] != "evil" {
		t.Fatalf("remote not sanitised: %q", f.remote)
	}
}

func TestCancel(t *testing.T) {
	m := sample()
	if msgs := uitest.Collect(press(m, "esc")); len(msgs) != 1 || msgs[0] != (modal.CloseMsg{}) {
		t.Fatalf("esc %#v", msgs)
	}
	// enter on the Cancel button closes without submitting.
	press(m, "shift+tab", "shift+tab")
	if msgs := uitest.Collect(press(m, "enter")); len(msgs) != 1 || msgs[0] != (modal.CloseMsg{}) {
		t.Fatalf("cancel button %#v", msgs)
	}
}

func TestViewMasksSecretsAndScrolls(t *testing.T) {
	m := sample()
	press(m, "tab", "tab", "tab", "tab")
	typeText(m, "A=1, API_TOKEN=hunter2")
	press(m, "tab")
	s := theme.NewStyles(theme.Fallback(), 1)
	out := ansi.Strip(m.View(s, 100, 40))
	if strings.Contains(out, "hunter2") || !strings.Contains(out, "API_TOKEN=••••••") || !strings.Contains(out, "A=1") {
		t.Fatalf("not masked:\n%s", out)
	}
	press(m, "shift+tab")
	if out := ansi.Strip(m.View(s, 100, 40)); !strings.Contains(out, "hunter2") {
		t.Fatalf("focused field masked:\n%s", out)
	}
	small := ansi.Strip(m.View(s, 60, 14))
	if n := strings.Count(small, "\n"); n > 14 {
		t.Fatalf("%d lines at height 14:\n%s", n, small)
	}
	for _, line := range strings.Split(small, "\n") {
		if w := ansi.StringWidth(line); w > 60 {
			t.Fatalf("line wider than the screen (%d): %q", w, line)
		}
	}
}
