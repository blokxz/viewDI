package detail

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
	"github.com/blokxz/viewDI/internal/ui/components/secret"
	"github.com/blokxz/viewDI/internal/ui/components/sparkline"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
)

// Breadcrumb is the detail's line under the header: where the user is on
// the left, the instance state on the right.
func (v *View) Breadcrumb(s *theme.Styles, from string, now time.Time, width int) string {
	in := v.inst
	eng, ok := s.EngineName[string(in.Engine)]
	if !ok {
		eng = s.Text
	}
	where := " " + s.Muted.Render(from+" › ") + s.Bold.Render(in.Name) + " " + eng.Render("("+in.Engine.Label()) +
		s.Muted.Render(project(in)) + eng.Render(")")
	state, level, muted := views.DisplayState(v.snap, in)
	st := s.LevelText(level)
	if muted {
		st = s.Muted
	}
	right := st.Render(state)
	if up := views.Uptime(in, now); up != "—" {
		right += s.Muted.Render("  up " + up)
	}
	if v.removed {
		right = s.LevelText(theme.LevelErr).Render("✖ removed")
	}
	right += " "
	gap := width - ansi.StringWidth(where) - ansi.StringWidth(right)
	if gap < 1 {
		return layout.Fit(where, width, 1)
	}
	return where + strings.Repeat(" ", gap) + right
}

func project(in engine.Instance) string {
	if in.Project == "" {
		return ""
	}
	return " · " + in.Project
}

// Render implements views.View: the tab strip, an optional removed notice,
// then the active tab.
func (v *View) Render(ctx views.Context) string {
	s := ctx.Styles
	v.height = max(1, ctx.Height-1)
	lines := []string{v.tabStrip(s, ctx.Width)}
	h := ctx.Height - 1
	if v.removed {
		lines = append(lines, s.Banner(theme.LevelErr).Render(layout.Fit("✖ This instance no longer exists — showing its last known data · esc back", ctx.Width-2, 1)))
		h--
	}
	if h > 0 {
		lines = append(lines, v.body(ctx, h))
	}
	return layout.Fit(strings.Join(lines, "\n"), ctx.Width, ctx.Height)
}

func (v *View) tabStrip(s *theme.Styles, width int) string {
	var b strings.Builder
	b.WriteByte(' ')
	for i, n := range tabNames {
		if Tab(i) == v.tab {
			b.WriteString(s.TabActive.Render(n))
		} else {
			b.WriteString(s.TabInactive.Render(n))
		}
	}
	left := b.String()
	right := v.tabInfo(s) + " "
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return layout.Fit(left, width, 1)
	}
	return left + strings.Repeat(" ", gap) + right
}

// tabInfo is the right side of the tab strip.
func (v *View) tabInfo(s *theme.Styles) string {
	switch v.tab {
	case TabResources:
		return s.Muted.Render("window: ") + s.Text.Render(fmtWindow(Windows[v.window])) + s.Muted.Render(" (+/-)")
	case TabInspect:
		if v.tree != nil && v.tree.Query() != "" {
			n, cur := v.tree.Matches()
			return s.Muted.Render(fmt.Sprintf("search /%s · %d/%d", v.tree.Query(), cur, n))
		}
		if v.tree != nil && v.tree.Typing() {
			return s.Accent.Render("search: /_")
		}
	}
	return ""
}

func fmtWindow(d time.Duration) string { return fmt.Sprintf("%dm", int(d/time.Minute)) }

func (v *View) body(ctx views.Context, h int) string {
	s, w := ctx.Styles, ctx.Width
	switch v.tab {
	case TabLogs:
		return v.renderLogs(s, w, h, ctx.Now)
	case TabInspect:
		return v.renderInspect(s, w, h)
	case TabNetwork:
		return v.scrolled(v.networkLines(s, ctx.Now), w, h)
	case TabVolumes:
		return v.scrolled(v.volumeLines(s, w), w, h)
	case TabResources:
		return v.renderResources(s, w, h, ctx.Now)
	}
	return v.renderOverview(s, w, h, ctx.Now)
}

// statusLine describes a stream that is not live, or "" when it is.
func statusLine(s *theme.Styles, what string, st StreamStatus, now time.Time) string {
	switch st.State {
	case Connecting:
		return s.Muted.Render(what + ": connecting…")
	case Ended:
		return s.Muted.Render(what + ": stream ended (instance not running) — resumes when it starts")
	case Retrying:
		retry := "retrying"
		if d := st.Retry.Sub(now).Round(time.Second); d > 0 {
			retry = fmt.Sprintf("retry in %ds", int(d/time.Second))
		}
		return s.LevelText(theme.LevelWarn).Render(fmt.Sprintf("⚠ %s: %s · %s", what, st.Err, retry))
	case Removed:
		return s.LevelText(theme.LevelErr).Render("✖ " + what + ": instance not found")
	case Failed:
		return s.LevelText(theme.LevelErr).Render("✖ " + what + ": " + st.Err)
	}
	return ""
}

// ----- Overview -----

type ovRow struct {
	label, value string // value is styled
	secret       string // env name whose value is masked ("" otherwise)
}

const labelW = 15

func (v *View) overviewRows(s *theme.Styles, now time.Time) []ovRow {
	if s == nil { // row count only (key handling)
		s = fallbackStyles()
	}
	in := v.inst
	var rows []ovRow
	add := func(label, value string) { rows = append(rows, ovRow{label: label, value: value}) }
	dx, _ := in.Extra.(*engine.DockerExtra)
	ix, _ := in.Extra.(*engine.IncusExtra)

	if desc, src := description(in); desc != "" {
		add("Description", s.Text.Render(desc)+s.Muted.Render("  ("+src+")"))
	}
	image := cmp.Or(in.Image, "—")
	if dx != nil && dx.ImageID != "" {
		image += s.Muted.Render("  " + shortDigest(dx.ImageID))
	}
	add("Image", s.Text.Render(image))
	if dx != nil && dx.Command != "" {
		add("Command", s.Text.Render(dx.Command))
	}
	state, level, _ := views.DisplayState(v.snap, in)
	st := s.LevelText(level).Render(state)
	if in.Health != engine.HealthNone {
		st += s.Muted.Render("   health ") + s.LevelText(healthLevel(in.Health)).Render(in.Health.String())
	}
	if dx != nil && in.State == engine.StateStopped {
		st += s.Muted.Render(fmt.Sprintf("   exit code %d", dx.ExitCode))
	}
	add("State", st)
	created := "—"
	if !in.Created.IsZero() {
		created = in.Created.In(time.Local).Format("2006-01-02 15:04")
	}
	times := s.Text.Render(created)
	if !in.Started.IsZero() {
		times += s.Muted.Render("   started ") + s.Text.Render(in.Started.In(time.Local).Format("2006-01-02 15:04"))
		if up := views.Uptime(in, now); up != "—" && !now.IsZero() {
			times += s.Muted.Render(" (up " + up + ")")
		}
	}
	add("Created", times)
	if dx != nil {
		if dx.RestartPolicy != "" {
			add("Restart policy", s.Text.Render(dx.RestartPolicy))
		}
		if dx.ComposeProject != "" {
			add("Compose", s.Text.Render("project "+dx.ComposeProject+" · service "+dx.ComposeService))
		}
	}
	if ix != nil {
		add("Project", s.Text.Render(cmp.Or(in.Project, "default"))+s.Muted.Render("   type ")+s.Text.Render(kindName(in.Kind)))
		if len(ix.Profiles) > 0 {
			add("Profiles", s.Text.Render(strings.Join(ix.Profiles, ", ")))
		}
		parts := []string{}
		if ix.Architecture != "" {
			parts = append(parts, "arch "+ix.Architecture)
		}
		if ix.Location != "" && ix.Location != "none" {
			parts = append(parts, "member "+ix.Location)
		}
		if ix.Ephemeral {
			parts = append(parts, "ephemeral")
		}
		if len(parts) > 0 {
			add("Placement", s.Text.Render(strings.Join(parts, " · ")))
		}
	}
	add("ID", s.Muted.Render(in.ID))
	if in.PID > 0 {
		add("PID", s.Text.Render(fmt.Sprint(in.PID)))
	}
	for i, k := range sortedKeys(in.Labels) {
		label := ""
		if i == 0 {
			label = "Labels"
		}
		add(label, s.Muted.Render(k+"=")+s.Text.Render(in.Labels[k]))
	}
	if len(in.Env) == 0 {
		msg := "— (loading…)"
		if v.inspected {
			msg = "—"
		}
		add("Env", s.Muted.Render(msg))
	}
	for i, kv := range in.Env {
		label := ""
		if i == 0 {
			label = "Env"
		}
		name, _, _ := strings.Cut(kv, "=")
		masked, isSecret := secret.Env(kv)
		row := ovRow{label: label}
		switch {
		case isSecret && v.revealed[name]:
			row.value, row.secret = s.Text.Render(kv)+s.Muted.Render("  (v hide)"), name
		case isSecret:
			row.value, row.secret = s.Text.Render(masked)+s.Muted.Render("  (v reveal)"), name
		default:
			row.value = s.Text.Render(kv)
		}
		rows = append(rows, row)
	}
	if v.inspectErr != "" {
		add("Inspect", s.LevelText(theme.LevelWarn).Render(v.inspectErr))
	}
	return rows
}

// fallbackStyles serves row counting outside rendering; built once.
var fallbackStyles = sync.OnceValue(func() *theme.Styles { return theme.NewStyles(theme.Fallback(), 0) })

func kindName(k engine.Kind) string {
	if k == engine.KindVM {
		return "virtual machine"
	}
	return "container"
}

func healthLevel(h engine.Health) theme.Level {
	switch h {
	case engine.HealthHealthy:
		return theme.LevelOK
	case engine.HealthUnhealthy:
		return theme.LevelErr
	}
	return theme.LevelInfo
}

// description finds a human description and says where it came from.
func description(in engine.Instance) (string, string) {
	const oci = "org.opencontainers.image.description"
	if d := in.Labels[oci]; d != "" {
		return d, "label " + oci
	}
	if x, ok := in.Extra.(*engine.IncusExtra); ok {
		if x.Description != "" {
			return x.Description, "instance description"
		}
		if x.ImageDescription != "" {
			return x.ImageDescription, "image.description"
		}
	}
	if d := in.Labels["description"]; d != "" {
		return d, "user.description"
	}
	return "", ""
}

func shortDigest(d string) string {
	hex := strings.TrimPrefix(d, "sha256:")
	if len(hex) > 16 {
		return "sha256:" + hex[:4] + "…" + hex[len(hex)-4:]
	}
	return d
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func (v *View) renderOverview(s *theme.Styles, w, h int, now time.Time) string {
	rows := v.overviewRows(s, now)
	v.ovCursor = max(0, min(len(rows)-1, v.ovCursor))
	switch {
	case v.ovCursor < v.ovOffset:
		v.ovOffset = v.ovCursor
	case v.ovCursor >= v.ovOffset+h:
		v.ovOffset = v.ovCursor - h + 1
	}
	v.ovOffset = max(0, min(v.ovOffset, len(rows)-h))
	var out []string
	for i := v.ovOffset; i < len(rows) && len(out) < h; i++ {
		r := rows[i]
		line := " " + s.Muted.Render(pad(r.label, labelW)) + r.value
		if i == v.ovCursor {
			plain := ansi.Truncate(ansi.Strip(line), w, "…")
			line = s.CursorLine.Render(plain + strings.Repeat(" ", max(0, w-ansi.StringWidth(plain))))
		}
		out = append(out, line)
	}
	return layout.Fit(strings.Join(out, "\n"), w, h)
}

// ----- Logs -----

func (v *View) renderLogs(s *theme.Styles, w, h int, now time.Time) string {
	status := " " + v.logs.Status(s)
	if st := statusLine(s, "log", v.logStatus, now); st != "" && v.logStatus.State != Live {
		status += s.Muted.Render(" · ") + st
	}
	if h <= 1 {
		return layout.Fit(status, w, h)
	}
	return layout.Fit(status, w, 1) + "\n" + margin(v.logs.Render(s, w-1, h-1))
}

// ----- Inspect -----

func (v *View) renderInspect(s *theme.Styles, w, h int) string {
	switch {
	case v.treeErr != "":
		return layout.Fit(s.LevelText(theme.LevelErr).Render(" cannot parse the inspect document: "+v.treeErr), w, h)
	case v.tree == nil && v.inspectErr != "":
		return layout.Fit(s.LevelText(theme.LevelWarn).Render(" inspect failed: "+v.inspectErr), w, h)
	case v.tree == nil:
		return layout.Fit(s.Muted.Render(" loading…"), w, h)
	}
	path := " " + s.Muted.Render("path ") + s.Text.Render(v.tree.Cursor().Path)
	if h <= 1 {
		return layout.Fit(path, w, h)
	}
	return layout.Fit(path, w, 1) + "\n" + margin(v.tree.Render(s, w-1, h-1))
}

// ----- Network and Volumes -----

func (v *View) scrolled(lines []string, w, h int) string {
	v.scroll = max(0, min(v.scroll, len(lines)-h))
	return layout.Fit(strings.Join(lines[v.scroll:], "\n"), w, h)
}

func (v *View) networkLines(s *theme.Styles, now time.Time) []string {
	in := v.inst
	out := []string{" " + s.PanelTitle.Render("Networks")}
	if len(in.Networks) == 0 {
		out = append(out, "   "+s.Muted.Render("no network attachments"))
	} else {
		out = append(out, "   "+s.TableHeader.Render(pad("NETWORK", 20)+" "+pad("IPV4", 18)+" "+pad("IPV6", 26)+" MAC"))
		for _, a := range in.Networks {
			out = append(out, "   "+s.Text.Render(pad(a.Network, 20))+" "+s.Text.Render(pad(dash(a.IPv4), 18))+" "+
				s.Text.Render(pad(dash(a.IPv6), 26))+" "+s.Muted.Render(dash(a.MAC)))
		}
	}
	out = append(out, "", " "+s.PanelTitle.Render("Interfaces")+s.Muted.Render(" (live)"))
	switch {
	case len(v.ifaces) > 0:
		out = append(out, "   "+s.TableHeader.Render(pad("IFACE", 12)+" "+padLeft("RX/s", 11)+" "+padLeft("TX/s", 11)+" "+padLeft("RX total", 11)+" "+padLeft("TX total", 11)))
		for _, i := range v.ifaces {
			out = append(out, "   "+s.Text.Render(pad(i.Name, 12))+" "+s.ChartNetRx.Render(padLeft(metrics.BytesRate(i.RxRate), 11))+" "+
				s.ChartNetTx.Render(padLeft(metrics.BytesRate(i.TxRate), 11))+" "+s.Muted.Render(padLeft(metrics.Bytes(i.Rx), 11)+" "+padLeft(metrics.Bytes(i.Tx), 11)))
		}
	case v.inst.State != engine.StateRunning:
		out = append(out, "   "+s.Muted.Render("not running"))
	default:
		out = append(out, "   "+cmp.Or(statusLine(s, "stats", v.statsStatus, now), s.Muted.Render("waiting for samples…")))
	}
	out = append(out, "", " "+s.PanelTitle.Render("Ports"))
	if len(in.Ports) == 0 {
		out = append(out, "   "+s.Muted.Render("no published ports"))
	} else {
		out = append(out, "   "+s.TableHeader.Render(pad("HOST", 24)+" "+pad("→ INSTANCE", 12)+" PROTO"))
		for _, p := range in.Ports {
			host := "—"
			if p.HostPort != 0 {
				host = fmt.Sprintf("%s:%d", cmp.Or(p.HostIP, "*"), p.HostPort)
			}
			out = append(out, "   "+s.Text.Render(pad(host, 24))+" "+s.Text.Render(pad(fmt.Sprintf("→ %d", p.ContainerPort), 12))+" "+s.Muted.Render(p.Protocol))
		}
	}
	return out
}

func (v *View) volumeLines(s *theme.Styles, w int) []string {
	in := v.inst
	usage := map[string]uint64{}
	for _, d := range v.disks {
		usage[d.Name] = d.Used
	}
	out := []string{" " + s.PanelTitle.Render("Mounts")}
	pathW := max(12, (w-3-7-4-10-4)/2)
	hdr := pad("TYPE", 7) + " " + pad("SOURCE", pathW) + " " + pad("DESTINATION", pathW) + " " + pad("MODE", 4) + " " + padLeft("USAGE", 10)
	out = append(out, "   "+s.TableHeader.Render(hdr))
	row := func(typ, src, dst string, ro bool, used string) string {
		mode := "rw"
		if ro {
			mode = "ro"
		}
		modeStyle := s.Text
		if ro {
			modeStyle = s.LevelText(theme.LevelInfo)
		}
		return "   " + s.Muted.Render(pad(typ, 7)) + " " + s.Text.Render(pad(src, pathW)) + " " + s.Text.Render(pad(dst, pathW)) + " " +
			modeStyle.Render(pad(mode, 4)) + " " + s.Text.Render(padLeft(used, 10))
	}
	if u, ok := usage["root"]; ok {
		out = append(out, row("disk", "root", "/", false, metrics.Bytes(u)))
	}
	for _, m := range in.Mounts {
		used := "—"
		if u, ok := usage[m.Name]; ok && m.Name != "" {
			used = metrics.Bytes(u)
		}
		out = append(out, row(m.Type, m.Source, m.Destination, m.ReadOnly, used))
	}
	if len(in.Mounts) == 0 && len(usage) == 0 {
		out = append(out, "   "+s.Muted.Render("no mounts"))
	}
	if in.Engine == engine.Docker {
		out = append(out, "", " "+s.Muted.Render("Docker reports no per-mount usage; see `docker system df -v`."))
	}
	return out
}

func dash(s string) string { return cmp.Or(s, "—") }

// margin indents every line of a block by one cell.
func margin(block string) string { return " " + strings.ReplaceAll(block, "\n", "\n ") }

// ----- Resources -----

func (v *View) renderResources(s *theme.Styles, w, h int, now time.Time) string {
	header := ""
	if st := statusLine(s, "stats", v.statsStatus, now); st != "" {
		header = " " + st
	}
	p, ok := v.points.Last()
	if !ok {
		msg := "waiting for samples…"
		if v.inst.State != engine.StateRunning {
			msg = "not running — no live metrics"
		}
		return layout.Fit(cmp.Or(header, " "+s.Muted.Render(msg)), w, h)
	}
	series := v.windowPoints(p.Time)
	pick := func(f func(metrics.Point) float64) []float64 {
		out := make([]float64, len(series))
		for i, q := range series {
			out[i] = f(q)
		}
		return out
	}
	var lines []string
	if header != "" {
		lines = append(lines, header)
	}
	charts := 4
	chartH := max(1, (h-len(lines)-charts-1)/charts)

	cpuText := " CPU  " + metrics.Percent(p.CPU) + "%"
	cpuTop := 0.0
	if p.CPULimit > 0 {
		cpuTop = p.CPULimit * 100
		cpuText += fmt.Sprintf("  (limit %g cpus · %.0f%% of it)", p.CPULimit, p.CPUOfLimit())
	}
	lines = append(lines, s.Bold.Render(cpuText))
	lines = append(lines, chart(s.ChartCPU, pick(func(q metrics.Point) float64 { return q.CPU }), w, chartH, cpuTop)...)

	memText := " MEM  " + metrics.Bytes(p.MemUsed)
	memTop := 0.0
	if p.MemLimit > 0 {
		memTop = float64(p.MemLimit)
		memText += fmt.Sprintf(" / %s (%.0f%%)", metrics.Bytes(p.MemLimit), p.MemPercent())
	}
	lines = append(lines, s.Bold.Render(memText))
	lines = append(lines, chart(s.ChartMem, pick(func(q metrics.Point) float64 { return float64(q.MemUsed) }), w, chartH, memTop)...)

	lines = append(lines, s.Bold.Render(fmt.Sprintf(" NET  ↓ %s  ↑ %s", metrics.BytesRate(p.NetRx), metrics.BytesRate(p.NetTx))))
	lines = append(lines, chart(s.ChartNetRx, pick(func(q metrics.Point) float64 { return q.NetRx + q.NetTx }), w, chartH, 0)...)

	blk := " BLK  "
	if p.Has&engine.HasBlk != 0 {
		blk += fmt.Sprintf("r %s  w %s", metrics.BytesRate(p.BlkRead), metrics.BytesRate(p.BlkWrite))
	} else {
		blk += "not reported by this engine"
	}
	lines = append(lines, s.Bold.Render(blk))
	lines = append(lines, chart(s.ChartDisk, pick(func(q metrics.Point) float64 { return q.BlkRead + q.BlkWrite }), w, chartH, 0)...)

	lines = append(lines, s.Bold.Render(fmt.Sprintf(" PIDS %d", p.PIDs))+s.Muted.Render(fmt.Sprintf("   %d samples in the last %s", len(series), fmtWindow(Windows[v.window]))))
	return layout.Fit(strings.Join(lines, "\n"), w, h)
}

// windowPoints returns the points of the selected window, ending at now.
func (v *View) windowPoints(now time.Time) []metrics.Point {
	all := v.points.Tail(nil, v.points.Len())
	from := now.Add(-Windows[v.window])
	i := 0
	for i < len(all) && all[i].Time.Before(from) {
		i++
	}
	return all[i:]
}

// chart draws an area chart with a left margin; with a limit the top edge
// is the limit.
func chart(st lipgloss.Style, values []float64, w, h int, top float64) []string {
	rows := sparkline.Chart(values, max(1, w-2), h, top)
	for i, r := range rows {
		rows[i] = " " + st.Render(r)
	}
	return rows
}

func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

func padLeft(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return strings.Repeat(" ", max(0, w-ansi.StringWidth(s))) + s
}
