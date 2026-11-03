package tree

import (
	"fmt"
	"strings"

	"github.com/blokxz/viewDI/internal/apps"

	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/sparkline"
	"github.com/blokxz/viewDI/internal/ui/components/tree"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
)

// sparkPoints is how many samples an inspector sparkline shows.
const sparkPoints = 30

// inspector describes the inspected node in w×h cells. compact is the
// six-line version shown under the tree on narrow screens. It reads only
// the snapshot.
func (v *View) inspector(s *theme.Styles, w, h int, compact bool, ctx views.Context) string {
	t := v.t()
	n, ok := t.Node(v.inspected)
	if !ok {
		if n, ok = t.Current(); !ok {
			return fitLines([]string{s.Muted.Render("Nothing selected.")}, w, h)
		}
	}
	ins := &inspect{s: s, w: w, compact: compact}
	it := data(n)
	switch it.kind {
	case kRoot:
		v.inspectRoot(ins)
	case kEngine:
		v.inspectEngine(ins, it.engine)
	case kNetwork:
		v.inspectNetwork(ins, n, it)
	case kProject, kCompose, kOther:
		v.inspectGroup(ins, n, it)
	case kApp:
		v.inspectApp(ins, n, it)
	case kInstance:
		v.inspectInstance(ins, it.key, ctx)
	case kLab:
		v.inspectLab(ins, it.lab)
	case kMissing:
		v.inspectMissing(ins, it)
	case kPort, kMount, kAlso:
		v.inspectLeaf(ins, it)
	}
	return fitLines(ins.lines, w, h)
}

// inspect accumulates inspector lines.
type inspect struct {
	s       *theme.Styles
	w       int
	compact bool
	lines   []string
}

// title adds the heading: a left part and a right-aligned kind, plus a
// ruler unless compact.
func (i *inspect) title(left, right string) {
	gap := i.w - ansi.StringWidth(left) - ansi.StringWidth(right)
	line := left
	if gap >= 1 {
		line += strings.Repeat(" ", gap) + right
	}
	i.lines = append(i.lines, line)
	if !i.compact {
		i.lines = append(i.lines, i.s.Separator.Render(strings.Repeat("─", i.w)))
	}
}

// kv adds a labelled row; value is already styled.
func (i *inspect) kv(k, value string) {
	i.lines = append(i.lines, i.s.Muted.Render(fmt.Sprintf("%-9s", k))+value)
}

// list adds a labelled list, one value per line, at most max lines.
func (i *inspect) list(k string, values []string, limit int) {
	for j, val := range values {
		if j == limit-1 && len(values) > limit {
			i.kv("", i.s.Muted.Render(fmt.Sprintf("+%d more", len(values)-j)))
			return
		}
		if j > 0 {
			k = ""
		}
		i.kv(k, val)
	}
}

func (i *inspect) blank() {
	if !i.compact {
		i.lines = append(i.lines, "")
	}
}

func (v *View) inspectRoot(i *inspect) {
	s := i.s
	i.title(s.Bold.Render("viewDI"), s.Muted.Render("grouping: "+v.grouping.Label()))
	if v.snap == nil {
		i.lines = append(i.lines, s.Muted.Render("Connecting to the engines…"))
		return
	}
	for _, ev := range v.snap.Engines {
		c := ev.Counts
		i.kv(ev.ID.Label(), health(s, ev)+s.Muted.Render(fmt.Sprintf(" · %d machines, %d running", c.Total, c.Running)))
	}
	i.blank()
	i.lines = append(i.lines, s.Muted.Render("g changes the grouping · / filters"))
}

func health(s *theme.Styles, ev store.EngineView) string {
	switch ev.Health.State {
	case store.StateConnected:
		if !ev.InfoKnown {
			return s.Muted.Render("connecting…")
		}
		return s.LevelText(theme.LevelOK).Render("● connected")
	case store.StateDegraded:
		return s.LevelText(theme.LevelWarn).Render("▲ reconnecting")
	case store.StateUnavailable:
		return s.LevelText(theme.LevelErr).Render("✖ unavailable")
	}
	return s.Muted.Render("connecting…")
}

func (v *View) inspectEngine(i *inspect, id engine.ID) {
	s := i.s
	ev := v.snap.Engine(id)
	icon := v.icons.docker
	if id == engine.Incus {
		icon = v.icons.incus
	}
	style := s.EngineName[string(id)]
	i.title(style.Render(icon+" "+id.Label())+s.Muted.Render(" "+textsafe.Clean(ev.Info.Version)), s.Muted.Render("engine"))
	status := health(s, ev)
	if ev.Health.Err != "" && ev.Health.State != store.StateConnected {
		status += s.Muted.Render(" · " + textsafe.Clean(ev.Health.Err))
	}
	i.kv("Status", status)
	if !ev.InfoKnown {
		return
	}
	c := ev.Counts
	i.kv("Machines", s.Text.Render(fmt.Sprintf("%d", c.Total))+s.Muted.Render(fmt.Sprintf("  ▶ %d running  ■ %d stopped  ⏸ %d paused", c.Running, c.Stopped, c.Paused)))
	if !i.compact {
		if ev.Info.APIVersion != "" {
			i.kv("API", s.Text.Render(textsafe.Clean(ev.Info.APIVersion)))
		}
		if ev.Info.Socket != "" {
			i.kv("Socket", s.Text.Render(textsafe.Clean(ev.Info.Socket)))
		}
		i.kv("Networks", s.Text.Render(fmt.Sprint(len(ev.Networks)))+s.Muted.Render(fmt.Sprintf(" · %d volumes", len(ev.Volumes))))
		if id == engine.Incus {
			p := "all projects"
			if ev.Project != "" {
				p = ev.Project
			}
			i.kv("Project", s.Text.Render(textsafe.Clean(p)))
		}
		if ev.Info.Capabilities.Restricted {
			i.kv("Access", s.LevelText(theme.LevelWarn).Render("restricted")+s.Muted.Render(" · "+textsafe.Clean(ev.Info.Capabilities.Reason)))
		}
	}
	i.blank()
	v.sparks(i, v.snap.EngineSeries(id, sparkPoints))
}

func (v *View) inspectNetwork(i *inspect, n *tree.Node, it item) {
	s := i.s
	net := it.net
	i.title(s.TreeGroup.Render(v.icons.group+" "+textsafe.Clean(net.Name)), s.Muted.Render(string(it.engine)+" network"))
	kind := net.Driver
	if it.engine == engine.Incus && kind != "" {
		if net.Managed {
			kind += " (managed)"
		} else {
			kind += " (unmanaged)"
		}
	}
	if net.Internal {
		kind += ", internal"
	}
	if kind != "" {
		i.kv("Type", s.Text.Render(textsafe.Clean(kind)))
	}
	if len(net.Subnets) > 0 {
		i.kv("Subnet", s.Text.Render(textsafe.Clean(strings.Join(net.Subnets, ", "))))
	}
	if net.Gateway != "" && !i.compact {
		i.kv("Gateway", s.Text.Render(textsafe.Clean(net.Gateway)))
	}
	names, rx, tx := v.members(n)
	i.kv("Members", s.Text.Render(fmt.Sprint(len(names)))+s.Muted.Render(" · "+textsafe.Clean(strings.Join(names, ", "))))
	i.kv("Traffic", s.ChartNetRx.Render("↓ "+metrics.BytesRate(rx))+"  "+s.ChartNetTx.Render("↑ "+metrics.BytesRate(tx)))
}

// members returns a group's instance names and their summed network rates.
func (v *View) members(n *tree.Node) (names []string, rx, tx float64) {
	for _, c := range n.Children {
		ci := data(c)
		in, ok := v.snap.Lookup(ci.key)
		if !ok {
			continue
		}
		names = append(names, in.Name)
		if p, ok := v.snap.Point(ci.key); ok {
			rx += p.NetRx
			tx += p.NetTx
		}
	}
	return names, rx, tx
}

func (v *View) inspectGroup(i *inspect, n *tree.Node, it item) {
	s := i.s
	kind := map[kind]string{kProject: "incus project", kCompose: "compose project", kOther: "group"}[it.kind]
	i.title(s.TreeGroup.Render(v.icons.group+" "+textsafe.Clean(it.name)), s.Muted.Render(kind))
	names, rx, tx := v.members(n)
	running := 0
	for _, c := range n.Children {
		if in, ok := v.snap.Lookup(data(c).key); ok && in.State == engine.StateRunning {
			running++
		}
	}
	i.kv("Machines", s.Text.Render(fmt.Sprint(len(names)))+s.Muted.Render(fmt.Sprintf(" · %d running", running)))
	i.kv("Members", s.Muted.Render(textsafe.Clean(strings.Join(names, ", "))))
	i.kv("Traffic", s.ChartNetRx.Render("↓ "+metrics.BytesRate(rx))+"  "+s.ChartNetTx.Render("↑ "+metrics.BytesRate(tx)))
	if it.name == notInLab {
		i.blank()
		i.lines = append(i.lines, s.Muted.Render("L on a machine starts a lab or adds it to one."))
	}
}

func (v *View) inspectInstance(i *inspect, k engine.Key, ctx views.Context) {
	s := i.s
	in, ok := v.snap.Lookup(k)
	if !ok {
		i.lines = append(i.lines, s.Muted.Render("This machine is gone."))
		return
	}
	text, level, muted := views.DisplayState(v.snap, in)
	glyph, words := v.stateGlyph(text)
	st := s.LevelText(level)
	if muted {
		st = s.Muted
	}
	kind := string(in.Engine) + " container"
	if in.Kind == engine.KindVM {
		kind = string(in.Engine) + " vm"
	}
	i.title(st.Render(glyph+" ")+s.Bold.Render(textsafe.Clean(in.Name)), s.EngineName[string(in.Engine)].Render(kind))
	state := st.Render(glyph + " " + words)
	if up := views.Uptime(in, ctx.Now); up != "—" {
		state += s.Muted.Render(" · up " + up)
	}
	i.kv("State", state)
	i.kv("Image", s.Text.Render(textsafe.Clean(cmpOr(in.Image, "—"))))
	if u, ok := v.snap.ImageUpdate(k); ok {
		cur, next := shortDigest(u.Current), shortDigest(u.Available)
		if u.Serial != "" && u.NewSerial != "" && u.Serial != u.NewSerial {
			cur, next = u.Serial, u.NewSerial
		}
		i.kv("Update", s.Badge.Render(v.icons.update+" ")+s.Text.Render(textsafe.Clean(cur+" → "+next)))
	}
	if i.compact {
		if len(in.Networks) > 0 {
			a := in.Networks[0]
			i.kv("IP", s.Text.Render(textsafe.Clean(a.IPv4))+s.Muted.Render(" on "+textsafe.Clean(a.Network)))
		}
		v.sparks(i, v.snap.Series(k, sparkPoints))
		return
	}
	switch x := in.Extra.(type) {
	case *engine.DockerExtra:
		if x.ComposeProject != "" {
			i.kv("Compose", s.Text.Render(textsafe.Clean(x.ComposeProject+" / "+x.ComposeService)))
		}
	case *engine.IncusExtra:
		i.kv("Project", s.Text.Render(textsafe.Clean(cmpOr(in.Project, "default")))+s.Muted.Render(" · profiles "+textsafe.Clean(strings.Join(x.Profiles, ", "))))
	}
	var ips []string
	for _, a := range in.Networks {
		ip := cmpOr(a.IPv4, a.IPv6, "—")
		ips = append(ips, s.TreeGroup.Render(fmt.Sprintf("%-13s", ansi.Truncate(textsafe.Clean(a.Network), 13, "…")))+" "+s.Text.Render(textsafe.Clean(ip)))
	}
	i.list("IPs", ips, 3)
	var ports []string
	for _, p := range in.Ports {
		if p.HostPort != 0 {
			ports = append(ports, s.Text.Render(fmt.Sprintf("%s → %d/%s", hostPort(p), p.ContainerPort, p.Protocol)))
		}
	}
	i.list("Ports", ports, 3)
	var mounts []string
	for _, m := range in.Mounts {
		ro := ""
		if m.ReadOnly {
			ro = s.Muted.Render(" (ro)")
		}
		mounts = append(mounts, s.Text.Render(textsafe.Clean(m.Source+" → "+m.Destination))+ro)
	}
	i.list("Mounts", mounts, 3)
	if in.Health != engine.HealthNone {
		hl := theme.LevelOK
		if in.Health == engine.HealthUnhealthy {
			hl = theme.LevelWarn
		}
		i.kv("Health", s.LevelText(hl).Render(in.Health.String()))
	}
	v.labLines(i, memberOf(in))
	i.blank()
	v.sparks(i, v.snap.Series(k, sparkPoints))
}

// sparks adds the CPU, memory and network lines for a series (nothing when
// the series is empty, e.g. a stopped instance).
func (v *View) sparks(i *inspect, series []metrics.Point) {
	s := i.s
	if len(series) == 0 {
		i.lines = append(i.lines, s.Muted.Render("No metrics yet."))
		return
	}
	last := series[len(series)-1]
	cpu, mem, net := make([]float64, len(series)), make([]float64, len(series)), make([]float64, len(series))
	for j, p := range series {
		cpu[j], mem[j], net[j] = p.CPU, float64(p.MemUsed), p.NetRx+p.NetTx
	}
	sw := max(4, min(sparkPoints, i.w-24))
	if i.compact { // one line less wide: room for the value column
		sw = max(4, min(sparkPoints, i.w-26))
	}
	memText := metrics.Bytes(last.MemUsed)
	memSpark := sparkline.Trend(mem, sw) // changes show without a limit to scale to
	if last.MemLimit > 0 {
		memText += "/" + metrics.Bytes(last.MemLimit)
		memSpark = sparkline.Render(mem, sw, float64(last.MemLimit))
	}
	row := func(label, value string, style func(string) string, spark string) string {
		return s.Muted.Render(label) + s.Text.Render(fmt.Sprintf("%-19s", value)) + style(spark)
	}
	render := func(st interface{ Render(...string) string }) func(string) string {
		return func(x string) string { return st.Render(x) }
	}
	i.lines = append(i.lines,
		row("CPU  ", metrics.Percent(last.CPU)+"%", render(s.ChartCPU), sparkline.Trend(cpu, sw)),
		row("MEM  ", memText, render(s.ChartMem), memSpark),
		row("NET  ", "↓"+metrics.BytesRate(last.NetRx)+" ↑"+metrics.BytesRate(last.NetTx), render(s.ChartNetRx), sparkline.Trend(net, sw)),
	)
}

func (v *View) inspectLeaf(i *inspect, it item) {
	s := i.s
	owner := ""
	if in, ok := v.snap.Lookup(it.key); ok {
		owner = textsafe.Clean(in.Name)
	}
	switch it.kind {
	case kPort:
		p := it.port
		i.title(s.ChartNetRx.Render(v.icons.port+" ")+s.Bold.Render(fmt.Sprintf("port %d/%s", p.ContainerPort, p.Protocol)), s.Muted.Render("of "+owner))
		if p.HostPort == 0 {
			i.kv("Host", s.Muted.Render("not published"))
			return
		}
		i.kv("Host", s.Text.Render(hostPort(p)))
		reach, lvl := "every interface", theme.LevelWarn
		switch p.HostIP {
		case "127.0.0.1", "::1":
			reach, lvl = "this host only", theme.LevelOK
		case "", "0.0.0.0", "::":
		default:
			reach, lvl = "the "+p.HostIP+" interface", theme.LevelInfo
		}
		i.kv("Reach", s.LevelText(lvl).Render(reach))
	case kMount:
		m := it.mount
		i.title(s.ChartDisk.Render(v.icons.mount+" ")+s.Bold.Render(textsafe.Clean(m.Destination)), s.Muted.Render("of "+owner))
		i.kv("Type", s.Text.Render(textsafe.Clean(cmpOr(m.Type, "—"))))
		if m.Name != "" {
			i.kv("Device", s.Text.Render(textsafe.Clean(m.Name)))
		}
		i.kv("Source", s.Text.Render(textsafe.Clean(m.Source)))
		mode := "read-write"
		if m.ReadOnly {
			mode = "read-only"
		}
		i.kv("Mode", s.Text.Render(mode))
	case kAlso:
		i.title(s.TreeGroup.Render(v.icons.also+" ")+s.Bold.Render("also on "+textsafe.Clean(it.also)), s.Muted.Render("of "+owner))
		if in, ok := v.snap.Lookup(it.key); ok {
			for _, a := range in.Networks {
				if a.Network == it.also {
					i.kv("IP", s.Text.Render(textsafe.Clean(cmpOr(a.IPv4, a.IPv6, "—"))))
				}
			}
		}
		i.lines = append(i.lines, s.Muted.Render("J on the machine jumps to its place there."))
	}
}

func cmpOr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// instanceOf finds the machine a lab member names.
func (v *View) instanceOf(m config.LabMember) (engine.Instance, bool) {
	if v.snap == nil {
		return engine.Instance{}, false
	}
	if v.byMemberOf != v.snap {
		v.byMember = make(map[config.LabMember]engine.Key, len(v.snap.Instances))
		for _, in := range v.snap.Instances {
			v.byMember[memberOf(in)] = in.Key()
		}
		v.byMemberOf = v.snap
	}
	k, ok := v.byMember[m]
	if !ok {
		return engine.Instance{}, false
	}
	return v.snap.Lookup(k)
}

func (v *View) running(m config.LabMember) bool {
	in, ok := v.instanceOf(m)
	return ok && in.State == engine.StateRunning
}

// activeLinks counts a lab's links whose two ends are running.
func (v *View) activeLinks(l *config.Lab) int {
	n := 0
	for _, k := range l.Links {
		if v.running(k.A) && v.running(k.B) {
			n++
		}
	}
	return n
}

// memberLine is a lab member with its state glyph and engine.
func (v *View) memberLine(s *theme.Styles, m config.LabMember) string {
	in, ok := v.instanceOf(m)
	if !ok {
		return s.LevelText(theme.LevelWarn).Render(v.icons.missing+" ") + s.Muted.Render(textsafe.Clean(m.Name)+" · missing")
	}
	text, level, muted := views.DisplayState(v.snap, in)
	glyph, _ := v.stateGlyph(text)
	st := s.LevelText(level)
	if muted {
		st = s.Muted
	}
	kind := "container"
	if in.Kind == engine.KindVM {
		kind = "vm"
	}
	return st.Render(glyph+" ") + s.Text.Render(textsafe.Clean(in.Name)) + s.Muted.Render(" · "+kind+" · "+string(in.Engine))
}

// labLines lists the labs a machine is in and its links.
func (v *View) labLines(i *inspect, m config.LabMember) {
	s := i.s
	of := v.labs.Of(m)
	if len(of) == 0 {
		return
	}
	names := make([]string, len(of))
	var links []string
	for j, l := range of {
		names[j] = l.Name
		for _, k := range l.LinksOf(m) {
			o := k.Other(m)
			arrow, st := "↔ ", s.LinkIdle
			if v.running(m) && v.running(o) {
				st = s.LinkActive
			}
			links = append(links, st.Render(arrow)+v.memberLine(s, o))
		}
	}
	i.kv("Labs", s.TreeLab.Render(textsafe.Clean(strings.Join(names, ", "))))
	i.list("Links", links, 4)
}

func (v *View) inspectLab(i *inspect, id int) {
	s := i.s
	l, ok := v.labs.Lab(id)
	if !ok {
		return
	}
	i.title(s.TreeLab.Render(v.icons.lab+" ")+s.Bold.Render("lab "+textsafe.Clean(l.Name)), s.Muted.Render("your lab"))
	count := map[string]int{}
	for _, m := range l.Members {
		count[m.Engine]++
	}
	i.kv("Machines", s.Text.Render(fmt.Sprint(len(l.Members)))+s.Muted.Render(fmt.Sprintf("  %d incus · %d docker", count["incus"], count["docker"])))
	active := v.activeLinks(l)
	lvl := theme.LevelOK
	if active < len(l.Links) {
		lvl = theme.LevelWarn
	}
	i.kv("Links", s.Text.Render(fmt.Sprint(len(l.Links)))+"  "+s.LevelText(lvl).Render(fmt.Sprintf("%d active", active)))
	if i.compact {
		return
	}
	members := make([]string, len(l.Members))
	for j, m := range l.Members {
		members[j] = v.memberLine(s, m)
	}
	i.list("Members", members, 8)
	i.blank()
	i.lines = append(i.lines,
		s.Muted.Render("Links are yours: viewDI cannot see who talks to"),
		s.Muted.Render("whom. A dot runs while both ends are up. L edits."))
}

func (v *View) inspectMissing(i *inspect, it item) {
	s := i.s
	m := it.member
	i.title(s.LevelText(theme.LevelWarn).Render(v.icons.missing+" ")+s.Bold.Render(textsafe.Clean(m.Name)), s.Muted.Render("missing "+m.Engine+" machine"))
	i.kv("Engine", s.Text.Render(m.Engine))
	if m.Project != "" {
		i.kv("Project", s.Text.Render(textsafe.Clean(m.Project)))
	}
	i.kv("Lab", s.TreeLab.Render(textsafe.Clean(v.labName(it.lab))))
	if l, ok := v.labs.Lab(it.lab); ok && !i.compact {
		var links []string
		for _, k := range l.LinksOf(m) {
			links = append(links, s.LinkIdle.Render("↔ ")+v.memberLine(s, k.Other(m)))
		}
		i.list("Links", links, 4)
	}
	i.blank()
	i.lines = append(i.lines, s.Muted.Render("The engine does not report it (deleted, or its engine is"),
		s.Muted.Render("down). It keeps its place and links; L removes it."))
}

// shortDigest abbreviates a digest or fingerprint to 12 hex digits.
func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		d = d[:12]
	}
	return d
}

// inspectApp describes an app in plain words: how it is doing, what is
// wrong, what it is made of and what it uses.
func (v *View) inspectApp(i *inspect, n *tree.Node, it item) {
	s := i.s
	a := it.app
	i.title(s.TreeGroup.Render(v.icons.group+" "+textsafe.Clean(a.Name)), s.Muted.Render("app · "+a.Kind.String()))
	state := map[apps.Health]string{
		apps.OK: "everything that should run, runs", apps.Off: "switched off", apps.Starting: "starting",
		apps.Attention: "needs a look", apps.Down: "something stopped on its own",
	}[a.Health]
	lvl := map[apps.Health]theme.Level{apps.OK: theme.LevelOK, apps.Attention: theme.LevelWarn, apps.Down: theme.LevelErr}[a.Health]
	i.kv("Doing", s.LevelText(lvl).Render(state))
	if a.Reason != "" {
		i.kv("Why", s.Text.Render(textsafe.Clean(a.Reason)))
	}
	i.kv("Machines", s.Text.Render(fmt.Sprint(len(a.Machines)))+s.Muted.Render(fmt.Sprintf(" · %d up · %d off", a.Up, a.Off)))
	i.kv("Made of", s.Muted.Render(textsafe.Clean(strings.Join(a.Names, ", "))))
	if a.Reach != "" {
		i.kv("Reach it", s.Accent.Render(textsafe.Clean(a.Reach)))
	}
	if a.Up > 0 {
		i.kv("Uses", s.Text.Render(fmt.Sprintf("%.0f%% of one CPU · %s", a.CPU, metrics.Bytes(a.MemUsed))))
	}
	_ = n
}
