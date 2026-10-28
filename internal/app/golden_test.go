package app

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/ui/components/banner"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Golden files hold the exact frame (ANSI included) returned by Render, not
// the renderer's incremental byte stream, so they stay stable across Bubble
// Tea versions. Regenerate with: make golden-update.
func TestGolden(t *testing.T) {
	sizes := [][2]int{{80, 24}, {120, 40}}
	themeIDs := []string{"everforest-dark", "catppuccin-latte"} // dark + light coverage
	screens := []struct {
		name  string
		setup func(d *driver)
	}{
		{"admin", func(*driver) {}},
		{"tree-focus-inspector", func(d *driver) { d.press("4", "tab") }},
		{"help", func(d *driver) { d.press("3", "?") }},
		{"theme-picker", func(d *driver) { d.press("T") }},
		{"banner-readonly-mock", func(d *driver) {
			d.send(banner.ShowMsg{Level: theme.LevelWarn, Text: "Incus unreachable — reconnecting in 4s (ctrl+r to retry now)"})
		}},
	}
	for _, id := range themeIDs {
		for _, sz := range sizes {
			for _, sc := range screens {
				t.Run(fmt.Sprintf("%s/%dx%d/%s", id, sz[0], sz[1], sc.name), func(t *testing.T) {
					d := newDriver(t, func(o *Options) {
						o.Theme, _ = o.Themes.Get(id)
						o.State = config.State{}
						if sc.name == "banner-readonly-mock" {
							o.ReadOnly, o.Mock = true, "homelab"
						}
					})
					d.send(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
					sc.setup(d)
					golden.RequireEqual(t, d.m.Render())
				})
			}
		}
	}
	t.Run("too-small", func(t *testing.T) {
		d := newDriver(t, nil)
		d.send(tea.WindowSizeMsg{Width: 60, Height: 18})
		golden.RequireEqual(t, d.m.Render())
	})
}
