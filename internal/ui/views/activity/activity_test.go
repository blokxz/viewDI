package activity

import (
	"strings"
	"testing"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
	"github.com/blokxz/viewDI/internal/ui/views"
	jobsview "github.com/blokxz/viewDI/internal/ui/views/jobs"
	updatesview "github.com/blokxz/viewDI/internal/ui/views/updates"
	"github.com/blokxz/viewDI/themes"
)

func ctx(t *testing.T, w, h int) views.Context {
	t.Helper()
	reg, _ := theme.LoadRegistry(themes.FS, "")
	th, _ := reg.Resolve()
	return views.Context{Styles: theme.NewStyles(th, 1), Keys: keys.MustDefault(), Width: w, Height: h, Mode: layout.ModeFor(w, h), Now: now}
}

func newView(t *testing.T) *View {
	t.Helper()
	v := New(keys.MustDefault())
	v.Update(views.DataMsg{Snapshot: world(t)})
	v.Render(ctx(t, 120, 30)) // sets the clock and builds the feed
	return v
}

func selectID(t *testing.T, v *View, id string) {
	t.Helper()
	v.HandleKey(uitest.Key("home"), 0)
	for range len(v.shown) {
		if e, _ := v.Selected(); e.ID == id {
			return
		}
		v.HandleKey(uitest.Key("down"), 0)
	}
	t.Fatalf("no entry %q", id)
}

func TestRenderFillsTheSizeExactly(t *testing.T) {
	v := newView(t)
	for _, sz := range [][2]int{{80, 24}, {120, 40}, {60, 14}} {
		out := v.Render(ctx(t, sz[0], sz[1]))
		lines := strings.Split(out, "\n")
		if len(lines) != sz[1] {
			t.Fatalf("%v: %d lines", sz, len(lines))
		}
	}
	out := v.Render(ctx(t, 120, 30))
	for _, want := range []string{"Activity · 1 alert · 1 running · 1 update", "NOW", "TODAY", "EARLIER", "using 96% of its memory", "restart web", "outside viewDI"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestChipsFilterTheFeed(t *testing.T) {
	v := newView(t)
	count := func() int { return len(v.Entries()) }
	if count() != 5 {
		t.Fatalf("all: %d", count())
	}
	for _, tc := range []struct {
		chip Chip
		n    int
	}{{ChipAlerts, 1}, {ChipUpdates, 1}, {ChipJobs, 2}, {ChipHistory, 2}, {ChipAll, 5}} {
		v.HandleKey(uitest.Key("right"), 0)
		if v.Chip() != tc.chip || count() != tc.n {
			t.Fatalf("chip %v: %d entries, want %v %d", v.Chip(), count(), tc.chip, tc.n)
		}
	}
	v.HandleKey(uitest.Key("left"), 0)
	if v.Chip() != ChipHistory {
		t.Fatalf("left from all: %v", v.Chip())
	}
	out := v.Render(ctx(t, 100, 20))
	if !strings.Contains(out, "[History 2]") || strings.Contains(out, "using 96%") {
		t.Fatalf("history chip:\n%s", out)
	}
}

func TestEmptyFeedSaysSo(t *testing.T) {
	v := New(keys.MustDefault())
	v.Update(views.DataMsg{Snapshot: store.New(engine.Docker).Snapshot()})
	if out := v.Render(ctx(t, 100, 20)); !strings.Contains(out, "Nothing is happening") {
		t.Fatalf("empty:\n%s", out)
	}
}

func TestCancelOnlyAJobInFlight(t *testing.T) {
	v := newView(t)
	selectID(t, v, "job/2")
	if _, cmd, _ := v.HandleKey(uitest.Key("c"), 0); cmd != nil {
		t.Fatal("cancel offered for a finished job")
	}
	selectID(t, v, "job/1")
	_, cmd, _ := v.HandleKey(uitest.Key("c"), 0)
	if msgs := uitest.Collect(cmd); len(msgs) != 1 || msgs[0] != (jobsview.CancelMsg{ID: 1}) {
		t.Fatalf("cancel msgs %v", msgs)
	}
	selectID(t, v, "alert/mem/api")
	if _, cmd, _ := v.HandleKey(uitest.Key("c"), 0); cmd != nil {
		t.Fatal("cancel offered for an alert")
	}
}

func TestUpdateActions(t *testing.T) {
	v := newView(t)
	selectID(t, v, "update/engine/docker")
	for key, want := range map[string]string{"z": "snooze", "I": "ignore", "u": "upgrade"} {
		_, cmd, handled := v.HandleKey(uitest.Key(key), 0)
		msgs := uitest.Collect(cmd)
		if !handled || len(msgs) != 1 {
			t.Fatalf("%s: handled %v msgs %v", key, handled, msgs)
		}
		switch m := msgs[0].(type) {
		case updatesview.SnoozeMsg:
			if want != "snooze" || m.Key != "engine/docker" {
				t.Errorf("%s: %+v", key, m)
			}
		case updatesview.IgnoreMsg:
			if want != "ignore" || m.Version != "29.1.0" {
				t.Errorf("%s: %+v", key, m)
			}
		case updatesview.UpgradeMsg:
			if want != "upgrade" || m.Engine == nil || m.Engine.Available != "29.1.0" {
				t.Errorf("%s: %+v", key, m)
			}
		default:
			t.Errorf("%s: %T", key, m)
		}
	}
	// The image verbs do nothing on an engine row but still go to the app,
	// which explains; elsewhere they are ignored.
	selectID(t, v, "alert/mem/api")
	for _, key := range []string{"z", "I", "u", "p", "f", "R"} {
		if _, cmd, _ := v.HandleKey(uitest.Key(key), 0); cmd != nil {
			t.Errorf("%s offered on an alert", key)
		}
	}
}

func TestEnter(t *testing.T) {
	v := newView(t)
	selectID(t, v, "alert/mem/api")
	_, cmd, handled := v.HandleAction(keys.ActOpen)
	if msgs := uitest.Collect(cmd); !handled || len(msgs) != 1 || msgs[0] != (OpenMachineMsg{Key: "docker/api"}) {
		t.Fatalf("alert enter: handled %v msgs %v", handled, msgs)
	}
	selectID(t, v, "update/engine/docker")
	if _, cmd, handled := v.HandleAction(keys.ActOpen); !handled || cmd != nil {
		t.Fatalf("engine update without notes: handled %v cmd %v", handled, cmd != nil)
	}
	if _, _, handled := v.HandleAction(keys.ActFilter); handled {
		t.Fatal("filter handled")
	}
}

func TestCursorSurvivesNewData(t *testing.T) {
	v := newView(t)
	selectID(t, v, "job/2")
	v.Update(views.DataMsg{Snapshot: world(t)})
	if e, _ := v.Selected(); e.ID != "job/2" {
		t.Fatalf("cursor moved to %s", e.ID)
	}
}

func TestHintsFollowTheSelection(t *testing.T) {
	v := newView(t)
	km := keys.MustDefault()
	has := func(id string, want string) bool {
		selectID(t, v, id)
		for _, b := range v.Hints(km) {
			if b.Help().Desc == want {
				return true
			}
		}
		return false
	}
	if !has("job/1", "cancel job") || has("job/2", "cancel job") {
		t.Error("cancel hint")
	}
	if !has("update/engine/docker", "release notes") || !has("update/engine/docker", "upgrade the engine") {
		t.Error("engine update hints")
	}
}
