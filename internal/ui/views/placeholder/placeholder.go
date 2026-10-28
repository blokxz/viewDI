// Package placeholder provides the Stage 0 stand-ins for the six main views.
// Each one renders its panes with the responsive layout and announces the
// stage that delivers the real content.
package placeholder

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/blokxz/viewDI/internal/ui/components/panel"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/views"
)

// Pane is one placeholder panel.
type Pane struct {
	Title string
	Lines []string
}

// View is a placeholder view.
type View struct {
	title     string
	stage     int
	scope     keys.Scope
	instances bool
	panes     []Pane
}

var _ views.View = View{}

// New builds a placeholder.
func New(title string, stage int, scope keys.Scope, showsInstances bool, panes ...Pane) View {
	return View{title: title, stage: stage, scope: scope, instances: showsInstances, panes: panes}
}

// All returns the six placeholder views, in tab order 1–6.
func All() []views.View {
	return []views.View{
		New("Admin", 2, "", true,
			Pane{"Docker daemon", []string{"status · version · socket type", "containers ▶ ■ ⏸ · images · volumes", "storage driver · cgroup version"}},
			Pane{"Incus daemon", []string{"status · version · project", "instances (containers and VMs)", "storage pools · networks · profiles"}},
		),
		New("Resources", 2, keys.Resources, false,
			Pane{"Docker", []string{"CPU · RAM · NET I/O · DISK I/O", "aggregated over running containers"}},
			Pane{"Incus", []string{"CPU · RAM · NET I/O · DISK I/O", "containers and VMs", "top consumers (o/O to sort)"}},
		),
		New("Table", 1, keys.Table, true,
			Pane{"Instances", []string{"engine · name · kind · state · image · CPU% · MEM · uptime", "sort o/O · filter / · engine e · project P"}},
		),
		New("Tree", 5, keys.Tree, true,
			Pane{"Tree", []string{"engine → network/project → instance → ports/mounts", "←/→ collapse/expand · g grouping · J next occurrence"}},
			Pane{"Inspector", []string{"follows the cursor", "reads snapshots only — no daemon I/O"}},
		),
		New("Jobs", 4, "", false,
			Pane{"Running", []string{"create · import · pull · delete", "progress and cancellation"}},
			Pane{"History", []string{"finished and failed jobs"}},
		),
		New("Updates", 6, keys.Updates, false,
			Pane{"Engines", []string{"installed → available", "upgrade hints (viewDI never installs)"}},
			Pane{"Images", []string{"registry digest checks", "p pull · c recreate · z snooze · I ignore"}},
		),
	}
}

// Title implements views.View.
func (v View) Title() string { return v.title }

// Scope implements views.View.
func (v View) Scope() keys.Scope { return v.scope }

// ShowsInstances implements views.View.
func (v View) ShowsInstances() bool { return v.instances }

// Panes implements views.View.
func (v View) Panes() int { return max(1, len(v.panes)) }

// Update implements views.View.
func (v View) Update(tea.Msg) (views.View, tea.Cmd) { return v, nil }

// HandleKey implements views.View. Placeholders consume no keys.
func (v View) HandleKey(tea.KeyPressMsg, int) (views.View, tea.Cmd, bool) { return v, nil, false }

// Hints implements views.View.
func (v View) Hints(*keys.Map) []key.Binding { return nil }

// Render implements views.View: panes side by side in wide mode, stacked otherwise.
func (v View) Render(ctx views.Context) string {
	s := ctx.Styles
	n := len(v.panes)
	if n == 0 {
		return layout.Fit("", ctx.Width, ctx.Height)
	}
	body := func(p Pane) string {
		lines := []string{
			s.Accent.Render(fmt.Sprintf("Coming in Stage %d", v.stage)),
			"",
		}
		for _, l := range p.Lines {
			lines = append(lines, s.Muted.Render("• "+l))
		}
		return strings.Join(lines, "\n")
	}

	var blocks []string
	if ctx.Mode == layout.Wide {
		for i, w := range layout.Split(ctx.Width, n) {
			blocks = append(blocks, panel.Render(s, v.panes[i].Title, body(v.panes[i]), w, ctx.Height, i == ctx.Focus))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, blocks...)
	}
	for i, h := range layout.Split(ctx.Height, n) {
		blocks = append(blocks, panel.Render(s, v.panes[i].Title, body(v.panes[i]), ctx.Width, h, i == ctx.Focus))
	}
	return lipgloss.JoinVertical(lipgloss.Left, blocks...)
}
