package table

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
	"github.com/blokxz/viewDI/internal/ui/views"
)

var now = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func snapshot(ins ...engine.Instance) *store.Snapshot {
	s := store.New(engine.IDs...)
	byEngine := map[engine.ID][]engine.Instance{}
	for _, in := range ins {
		byEngine[in.Engine] = append(byEngine[in.Engine], in)
	}
	for id, list := range byEngine {
		s.Apply(store.Synced{Engine: id, Instances: list})
	}
	return s.Snapshot()
}

func inst(e engine.ID, name, image string, st engine.State, up time.Duration) engine.Instance {
	in := engine.Instance{Engine: e, ID: name, Name: name, Image: image, State: st}
	if st == engine.StateRunning {
		in.Started = now.Add(-up)
	}
	return in
}

func newView(ins ...engine.Instance) *View {
	v := New(keys.MustDefault(), config.TableState{})
	v.Update(views.DataMsg{Snapshot: snapshot(ins...)})
	v.Update(views.ResizeMsg{Width: 120, Height: 30})
	return v
}

func names(rows []engine.Instance) string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return strings.Join(out, ",")
}

func TestSortColumns(t *testing.T) {
	v := newView(
		inst(engine.Docker, "b", "nginx", engine.StateStopped, 0),
		inst(engine.Incus, "a", "debian", engine.StateRunning, time.Hour),
		inst(engine.Docker, "c", "alpine", engine.StateRunning, 2*time.Hour),
	)
	cases := []struct {
		col  Column
		want string
	}{
		{ColName, "a,b,c"}, {ColEngine, "b,c,a"}, {ColState, "a,c,b"}, {ColImage, "c,a,b"}, {ColUptime, "c,a,b"},
	}
	for _, c := range cases {
		sortRows(v.rows, c.col, false)
		if got := names(v.rows); got != c.want {
			t.Errorf("%s: %s, want %s", c.col.ID(), got, c.want)
		}
	}
	sortRows(v.rows, ColUptime, true)
	if got := names(v.rows); got != "b,a,c" {
		t.Errorf("reverse uptime: %s", got)
	}
}

func TestFuzzyMatch(t *testing.T) {
	in := engine.Instance{Name: "web-api", Image: "ghcr.io/me/api:1.4", Project: "lab", Engine: engine.Incus, State: engine.StateRunning}
	for q, want := range map[string]bool{
		"wpi": true, "web api": true, "api": true, "iwa": false, "ghcr": true, "lab": true,
		"incus": true, "running": true, "zzz": false, "": true,
	} {
		if got := match(q, in); got != want {
			t.Errorf("match(%q) = %v", q, got)
		}
	}
}

func TestUptimeFormat(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "—", 30 * time.Second: "30s", 45 * time.Minute: "45m", 5 * time.Hour: "5h", 50 * time.Hour: "2d",
	} {
		st := engine.StateRunning
		if d == 0 {
			st = engine.StateStopped
		}
		if got := views.Uptime(inst(engine.Docker, "x", "", st, d), now); got != want {
			t.Errorf("uptime(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestCursorScrollsAndClamps(t *testing.T) {
	var ins []engine.Instance
	for i := range 100 {
		ins = append(ins, inst(engine.Docker, "n"+string(rune('a'+i/26))+string(rune('a'+i%26)), "img", engine.StateRunning, time.Minute))
	}
	v := newView(ins...)
	for _, k := range []string{"pgdown", "pgdown", "down"} {
		v.HandleKey(uitest.Key(k), 0)
	}
	if v.cursor != 2*v.page+1 || v.offset > v.cursor || v.cursor >= v.offset+v.page {
		t.Fatalf("cursor %d offset %d page %d", v.cursor, v.offset, v.page)
	}
	v.HandleKey(uitest.Key("end"), 0)
	if v.cursor != 99 || v.offset != 100-v.page {
		t.Fatalf("end: cursor %d offset %d", v.cursor, v.offset)
	}
	v.HandleKey(uitest.Key("home"), 0)
	if v.cursor != 0 || v.offset != 0 {
		t.Fatalf("home: cursor %d offset %d", v.cursor, v.offset)
	}
}

func TestRenderIsExactSizeAndVirtualised(t *testing.T) {
	var ins []engine.Instance
	for i := range 500 {
		ins = append(ins, inst(engine.Docker, "svc-"+strings.Repeat("x", i%40), "image-with-a-long-reference:1.2.3", engine.StateRunning, time.Duration(i)*time.Minute))
	}
	v := newView(ins...)
	s := theme.NewStyles(theme.Fallback(), 1)
	for _, sz := range [][2]int{{80, 21}, {120, 37}, {300, 77}} {
		out := v.Render(views.Context{Styles: s, Width: sz[0], Height: sz[1], Mode: layout.ModeFor(sz[0], sz[1]+3), Now: now})
		lines := strings.Split(out, "\n")
		if len(lines) != sz[1] {
			t.Fatalf("%v: %d lines", sz, len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w != sz[0] {
				t.Fatalf("%v: line %d width %d", sz, i, w)
			}
		}
		if n := strings.Count(out, "svc-"); n > sz[1] {
			t.Fatalf("%v: rendered %d rows for %d lines: not virtualised", sz, n, sz[1])
		}
	}
}

func BenchmarkScroll500(b *testing.B) {
	var ins []engine.Instance
	for i := range 500 {
		ins = append(ins, inst(engine.Docker, "svc-"+strings.Repeat("y", i%30), "nginx:1.27", engine.StateRunning, time.Duration(i)*time.Minute))
	}
	v := newView(ins...)
	s := theme.NewStyles(theme.Fallback(), 1)
	ctx := views.Context{Styles: s, Width: 120, Height: 37, Mode: layout.Wide, Now: now}
	down := uitest.Key("down")
	b.ReportAllocs()
	for b.Loop() {
		v.HandleKey(down, 0)
		if v.cursor == 499 {
			v.HandleKey(uitest.Key("home"), 0)
		}
		_ = v.Render(ctx)
	}
}
