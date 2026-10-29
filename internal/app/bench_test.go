package app

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/clock"
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
		for view := 1; view <= 6; view++ {
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
func largeModel(tb testing.TB) tea.Model {
	reg, _ := theme.LoadRegistry(themes.FS, "")
	th, _ := reg.Resolve()
	var model tea.Model = New(Options{Themes: reg, Theme: th, Keys: keys.MustDefault(), Now: func() time.Time { return fixedNow }})
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model, _ = model.Update(ChangesMsg{Changes: largeChanges(tb)})
	model, _ = model.Update(uitest.Key("3"))
	return model
}

func largeChanges(tb testing.TB) []store.Change {
	w := fake.NewWorld(clock.NewFake(fixedNow))
	fake.Large(w, 500)
	ctx := context.Background()
	var out []store.Change
	for _, e := range w.Engines() {
		info, _ := e.Info(ctx)
		list, err := e.ListInstances(ctx, engine.ListOptions{})
		if err != nil {
			tb.Fatal(err)
		}
		out = append(out, store.Synced{Engine: e.ID(), Info: info, Instances: list},
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
