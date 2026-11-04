package hubbrowser

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
)

var (
	nginx   = Item{Ref: "nginx", Description: "Official build of Nginx.", Stars: 21416, Pulls: 13_544_827_346, Official: true}
	grafana = Item{Ref: "grafana/grafana", Description: "The open observability platform", Stars: 3000, Pulls: 4_000_000_000}
)

func open(t *testing.T, cfg Config) *Model {
	t.Helper()
	return New(keys.MustDefault(), cfg)
}

func press(m *Model, ks ...string) []tea.Msg {
	var out []tea.Msg
	for _, k := range ks {
		_, cmd := m.Update(uitest.Key(k))
		out = append(out, uitest.Collect(cmd)...)
	}
	return out
}

func typeText(m *Model, s string) {
	for _, r := range s {
		_, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func view(m *Model) string { return ansi.Strip(m.View(theme.NewStyles(theme.Fallback(), 1), 120, 40)) }

func TestSearchTabAsksOnlyWhenTheQueryChanged(t *testing.T) {
	m := open(t, Config{})
	if got := press(m, "enter"); len(got) != 0 {
		t.Fatalf("an empty query asked for %v", got)
	}
	typeText(m, "ngin")
	press(m, "backspace")
	typeText(m, "nx")
	if m.Query() != "nginx" {
		t.Fatalf("query %q", m.Query())
	}
	got := press(m, "enter")
	if len(got) != 1 || got[0] != (SearchMsg{Query: "nginx"}) {
		t.Fatalf("enter sent %v", got)
	}
	// While it loads, enter does not ask a second time.
	if got := press(m, "enter"); len(got) != 0 {
		t.Fatalf("a second enter sent %v", got)
	}
	_, _ = m.Update(ResultsMsg{Tab: TabSearch, Query: "nginx", Items: []Item{nginx, grafana}})
	if !strings.Contains(view(m), "› nginx") && !strings.Contains(view(m), "›nginx") {
		t.Errorf("results not shown:\n%s", view(m))
	}
	// Enter now chooses the highlighted image and closes the browser first.
	got = press(m, "down", "enter")
	if len(got) != 2 {
		t.Fatalf("choose sent %v", got)
	}
	if _, ok := got[0].(modal.CloseMsg); !ok || got[1] != (SelectedMsg{Ref: "grafana/grafana"}) {
		t.Fatalf("choose sent %v", got)
	}
	// Typing again makes enter search again.
	typeText(m, "x")
	if got := press(m, "enter"); len(got) != 1 || got[0] != (SearchMsg{Query: "nginxx"}) {
		t.Fatalf("a changed query sent %v", got)
	}
}

func TestAnAnswerToAnOlderSearchIsIgnored(t *testing.T) {
	m := open(t, Config{})
	typeText(m, "redis")
	press(m, "enter")
	typeText(m, "x")
	press(m, "enter") // now searching "redisx"
	_, _ = m.Update(ResultsMsg{Tab: TabSearch, Query: "redis", Items: []Item{nginx}})
	if len(m.Items()) != 0 {
		t.Fatalf("the answer to an older search replaced the list: %v", m.Items())
	}
	_, _ = m.Update(ResultsMsg{Tab: TabSearch, Query: "redisx", Items: []Item{grafana}})
	if len(m.Items()) != 1 || m.Items()[0].Ref != "grafana/grafana" {
		t.Fatalf("items %v", m.Items())
	}
}

func TestMostDownloadedLoadsOnceWhenOpened(t *testing.T) {
	m := open(t, Config{})
	got := press(m, "tab")
	if m.Tab() != TabTop || len(got) != 1 || got[0] != (LoadMsg{Tab: TabTop}) {
		t.Fatalf("tab %v sent %v", m.Tab(), got)
	}
	if !strings.Contains(view(m), "asking Docker Hub") {
		t.Errorf("no loading text:\n%s", view(m))
	}
	_, _ = m.Update(ResultsMsg{Tab: TabTop, Items: []Item{nginx, grafana}})
	out := view(m)
	for _, want := range []string{"Most downloaded", "nginx", "official", "★ 21.4K", "↓ 13.5B", "Official build of Nginx."} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	// Away and back: no second request.
	if got := press(m, "tab", "shift+tab"); len(got) != 0 || m.Tab() != TabTop {
		t.Fatalf("coming back asked again: %v", got)
	}
	if got := press(m, "ctrl+r"); len(got) != 1 || got[0] != (LoadMsg{Tab: TabTop}) {
		t.Fatalf("ctrl+r sent %v", got)
	}
}

func TestErrorsReplaceTheList(t *testing.T) {
	m := open(t, Config{})
	press(m, "tab")
	_, _ = m.Update(ResultsMsg{Tab: TabTop, Err: "docker hub: too many requests, try again in a minute"})
	if out := view(m); !strings.Contains(out, "too many requests") {
		t.Errorf("error not shown:\n%s", out)
	}
	if len(m.Items()) != 0 {
		t.Errorf("items %v", m.Items())
	}
}

func TestFavouritesToggleAndPersist(t *testing.T) {
	m := open(t, Config{Favourites: []string{"redis"}})
	typeText(m, "nginx")
	press(m, "enter")
	_, _ = m.Update(ResultsMsg{Tab: TabSearch, Query: "nginx", Items: []Item{nginx, grafana}})
	got := press(m, "ctrl+f")
	if len(got) != 1 || got[0].(FavouritesMsg).Refs[0] != "redis" || got[0].(FavouritesMsg).Refs[1] != "nginx" {
		t.Fatalf("ctrl+f sent %v", got)
	}
	if !strings.Contains(view(m), "♥") {
		t.Errorf("no marker:\n%s", view(m))
	}
	// The Favourites tab lists them, with what was seen about each.
	press(m, "tab", "tab")
	if m.Tab() != TabFavourites || len(m.Items()) != 2 {
		t.Fatalf("tab %v items %v", m.Tab(), m.Items())
	}
	if out := view(m); !strings.Contains(out, "Favourites 2") || !strings.Contains(out, "Official build of Nginx.") {
		t.Errorf("view:\n%s", out)
	}
	// Removing the highlighted one empties its row.
	got = press(m, "down", "ctrl+f")
	if refs := got[0].(FavouritesMsg).Refs; len(refs) != 1 || refs[0] != "redis" {
		t.Fatalf("after removing: %v", refs)
	}
	if want := []string{"redis"}; strings.Join(m.Favourites(), ",") != strings.Join(want, ",") {
		t.Errorf("favourites %v", m.Favourites())
	}
	// Choosing a favourite works offline.
	got = press(m, "enter")
	if len(got) != 2 || got[1] != (SelectedMsg{Ref: "redis"}) {
		t.Fatalf("choose sent %v", got)
	}
}

func TestFavouritesAreCapped(t *testing.T) {
	var many []string
	for i := range MaxFavourites + 10 {
		many = append(many, "img"+strings.Repeat("a", i%7)+string(rune('a'+i%26)))
	}
	m := open(t, Config{Favourites: many})
	if len(m.Favourites()) != MaxFavourites {
		t.Fatalf("%d favourites kept", len(m.Favourites()))
	}
	typeText(m, "x")
	press(m, "enter")
	_, _ = m.Update(ResultsMsg{Tab: TabSearch, Query: "x", Items: []Item{nginx}})
	press(m, "ctrl+f")
	if len(m.Favourites()) != MaxFavourites || !strings.Contains(view(m), "at most") {
		t.Errorf("a %dth favourite was accepted", MaxFavourites+1)
	}
}

func TestNotesReplaceTabsThatCannotWork(t *testing.T) {
	m := open(t, Config{SearchNote: "Docker is not available", TopNote: "network calls are off ([updates] enabled = false)"})
	typeText(m, "nginx")
	if got := press(m, "enter"); len(got) != 0 {
		t.Fatalf("a search was sent: %v", got)
	}
	if out := view(m); !strings.Contains(out, "Docker is not available") {
		t.Errorf("view:\n%s", out)
	}
	if got := press(m, "tab"); len(got) != 0 {
		t.Fatalf("a load was sent: %v", got)
	}
	if out := view(m); !strings.Contains(out, "network calls are off") {
		t.Errorf("view:\n%s", out)
	}
}

func TestQueryTextIsSafe(t *testing.T) {
	m := open(t, Config{})
	_, _ = m.Update(tea.PasteMsg{Content: "red\x1b[31mis\nup\x00" + strings.Repeat("z", 300)})
	q := m.Query()
	if strings.ContainsAny(q, "\x1b\n\x00") || len([]rune(q)) > MaxQuery {
		t.Errorf("query %q (%d runes)", q, len([]rune(q)))
	}
	press(m, "ctrl+u")
	if m.Query() != "" {
		t.Errorf("ctrl+u left %q", m.Query())
	}
	// Typing is for the Search tab only.
	press(m, "tab")
	typeText(m, "abc")
	if m.Query() != "" {
		t.Errorf("typing in another tab changed the query: %q", m.Query())
	}
}

func TestEscCloses(t *testing.T) {
	m := open(t, Config{})
	got := press(m, "esc")
	if len(got) != 1 {
		t.Fatalf("esc sent %v", got)
	}
	if _, ok := got[0].(modal.CloseMsg); !ok {
		t.Fatalf("esc sent %T", got[0])
	}
}

func TestCompact(t *testing.T) {
	for n, want := range map[uint64]string{0: "0", 999: "999", 1000: "1.0K", 21416: "21.4K", 3_400_000: "3.4M", 13_544_827_346: "13.5B"} {
		if got := Compact(n); got != want {
			t.Errorf("Compact(%d) = %s, want %s", n, got, want)
		}
	}
}
