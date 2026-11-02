package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/jobs"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/components/banner"
	"github.com/blokxz/viewDI/internal/ui/components/form"
	"github.com/blokxz/viewDI/internal/ui/theme"
	updatesview "github.com/blokxz/viewDI/internal/ui/views/updates"
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
		{"tree", func(d *driver) {
			d.send(ChangesMsg{Changes: append(homelab(t, nil), homelabMetrics(t, 30)...)})
			d.press("4", "down", "down", "down", "down", "down", "down", "down", "down", "down", "right")
		}},
		{"tree-network-compose", func(d *driver) {
			d.send(ChangesMsg{Changes: append(homelab(t, nil), homelabMetrics(t, 30)...)})
			d.press("4", "down", "down", "down", "down", "down", "down", "down", "down", "g", "g", "down")
		}},
		{"tree-filter", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.press("4", "/", "w", "e", "b", "enter", "ctrl+n")
		}},
		{"tree-labs", func(d *driver) {
			d.send(ChangesMsg{Changes: append(homelab(t, nil), homelabMetrics(t, 30)...)})
			d.press("4", "g", "g", "g", "g", "down", "down", "down")
		}},
		{"tree-lab-menu", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.press("4", "g", "g", "g", "g", "down", "down", "down", "L")
		}},
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
		{"table-pending", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.openDetailOf("redis-cache")
			d.press("esc", "x")
			d.send(JobsMsg{Jobs: []jobs.Job{d.jobs.job(1, jobs.Running, "")}})
		}},
		{"jobs", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.press("3", "space", "space", "space", "x", "y")
			d.send(JobsMsg{Jobs: []jobs.Job{
				d.jobs.job(1, jobs.Succeeded, ""), d.jobs.job(2, jobs.Failed, "instance is not running"), d.jobs.job(3, jobs.Running, ""),
			}})
			d.press("5")
		}},
		{"bulk-confirm", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.press("3", "space", "space", "R")
		}},
		{"delete-dialog", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.openDetailOf("web-api")
			d.press("d", "w", "e", "b")
		}},
		{"create-docker-form", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.press("n", "enter")
			d.send(form.SuggestionsMsg{Kind: "create-docker", Field: "image", Items: []string{"nginx:1.27", "node:22-alpine", "postgres:17", "redis:7"}, Note: "9 local images"})
			d.typeText("n")
		}},
		{"create-incus-errors", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.press("n", "down", "enter", "tab")
			d.typeText("9lab")
			d.press("enter")
		}},
		{"import-files", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.press("i", "down", "enter", "down")
		}},
		{"jobs-progress", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			spec := engine.CreateSpec{Name: "new-web", Image: "nginx:1.27", Pull: true}
			imp := engine.ImportSpec{Kind: engine.ImportIncusImage, Path: "custom.tar.gz", Name: "custom/app"}
			d.jobs.reqs = append(d.jobs.reqs,
				jobs.Request{Key: engine.MakeKey(engine.Docker, ""), Name: "new-web", Create: &spec},
				jobs.Request{Key: engine.MakeKey(engine.Incus, ""), Name: "custom.tar.gz", Import: &imp},
			)
			running := d.jobs.job(1, jobs.Running, "")
			running.Progress = engine.Progress{Phase: "pulling", Current: 45 << 20, Total: 100 << 20}
			done := d.jobs.job(2, jobs.Succeeded, "")
			done.Result = "custom/app"
			d.send(JobsMsg{Jobs: []jobs.Job{running, done}})
			d.press("5")
		}},
		{"updates", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.send(UpdatesMsg{Report: homelabReport(t)})
			d.press("6", "down")
		}},
		{"updates-failed", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.send(UpdatesMsg{Report: scenarioReports(t, "offline", 1)[0]})
			d.press("6")
		}},
		{"updates-table", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.send(UpdatesMsg{Report: homelabReport(t)})
			d.press("3")
		}},
		{"updates-tree", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			d.send(UpdatesMsg{Report: homelabReport(t)})
			d.press("4", "down", "down", "down")
		}},
		{"updates-pulled", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			rep := homelabReport(t)
			for i := range rep.Images {
				if rep.Images[i].Check.Ref == "nginx:1.27" {
					rep.Images[i].Pulled = true
				}
			}
			d.send(UpdatesMsg{Report: rep})
			d.press("6", "down", "down", "down")
		}},
		{"updates-recreate-preview", func(d *driver) {
			d.send(ChangesMsg{Changes: homelab(t, nil)})
			rep := homelabReport(t)
			d.send(UpdatesMsg{Report: rep})
			d.press("6")
			d.send(updatesview.ImageActionMsg{Action: updatesview.ActRecreate, Image: imageNamed(t, rep, "nginx:1.27")})
		}},
		{"detail-removed", func(d *driver) {
			openDetailGolden(t, d, engine.Docker, "web-api", 0)
			d.send(ChangesMsg{Changes: homelab(t, func(w *fake.World) { w.Docker.Remove("web-api") })})
		}},
	}
	for _, id := range themeIDs {
		for _, sz := range sizes {
			for _, sc := range screens {
				t.Run(fmt.Sprintf("%s/%dx%d/%s", id, sz[0], sz[1], sc.name), func(t *testing.T) {
					jr := &stubJobs{}
					d := newDriver(t, withJobs(jr), func(o *Options) {
						withEngines(nil)(o)
						o.Theme, _ = o.Themes.Get(id)
						o.State = config.State{}
						if sc.name == "banner-readonly-mock" {
							o.ReadOnly, o.Mock = true, "homelab"
						}
						o.WorkDir = "testdata/import"
						o.Labs = goldenLabs()
						if strings.HasPrefix(sc.name, "updates") {
							o.Updates = &stubUpdater{}
						}
					})
					d.jobs = jr
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

// goldenLabs spans both engines, with a stopped member (a grey link) and
// a missing one.
func goldenLabs() config.Labs {
	var ls config.Labs
	web, _ := ls.Add("web-stack")
	k8s, _ := ls.Add("k8s")
	docker := func(name string) config.LabMember { return config.LabMember{Engine: "docker", Name: name} }
	incus := func(project, name string) config.LabMember {
		return config.LabMember{Engine: "incus", Project: project, Name: name}
	}
	for _, l := range [][2]config.LabMember{
		{docker("nginx-proxy"), docker("web-api")},
		{docker("web-api"), docker("web-db")},
		{docker("web-api"), docker("redis-cache")},
		{docker("nginx-proxy"), incus("default", "web-lab")},
		{incus("default", "web-lab"), docker("postgres-dev")},
		{docker("web-api"), incus("default", "legacy-vm")},
	} {
		_, _ = ls.Link(web, l[0], l[1])
	}
	_, _ = ls.Link(k8s, incus("default", "k3s-node1"), incus("lab", "dev-arch"))
	return ls
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
