package app

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

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
