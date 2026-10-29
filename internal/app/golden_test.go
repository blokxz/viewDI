package app

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/store"
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
		{"table", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.press("3", "down", "down", "space")
		}},
		{"table-filter", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.press("3", "/", "w", "e", "b")
		}},
		{"table-degraded", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.send(ChangesMsg{Changes: []store.Change{store.StatusChanged{Engine: engine.Incus, Health: store.Health{
				State: store.StateDegraded, Since: fixedNow, RetryAt: fixedNow.Add(4 * time.Second), Err: "daemon not responding (timeout)",
			}}}})
			d.press("3", "o", "o", "O")
		}},
		{"table-unavailable", func(d *driver) {
			d.send(ChangesMsg{Changes: append(homelab(t, nil)[2:], store.StatusChanged{Engine: engine.Docker, Health: store.Health{
				State: store.StateUnavailable, RetryAt: fixedNow.Add(60 * time.Second), Err: "permission denied", Class: engine.ClassPermission,
				Hint: "Permission denied on /var/run/docker.sock. Join the docker group: sudo usermod -aG docker $USER && newgrp docker (or use rootless Docker).",
			}})})
			d.press("3")
		}},
		{"resources", func(d *driver) {
			d.send(ChangesMsg{Changes: append(homelab(t, nil), homelabMetrics(t, 90)...)})
			d.press("2")
		}},
		{"admin-live", func(d *driver) {
			d.send(ChangesMsg{Changes: append(homelab(t, nil), homelabMetrics(t, 90)...)})
			d.send(ClockTickMsg(fixedNow.Add(31 * time.Second)))
			d.press("1")
		}},
		{"table-metrics", func(d *driver) {
			d.send(ChangesMsg{Changes: append(homelab(t, nil), homelabMetrics(t, 5)...)})
			d.press("3")
		}},
		{"project-picker", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.press("3", "P")
		}},
		{"detail-overview", func(d *driver) { openDetailGolden(t, d, engine.Docker, "web-api", 0) }},
		{"detail-logs", func(d *driver) { openDetailGolden(t, d, engine.Docker, "web-api", 1) }},
		{"detail-logs-filter", func(d *driver) {
			openDetailGolden(t, d, engine.Docker, "web-api", 1)
			d.press("/", "e", "r", "r", "enter", "p")
		}},
		{"detail-inspect", func(d *driver) {
			openDetailGolden(t, d, engine.Docker, "web-api", 2)
			d.press("down", "right", "down", "down", "right")
		}},
		{"detail-network", func(d *driver) { openDetailGolden(t, d, engine.Incus, "web-lab", 3) }},
		{"detail-volumes", func(d *driver) { openDetailGolden(t, d, engine.Incus, "web-lab", 4) }},
		{"detail-resources", func(d *driver) { openDetailGolden(t, d, engine.Incus, "k3s-node1", 5) }},
		{"detail-removed", func(d *driver) {
			openDetailGolden(t, d, engine.Docker, "web-api", 0)
			d.send(ChangesMsg{Changes: homelab(t, func(w *fake.World) { w.Docker.Remove("web-api") })})
		}},
	}
	for _, id := range themeIDs {
		for _, sz := range sizes {
			for _, sc := range screens {
				t.Run(fmt.Sprintf("%s/%dx%d/%s", id, sz[0], sz[1], sc.name), func(t *testing.T) {
					d := newDriver(t, func(o *Options) {
						withEngines(nil)(o)
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

// openDetailGolden opens the detail of name on the given tab with a
// deterministic batch of streamed data.
func openDetailGolden(t *testing.T, d *driver, id engine.ID, name string, tab int) {
	t.Helper()
	d.send(ChangesMsg{Changes: append(homelab(t, nil), homelabMetrics(t, 5)...)})
	d.openDetailOf(name)
	d.send(detailBatch(t, id, name, 120))
	for range tab {
		d.press("tab")
	}
}
