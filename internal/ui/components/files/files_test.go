package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
)

func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"b/backup.tar.gz", "a/x.txt", "image.tar", "notes.txt", ".hidden.tar", "evil\x1b[2J.tar"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "dangling.tar")); err != nil {
		t.Fatal(err)
	}
	return root
}

// run applies cmd's messages (listings) to the picker and returns the rest.
func run(m *Model, cmd tea.Cmd) []tea.Msg {
	var rest []tea.Msg
	for _, msg := range uitest.Collect(cmd) {
		if l, ok := msg.(ListedMsg); ok {
			m.Update(l)
			continue
		}
		rest = append(rest, msg)
	}
	return rest
}

func press(m *Model, k string) []tea.Msg {
	_, cmd := m.Update(uitest.Key(k))
	return run(m, cmd)
}

func names(m *Model) []string {
	out := make([]string, len(m.shown))
	for i, e := range m.shown {
		out[i] = e.Name
	}
	return out
}

func TestListingFiltersAndNavigates(t *testing.T) {
	root := tree(t)
	m, cmd := New(keys.MustDefault(), "img", "Choose", root, root, []string{".tar", ".tar.gz"})
	run(m, cmd)
	if got := strings.Join(names(m), " "); got != ".. a b evil\x1b[2J.tar image.tar" {
		t.Fatalf("shown %q", got)
	}
	press(m, "a")
	if got := strings.Join(names(m), " "); got != ".. a b evil\x1b[2J.tar image.tar notes.txt" {
		t.Fatalf("all files %q", got)
	}
	press(m, ".")
	if names(m)[3] != ".hidden.tar" {
		t.Fatalf("hidden %q", names(m))
	}
	press(m, "down")
	press(m, "down") // b
	press(m, "enter")
	if m.Dir() != filepath.Join(root, "b") || strings.Join(names(m), " ") != ".. backup.tar.gz" {
		t.Fatalf("in %s: %q", m.Dir(), names(m))
	}
	press(m, "backspace")
	if m.Dir() != root || m.shown[m.cursor].Name != "b" {
		t.Fatalf("parent: %s, cursor on %q", m.Dir(), m.shown[m.cursor].Name)
	}
	press(m, "enter")
	press(m, "down") // backup.tar.gz
	msgs := press(m, "enter")
	want := ChosenMsg{Kind: "img", Path: filepath.Join(root, "b", "backup.tar.gz"), Size: 4}
	if len(msgs) != 2 || msgs[0] != (modal.CloseMsg{}) || msgs[1] != want {
		t.Fatalf("chosen %#v", msgs)
	}
}

func TestViewSanitisesNamesAndErrors(t *testing.T) {
	root := tree(t)
	m, cmd := New(keys.MustDefault(), "img", "Choose", root, "", []string{".tar"})
	run(m, cmd)
	s := theme.NewStyles(theme.Fallback(), 1)
	out := m.View(s, 100, 40)
	if strings.Contains(out, "\x1b[2J") || !strings.Contains(ansi.Strip(out), "image.tar") {
		t.Fatalf("view:\n%s", out)
	}
	m2, cmd := New(keys.MustDefault(), "img", "Choose", filepath.Join(root, "nope"), "", nil)
	run(m2, cmd)
	if out := ansi.Strip(m2.View(s, 100, 40)); !strings.Contains(out, "no such file") {
		t.Fatalf("error view:\n%s", out)
	}
	if msgs := press(m2, "esc"); len(msgs) != 1 || msgs[0] != (modal.CloseMsg{}) {
		t.Fatalf("esc %#v", msgs)
	}
}

func TestStaleListingIgnored(t *testing.T) {
	root := tree(t)
	m, first := New(keys.MustDefault(), "img", "Choose", root, root, nil)
	_, second := m.Update(uitest.Key("~")) // a second listing before the first returns
	run(m, second)
	before := names(m)
	run(m, first)
	if strings.Join(names(m), " ") != strings.Join(before, " ") || m.Dir() != root {
		t.Fatalf("stale listing applied: %q", names(m))
	}
}
