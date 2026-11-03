package app

import (
	"context"
	"strings"
	"testing"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/ui/views/table"
)

func homeDriver(t *testing.T, jr *stubJobs, mutates ...func(*Options)) *driver {
	t.Helper()
	d := newDriver(t, append([]func(*Options){withEngines(nil), withJobs(jr)}, mutates...)...)
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	return d
}

func TestHomeNamesWhatNeedsYouAndDoesTheFirstActionOnEnter(t *testing.T) {
	jr := &stubJobs{}
	d := homeDriver(t, jr)
	s := d.screen()
	for _, want := range []string{"2 things need you", "NEEDS YOU", "old-backup", "stopped and was not asked to", "s start it", "web-db", "APPS"} {
		if !strings.Contains(s, want) {
			t.Fatalf("home lacks %q:\n%s", want, s)
		}
	}
	d.press("enter") // the first item is old-backup, whose first action is start
	if len(jr.reqs) != 1 || jr.reqs[0].Title() != "start old-backup" {
		t.Fatalf("enter submitted %v", jr.titles())
	}
}

func TestHomeActionKeysOnTheFocusedItem(t *testing.T) {
	jr := &stubJobs{}
	d := homeDriver(t, jr)
	d.press("down", "down", "r") // web-db: restart
	if len(jr.reqs) != 1 || jr.reqs[0].Title() != "restart web-db" {
		t.Fatalf("submitted %v", jr.titles())
	}
	d.press("down", "s") // the last item has no start action: nothing happens
	if len(jr.reqs) != 1 {
		t.Fatalf("s on an item without it submitted %v", jr.titles())
	}
	d.press("L") // logs of the focused item opens the Detail view
	if !strings.Contains(d.screen(), "web-db") || d.m.detail == nil {
		t.Fatalf("logs did not open the detail view:\n%s", d.screen())
	}
}

func TestHomeFineSilencesAStoppedMachine(t *testing.T) {
	d := homeDriver(t, &stubJobs{})
	if !strings.Contains(d.screen(), "old-backup") {
		t.Fatal("old-backup should need the user")
	}
	d.press("f")
	s := d.screen()
	if strings.Contains(s, "stopped and was not asked to") || !strings.Contains(s, "1 thing needs you") {
		t.Fatalf("fine did not silence it:\n%s", s)
	}
}

func TestHomeReadOnlyOffersNoMutatingAction(t *testing.T) {
	jr := &stubJobs{}
	d := homeDriver(t, jr, func(o *Options) { o.ReadOnly = true })
	s := d.screen()
	if strings.Contains(s, "s start it") || strings.Contains(s, "r restart") || !strings.Contains(s, "L logs") {
		t.Fatalf("read-only home:\n%s", s)
	}
	d.press("s", "r")
	if len(jr.reqs) != 0 {
		t.Fatalf("submitted %v", jr.titles())
	}
}

func TestHomeOpensAnAppInMachines(t *testing.T) {
	d := homeDriver(t, &stubJobs{})
	d.press("tab") // into the apps
	d.press("enter")
	if d.m.ActiveView() != 3 {
		t.Fatalf("view %d, want the Table", d.m.ActiveView())
	}
	tv := d.m.views[2].(*table.View)
	if rows, all := tv.Rows(), len(d.m.store.Snapshot().Instances); len(rows) == 0 || len(rows) >= all {
		t.Fatalf("the table shows %d rows", len(rows))
	}
	if !strings.Contains(d.screen(), "app ") {
		t.Fatalf("the title must name the app:\n%s", d.screen())
	}
	d.press("esc")
	if len(tv.Rows()) <= 6 {
		t.Fatalf("esc must lift the restriction, %d rows", len(tv.Rows()))
	}
}

func TestHomeDensityPinAndFullChartsPersist(t *testing.T) {
	d := homeDriver(t, &stubJobs{})
	d.press("tab", "d")
	if got := d.saved[len(d.saved)-1].Home.Density; got != "cards" {
		t.Fatalf("density %q", got)
	}
	d.press("d")
	if s := d.screen(); strings.Contains(s, "╭─") && strings.Contains(s, "│ ●") {
		t.Fatalf("rows density still draws cards:\n%s", s)
	}
	d.press("p")
	pinned := d.saved[len(d.saved)-1].Home.Pinned
	if len(pinned) != 1 {
		t.Fatalf("pinned %v", pinned)
	}
	d.press("p")
	if len(d.saved[len(d.saved)-1].Home.Pinned) != 0 {
		t.Fatal("p again must unpin")
	}
	d.press("u")
	if !strings.Contains(d.screen(), "Top consumers") {
		t.Fatalf("u must show the full charts:\n%s", d.screen())
	}
	d.press("u")
	if !strings.Contains(d.screen(), "NEEDS YOU") {
		t.Fatalf("u again must come back:\n%s", d.screen())
	}
}

func TestHomeFindsAnApp(t *testing.T) {
	d := homeDriver(t, &stubJobs{})
	d.press("tab", "/")
	d.typeText("web")
	d.press("enter")
	s := d.screen()
	if !strings.Contains(s, "find: web") || strings.Contains(s, "other machines") {
		t.Fatalf("filtered home:\n%s", s)
	}
	d.press("esc")
	if s := d.screen(); !strings.Contains(s, "other machines") {
		t.Fatalf("esc must clear the search:\n%s", s)
	}
}

// A machine that was running and stops on its own needs the user; the same
// stop after a viewDI job asked for it does not.
func TestHomeTellsASurpriseStopFromAStopYouAskedFor(t *testing.T) {
	stopped := func(w *fake.World) {
		in, _ := w.Docker.Instance("redis-cache")
		if err := w.Docker.Act(context.Background(), in.ID, engine.Stop, engine.ActionOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	surprise := func(asked bool) bool {
		d := homeDriver(t, &stubJobs{})
		if asked {
			for _, in := range d.m.store.Snapshot().Instances {
				if in.Name == "redis-cache" {
					d.m.askedUntil[in.Key()] = d.m.now.Add(askedFor)
				}
			}
		}
		d.send(ChangesMsg{Changes: homelab(t, stopped)})
		return strings.Contains(strings.Join(strings.Fields(d.screen()), " "), "redis-cache stopped and was not asked")
	}
	if !surprise(false) {
		t.Fatal("a machine that stopped on its own must be reported")
	}
	if surprise(true) {
		t.Fatal("a machine a job stopped must not be reported")
	}
}
