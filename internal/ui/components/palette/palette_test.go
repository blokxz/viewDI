package palette

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
	"github.com/blokxz/viewDI/themes"
)

var items = []Item{
	{ID: "view:1", Label: "Go to Home", Group: "Views", Key: "1"},
	{ID: "view:4", Label: "Go to Activity", Group: "Views", Key: "4"},
	{ID: "do:theme", Label: "Change the theme", Group: "Settings", Key: "T"},
	{ID: "m:web", Label: "web-api", Group: "Machines"},
	{ID: "m:web-db", Label: "web-db", Group: "Machines"},
	{ID: "a:restart:web", Label: "Restart web-api", Group: "Actions", Key: "R", Search: true},
	{ID: "a:logs:web", Label: "Read the logs of web-api", Group: "Actions", Search: true},
	{ID: "lib:pg", Label: "Run PostgreSQL 17", Group: "Library", Words: "database sql"},
}

func ids(it []Item) string {
	var out []string
	for _, i := range it {
		out = append(out, i.ID)
	}
	return strings.Join(out, " ")
}

func TestMatchRanking(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		recent      []string
		wantFirst   string
		wantAbsent  string
	}{
		{"prefix beats scattered", "web", nil, "m:web", "view:1"},
		{"word start", "rest", nil, "a:restart:web", ""},
		{"fuzzy subsequence", "rstweb", nil, "a:restart:web", ""},
		{"extra words match", "sql", nil, "lib:pg", ""},
		{"case insensitive", "THEME", nil, "do:theme", ""},
		{"actions only appear for a query", "", nil, "view:1", "a:restart:web"},
		{"recent first on an empty query", "", []string{"lib:pg"}, "lib:pg", ""},
		{"no match", "zzzz", nil, "", "m:web"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Match(tc.query, items, tc.recent)
			if tc.wantFirst == "" {
				if len(got) != 0 {
					t.Fatalf("got %s", ids(got))
				}
				return
			}
			if len(got) == 0 || got[0].ID != tc.wantFirst {
				t.Fatalf("got %s, want %s first", ids(got), tc.wantFirst)
			}
			if tc.wantAbsent != "" && strings.Contains(" "+ids(got)+" ", " "+tc.wantAbsent+" ") {
				t.Fatalf("%s should not be listed: %s", tc.wantAbsent, ids(got))
			}
		})
	}
}

func TestRecentNeverBeatsABetterMatch(t *testing.T) {
	got := Match("web-db", items, []string{"m:web"})
	if got[0].ID != "m:web-db" {
		t.Fatalf("got %s", ids(got))
	}
}

func TestResultsAreCapped(t *testing.T) {
	var many []Item
	for range 500 {
		many = append(many, Item{ID: "x", Label: "machine"})
	}
	if n := len(Match("mach", many, nil)); n != MaxResults {
		t.Fatalf("%d results", n)
	}
}

func FuzzMatch(f *testing.F) {
	for _, s := range []string{"", "web", "\x1b[2J", "ñandú", strings.Repeat("a", 300), "  ", "%s%v"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, q string) {
		res := Match(q, items, []string{"lib:pg"})
		if len(res) > MaxResults {
			t.Fatal("too many results")
		}
		for _, r := range res {
			if !utf8.ValidString(r.Label) {
				t.Fatal("bad label")
			}
		}
	})
}

func TestModalTypingAndSelecting(t *testing.T) {
	km := keys.MustDefault()
	m := New(km, items, nil)
	for _, k := range "restart" {
		m.Update(uitest.Key(string(k)))
	}
	if m.Query() != "restart" || len(m.Results()) == 0 || m.Results()[0].ID != "a:restart:web" {
		t.Fatalf("query %q results %s", m.Query(), ids(m.Results()))
	}
	m.Update(uitest.Key("backspace"))
	if m.Query() != "restar" {
		t.Fatalf("query %q", m.Query())
	}
	m.Update(uitest.Key("ctrl+u"))
	if m.Query() != "" || len(m.Results()) == 0 {
		t.Fatal("ctrl+u must clear")
	}
	_, cmd := m.Update(uitest.Key("enter"))
	var sel SelectedMsg
	for _, msg := range uitest.Collect(cmd) {
		if s, ok := msg.(SelectedMsg); ok {
			sel = s
		}
	}
	if sel.ID != "view:1" {
		t.Fatalf("selected %q", sel.ID)
	}
}

func TestModalViewShowsKeysAndFits(t *testing.T) {
	reg, _ := theme.LoadRegistry(themes.FS, "")
	th, _ := reg.Resolve()
	s := theme.NewStyles(th, 1)
	m := New(keys.MustDefault(), items, nil)
	m.Update(uitest.Key("w"))
	out := m.View(s, 100, 30)
	for _, want := range []string{"Command palette", "› w", "web-api", "Machines"} {
		if !strings.Contains(stripANSI(out), want) {
			t.Errorf("view lacks %q:\n%s", want, stripANSI(out))
		}
	}
	m.Update(uitest.Key("ctrl+u"))
	for _, k := range "restart" {
		m.Update(uitest.Key(string(k)))
	}
	if out := stripANSI(m.View(s, 100, 30)); !strings.Contains(out, "R") {
		t.Errorf("the restart result must show its key:\n%s", out)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			in = true
		case in && (r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'):
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}
