package home

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/apps"
	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
	"github.com/blokxz/viewDI/internal/ui/views"
	"github.com/blokxz/viewDI/themes"
)

func manyApps(n int) []apps.App {
	out := make([]apps.App, n)
	for i := range out {
		name := fmt.Sprintf("app-%02d", i)
		out[i] = apps.App{
			ID: "compose:" + name, Name: name, Kind: apps.Compose, Health: apps.OK, Up: 2,
			Machines: []engine.Key{engine.MakeKey(engine.Docker, name+"-a"), engine.MakeKey(engine.Docker, name+"-b")},
			Names:    []string{name + "-a", name + "-b"}, Cells: []apps.Health{apps.OK, apps.OK},
		}
	}
	return out
}

func newView(t *testing.T, n int) *View {
	t.Helper()
	v := New(keys.MustDefault(), config.HomeState{})
	v.Update(DerivedMsg{Apps: manyApps(n)})
	return v
}

func render(v *View, w, h int) string {
	reg, _ := theme.LoadRegistry(themes.FS, "")
	th, _ := reg.Resolve()
	return ansi.Strip(v.Render(views.Context{Styles: theme.NewStyles(th, 1), Keys: keys.MustDefault(), Width: w, Height: h, Mode: layout.ModeFor(w, h), Focus: paneApps}))
}

func TestRenderIsExactlyTheSizeAskedFor(t *testing.T) {
	for _, n := range []int{0, 1, 4, 12, 80} {
		for _, sz := range [][2]int{{80, 24}, {120, 40}, {200, 60}} {
			v := newView(t, n)
			out := render(v, sz[0], sz[1])
			lines := strings.Split(out, "\n")
			if len(lines) != sz[1] {
				t.Fatalf("%d apps at %v: %d lines", n, sz, len(lines))
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w != sz[0] {
					t.Fatalf("%d apps at %v: line %d is %d wide", n, sz, i, w)
				}
			}
		}
	}
}

func TestDensityFollowsTheRoomAndTheNumberOfApps(t *testing.T) {
	v := newView(t, 3)
	render(v, 120, 40)
	if v.shown != DensityCards {
		t.Errorf("3 apps in 120×40: %s", v.shown)
	}
	v = newView(t, 20)
	render(v, 120, 40)
	if v.shown != DensityRows {
		t.Errorf("20 apps in 120×40: %s", v.shown)
	}
	v = newView(t, 80)
	render(v, 120, 40)
	if v.shown != DensityMosaic {
		t.Errorf("80 apps in 120×40: %s", v.shown)
	}
	v = newView(t, 3)
	v.state.Density = DensityRows
	render(v, 120, 40)
	if v.shown != DensityRows {
		t.Errorf("a chosen density wins: %s", v.shown)
	}
}

func TestOverflowIsOneSentenceAndTheGridScrolls(t *testing.T) {
	v := newView(t, 12)
	v.state.Density = DensityCards
	out := render(v, 120, 24)
	if !strings.Contains(out, "more apps, all healthy") {
		t.Fatalf("no overflow sentence:\n%s", out)
	}
	for range 11 {
		v.HandleKey(uitest.Key("down"), paneApps)
	}
	out = render(v, 120, 24)
	if !strings.Contains(out, "app-11") {
		t.Fatalf("the cursor must scroll into view:\n%s", out)
	}
}

func TestPinsComeFirstAndFilterHides(t *testing.T) {
	v := newView(t, 5)
	v.cursorApp = 3
	v.appID = "compose:app-03"
	_, cmd, _ := v.HandleKey(uitest.Key("p"), paneApps)
	if st, ok := cmd().(StateChangedMsg); !ok || len(st.State.Pinned) != 1 || st.State.Pinned[0] != "compose:app-03" {
		t.Fatalf("pin: %+v", cmd())
	}
	if got := v.display()[0].Name; got != "app-03" {
		t.Fatalf("first app %s", got)
	}
	if v.cursorApp != 0 {
		t.Fatalf("the cursor must follow the pinned app, at %d", v.cursorApp)
	}
	v.filter = "app-0"
	v.restore()
	if len(v.display()) != 5 {
		t.Fatalf("display %d", len(v.display()))
	}
	v.filter = "app-04"
	v.restore()
	if len(v.display()) != 1 {
		t.Fatalf("display %d", len(v.display()))
	}
}

func TestEnterDoesTheFirstActionOrOpensTheApp(t *testing.T) {
	v := newView(t, 3)
	v.Update(DerivedMsg{Apps: manyApps(3), Needs: []apps.Item{{
		ID: "down:docker/x", Subject: "x", Text: "stopped", Machine: "docker/x",
		Actions: []apps.Action{{ID: apps.ActStart, Label: "start it"}, {ID: apps.ActLogs, Label: "logs"}},
	}}})
	_, cmd, _ := v.HandleActionAt(keys.ActOpen, paneNeeds)
	if m, ok := cmd().(ActionMsg); !ok || m.Action != apps.ActStart {
		t.Fatalf("needs pane: %+v", cmd())
	}
	_, cmd, _ = v.HandleActionAt(keys.ActOpen, paneApps)
	if m, ok := cmd().(OpenAppMsg); !ok || m.App.Name != "app-00" {
		t.Fatalf("apps pane: %+v", cmd())
	}
	_, cmd, _ = v.HandleKey(uitest.Key("L"), paneNeeds)
	if m, ok := cmd().(ActionMsg); !ok || m.Action != apps.ActLogs {
		t.Fatalf("L: %+v", cmd())
	}
	if _, _, handled := v.HandleKey(uitest.Key("a"), paneNeeds); handled {
		t.Fatal("an action the item does not have must not be handled")
	}
}

func TestEmptyHomeInvitesToCreate(t *testing.T) {
	v := newView(t, 0)
	if out := render(v, 120, 40); !strings.Contains(out, "Nothing here yet") {
		t.Fatalf("empty home:\n%s", out)
	}
}
