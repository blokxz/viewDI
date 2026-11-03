package app

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	machinesview "github.com/blokxz/viewDI/internal/ui/views/machines"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
	"github.com/blokxz/viewDI/themes"
)

// BenchmarkRender measures a full frame per view; budget p99 < 8 ms at
// 120×40 and < 16 ms at 300×80 (docs/05-quality-plan.md §4.1).
func BenchmarkRender(b *testing.B) {
	reg, _ := theme.LoadRegistry(themes.FS, "")
	th, _ := reg.Resolve()
	for _, sz := range [][2]int{{120, 40}, {300, 80}} {
		for view := 1; view <= 4; view++ {
			for _, overlay := range []string{"", "?"} {
				name := fmt.Sprintf("%dx%d/view%d", sz[0], sz[1], view)
				if overlay != "" {
					name += "/help"
				}
				b.Run(name, func(b *testing.B) {
					var model tea.Model = New(Options{Themes: reg, Theme: th, Keys: keys.MustDefault()})
					model, _ = model.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
					model, _ = model.Update(uitest.Key(fmt.Sprint(view)))
					if overlay != "" {
						var cmd tea.Cmd
						model, cmd = model.Update(uitest.Key(overlay))
						model, _ = model.Update(cmd())
					}
					b.ReportAllocs()
					for b.Loop() {
						_ = model.View()
					}
				})
			}
		}
	}
}

// largeModel is the app with the 500-instance "large" scenario loaded.
func largeModel(tb testing.TB) tea.Model { return largeModelOn(tb, 500, "2v") } // the flat list

// largeModelOn loads n instances and shows a view.
func largeModelOn(tb testing.TB, n int, view string, mutate ...func(*Options)) tea.Model {
	reg, _ := theme.LoadRegistry(themes.FS, "")
	th, _ := reg.Resolve()
	opts := Options{Themes: reg, Theme: th, Keys: keys.MustDefault(), Now: func() time.Time { return fixedNow }}
	for _, m := range mutate {
		m(&opts)
	}
	changes := largeChanges(tb, n)
	var model tea.Model = New(opts)
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model, _ = model.Update(ChangesMsg{Changes: changes})
	for _, k := range view { // one key per character
		model, _ = model.Update(uitest.Key(string(k)))
	}
	return model
}

func largeChanges(tb testing.TB, n ...int) []store.Change {
	size := 500
	if len(n) > 0 {
		size = n[0]
	}
	w := fake.NewWorld(clock.NewFake(fixedNow))
	fake.Large(w, size)
	ctx := context.Background()
	var out []store.Change
	for _, e := range w.Engines() {
		info, _ := e.Info(ctx)
		list, err := e.ListInstances(ctx, engine.ListOptions{})
		if err != nil {
			tb.Fatal(err)
		}
		nets, err := e.Networks(ctx, engine.ListOptions{})
		if err != nil {
			tb.Fatal(err)
		}
		out = append(out, store.Synced{Engine: e.ID(), Info: info, Instances: list, Networks: nets},
			store.StatusChanged{Engine: e.ID(), Health: store.Health{State: store.StateConnected}})
	}
	return out
}

// BenchmarkTableScroll500 is one key press plus one full frame with 500
// instances (Stage 1 acceptance: within the frame-time budget).
func BenchmarkTableScroll500(b *testing.B) {
	model := largeModel(b)
	down := uitest.Key("down")
	b.ReportAllocs()
	for b.Loop() {
		model, _ = model.Update(down)
		_ = model.View()
	}
}

// TestTableScroll500FrameBudget: p99 frame time while scrolling 500
// instances stays under 8 ms at 120×40 (docs/05-quality-plan.md §4.1).
func TestTableScroll500FrameBudget(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing budget is meaningless under the race detector")
	}
	model := largeModel(t)
	down := uitest.Key("down")
	var frames []time.Duration
	for range 300 {
		start := time.Now()
		model, _ = model.Update(down)
		_ = model.View()
		frames = append(frames, time.Since(start))
	}
	slices.Sort(frames)
	if p99 := frames[len(frames)*99/100]; p99 > 8*time.Millisecond {
		t.Fatalf("p99 frame %s, budget 8ms", p99)
	}
}

// BenchmarkTreeScroll1000 is one key press plus one full frame in the Tree
// view with 1 000 instances (Stage 5 acceptance).
func BenchmarkTreeScroll1000(b *testing.B) {
	model := largeModelOn(b, 1000, "2", func(o *Options) { o.State.Tree.Grouping = "network" })
	down := uitest.Key("down")
	b.ReportAllocs()
	for b.Loop() {
		model, _ = model.Update(down)
		_ = model.View()
	}
}

// TestTreeScroll1000FrameBudget: holding ↓ over 1 000 nodes keeps the p99
// frame under 8 ms at 120×40 (docs/05-quality-plan.md §4.1), with the
// inspector following the cursor.
func TestTreeScroll1000FrameBudget(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing budget is meaningless under the race detector")
	}
	model := largeModelOn(t, 1000, "2", func(o *Options) { o.State.Tree.Grouping = "network" })
	down := uitest.Key("down")
	var frames []time.Duration
	for range 1100 {
		start := time.Now()
		model, _ = model.Update(down)
		_ = model.View()
		frames = append(frames, time.Since(start))
	}
	if n := len(model.(Model).ActiveViewModel().(*machinesview.View).Tree().Rows()); n < 1000 {
		t.Fatalf("only %d nodes shown", n)
	}
	slices.Sort(frames)
	if p99 := frames[len(frames)*99/100]; p99 > 8*time.Millisecond {
		t.Fatalf("p99 frame %s, budget 8ms", p99)
	}
}

// largeLabs puts n machines of the large world into labs of 50, each a
// chain of links plus a few long ones, so lanes cross and stack.
func largeLabs(tb testing.TB, n int) config.Labs {
	var members []config.LabMember
	for _, c := range largeChanges(tb, n) {
		if s, ok := c.(store.Synced); ok {
			for _, in := range s.Instances {
				members = append(members, config.LabMember{Engine: string(in.Engine), Project: in.Project, Name: in.Name})
			}
		}
	}
	var ls config.Labs
	for i := 0; i+1 < len(members) && len(ls.Labs) < config.MaxLabs; i += 50 {
		id, err := ls.Add(fmt.Sprintf("lab-%02d", len(ls.Labs)+1))
		if err != nil {
			tb.Fatal(err)
		}
		group := members[i:min(i+50, len(members))]
		for k := 1; k < len(group); k++ {
			_, _ = ls.Link(id, group[k-1], group[k])
			if k%5 == 0 {
				_, _ = ls.Link(id, group[k-5], group[k])
			}
		}
	}
	return ls
}

// The lab grouping draws lanes on every frame: it must keep the tree's
// frame budget with 1 000 machines in labs and the animation on.
func TestTreeLabs1000FrameBudget(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing budget is meaningless under the race detector")
	}
	labs := largeLabs(t, 1000)
	model := largeModelOn(t, 1000, "2", func(o *Options) {
		o.Labs = labs
		o.State.Tree.Grouping = "lab"
		o.Animation = 125 * time.Millisecond
	})
	down := uitest.Key("down")
	var frames []time.Duration
	for range 1100 {
		start := time.Now()
		model, _ = model.Update(down)
		_ = model.View()
		frames = append(frames, time.Since(start))
	}
	if n := len(model.(Model).ActiveViewModel().(*machinesview.View).Tree().Rows()); n < 1000 {
		t.Fatalf("only %d nodes shown", n)
	}
	slices.Sort(frames)
	if p99 := frames[len(frames)*99/100]; p99 > 8*time.Millisecond {
		t.Fatalf("p99 frame %s, budget 8ms", p99)
	}
}

// frameBudget presses down n times on a model and fails when the p99 of
// key press plus frame goes over 8 ms (docs/05-quality-plan.md §4.1).
func frameBudget(t *testing.T, model tea.Model, n int) {
	t.Helper()
	down := uitest.Key("down")
	frames := make([]time.Duration, 0, n)
	for range n {
		start := time.Now()
		model, _ = model.Update(down)
		_ = model.View()
		frames = append(frames, time.Since(start))
	}
	slices.Sort(frames)
	if p99 := frames[len(frames)*99/100]; p99 > 8*time.Millisecond {
		t.Fatalf("p99 frame %s, budget 8ms", p99)
	}
}

// BenchmarkHomeScroll500 is one key press plus one full Home frame with 500
// machines (Stage 9a acceptance).
func BenchmarkHomeScroll500(b *testing.B) {
	model := largeModelOn(b, 500, "1")
	down := uitest.Key("down")
	b.ReportAllocs()
	for b.Loop() {
		model, _ = model.Update(down)
		_ = model.View()
	}
}

// The Home derives apps and health on every change and draws cards, rows or
// the mosaic: with 500 machines it keeps the frame budget.
func TestHomeFrameBudget500(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing budget is meaningless under the race detector")
	}
	for _, density := range []string{"cards", "rows", "mosaic"} {
		t.Run(density, func(t *testing.T) {
			frameBudget(t, largeModelOn(t, 500, "1", func(o *Options) { o.State.Home.Density = density }), 300)
		})
	}
}

// The Machines tree grouped by app (the default) with 500 machines.
func TestMachinesAppTreeFrameBudget500(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing budget is meaningless under the race detector")
	}
	frameBudget(t, largeModelOn(t, 500, "2"), 500)
}

// The Activity feed with 500 machines and a lot of daemon events.
func TestActivityFrameBudget500(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing budget is meaningless under the race detector")
	}
	model := largeModelOn(t, 500, "4")
	var evs []store.Change
	for i := range 100 {
		evs = append(evs, store.EventRecorded{Event: engine.Event{
			Engine: engine.Docker, Action: "die", InstanceID: fmt.Sprint("c", i), Name: fmt.Sprint("c", i), Time: fixedNow.Add(-time.Duration(i) * time.Second),
		}})
	}
	model, _ = model.Update(ChangesMsg{Changes: evs})
	frameBudget(t, model, 300)
}

// BenchmarkChangeWith500 is one store change (a daemon event) processed with
// 500 machines: the Home derives apps and needs again, every view gets the
// snapshot.
func BenchmarkChangeWith500(b *testing.B) {
	model := largeModelOn(b, 500, "1")
	ev := ChangesMsg{Changes: []store.Change{store.EventRecorded{Event: engine.Event{
		Engine: engine.Docker, Action: "die", InstanceID: "x", Name: "x", Time: fixedNow,
	}}}}
	b.ReportAllocs()
	for b.Loop() {
		model, _ = model.Update(ev)
	}
}
