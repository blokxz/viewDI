package tree

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/components/tree"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
	"github.com/blokxz/viewDI/internal/ui/views"
)

var now = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func dockerCtr(name string, st engine.State, compose string, nets ...string) engine.Instance {
	in := engine.Instance{Engine: engine.Docker, ID: "id-" + name, Name: name, Image: name + ":1", State: st, Kind: engine.KindContainer}
	for i, n := range nets {
		in.Networks = append(in.Networks, engine.Attachment{Network: n, IPv4: "172.20.0." + string(rune('2'+i))})
	}
	in.Extra = &engine.DockerExtra{ComposeProject: compose}
	return in
}

func incusInst(project, name string, kind engine.Kind, net string) engine.Instance {
	in := engine.Instance{Engine: engine.Incus, ID: project + "/" + name, Name: name, Project: project, Image: "images:debian/12", State: engine.StateRunning, Kind: kind}
	if net != "" {
		in.Networks = []engine.Attachment{{Network: net, IPv4: "10.10.0.21"}}
	}
	in.Extra = &engine.IncusExtra{Profiles: []string{"default"}}
	return in
}

func world() []store.Change {
	api := dockerCtr("web-api", engine.StateRunning, "web", "web_default", "monitoring")
	api.Ports = []engine.Port{{HostIP: "0.0.0.0", HostPort: 8080, ContainerPort: 8080, Protocol: "tcp"}, {HostIP: "::", HostPort: 8080, ContainerPort: 8080, Protocol: "tcp"}}
	api.Mounts = []engine.Mount{{Type: "bind", Source: "./config", Destination: "/app/config", ReadOnly: true}}
	return []store.Change{
		store.Synced{Engine: engine.Docker, Info: engine.Info{Version: "29.0.1"}, Instances: []engine.Instance{
			api,
			dockerCtr("web-db", engine.StateRunning, "web", "web_default"),
			dockerCtr("grafana", engine.StateRunning, "", "monitoring"),
			dockerCtr("old", engine.StateStopped, ""),
		}, Networks: []engine.Network{
			{Name: "web_default", Driver: "bridge", Subnets: []string{"172.20.0.0/16"}},
			{Name: "monitoring", Driver: "bridge", Subnets: []string{"172.21.0.0/16"}},
			{Name: "host", Driver: "host"},
		}},
		store.Synced{Engine: engine.Incus, Info: engine.Info{Version: "6.0.5"}, Instances: []engine.Instance{
			incusInst("default", "web-lab", engine.KindContainer, "incusbr0"),
			incusInst("lab", "k3s", engine.KindVM, "incusbr0"),
		}, Networks: []engine.Network{{Name: "incusbr0", Driver: "bridge", Managed: true}}},
	}
}

func snapshotOf(changes ...store.Change) *store.Snapshot {
	s := store.New(engine.IDs...)
	s.Apply(changes...)
	return s.Snapshot()
}

func newView(t *testing.T, opts Options) *View {
	t.Helper()
	v := New(keys.MustDefault(), opts)
	v.Update(views.DataMsg{Snapshot: snapshotOf(world()...)})
	v.Update(views.ResizeMsg{Width: 120, Height: 30})
	return v
}

func press(v *View, ks ...string) []tea.Msg {
	var out []tea.Msg
	for _, k := range ks {
		_, cmd, _ := v.HandleKey(uitest.Key(k), 0)
		out = append(out, uitest.Collect(cmd)...)
	}
	return out
}

// labels returns the visible rows as "depth:text" for compact assertions.
func labels(v *View) []string {
	s := theme.NewStyles(theme.Fallback(), 1)
	var out []string
	for _, r := range v.t().Rows() {
		var b strings.Builder
		for _, sg := range v.label(s, r).Left {
			b.WriteString(sg.Text)
		}
		out = append(out, strings.Repeat(" ", r.Depth)+b.String())
	}
	return out
}

func TestNetworkGrouping(t *testing.T) {
	v := newView(t, Options{})
	want := []string{
		"viewDI",
		" ▣ Docker 29.0.1",
		"  ◆ monitoring  172.21.0.0/16",
		"   ● grafana",
		"   ● web-api",
		"  ◆ web_default  172.20.0.0/16",
		"   ● web-api  ⇄",
		"   ● web-db",
		"  ◇ (no network)",
		"   ■ old  stopped",
		" ▦ Incus 6.0.5 · all projects",
		"  ◆ incusbr0  managed",
		"   ● k3s  vm  [lab]",
		"   ● web-lab",
	}
	if got := labels(v); !slices.Equal(got, want) {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestOtherGroupings(t *testing.T) {
	v := newView(t, Options{})
	press(v, "g")
	if got := labels(v); !slices.Contains(got, "  ◆ project lab") || !slices.Contains(got, "  ● web-api") {
		t.Fatalf("project grouping:\n%s", strings.Join(got, "\n"))
	}
	press(v, "g")
	got := labels(v)
	if !slices.Contains(got, "  ◆ web") || !slices.Contains(got, "  ◇ (standalone)") || !slices.Contains(got, "  ● k3s  vm  [lab]") {
		t.Fatalf("compose grouping:\n%s", strings.Join(got, "\n"))
	}
	press(v, "g")
	if v.Grouping() != ByEngine || len(labels(v)) != 9 {
		t.Fatalf("engine grouping %v:\n%s", v.Grouping(), strings.Join(labels(v), "\n"))
	}
	press(v, "g")
	if v.Grouping() != ByNetwork {
		t.Fatal("grouping did not wrap")
	}
}

func TestLeavesAndSelection(t *testing.T) {
	v := newView(t, Options{})
	v.t().Select("docker/net:web_default/docker/id-web-api")
	press(v, "right")
	got := labels(v)[7:11]
	want := []string{
		"    ⇢ 0.0.0.0:8080 → 8080/tcp",
		"    ⇢ [::]:8080 → 8080/tcp",
		"    ▤ ./config → /app/config  ro",
		"    ⇄ also on: monitoring",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("leaves %q", got)
	}
	press(v, "down", "down", "down")
	if in, ok := v.Selected(); !ok || in.Name != "web-api" {
		t.Fatalf("a leaf selects its machine: %v %v", in.Name, ok)
	}
	if b := v.breadcrumb(); b != "Docker › web_default › web-api › /app/config" {
		t.Fatalf("breadcrumb %q", b)
	}
	press(v, "left") // leaf → its machine
	press(v, "left") // closes it
	press(v, "left") // → its network
	if _, ok := v.Selected(); ok || v.Cursor() != "docker/net:web_default" {
		t.Fatalf("cursor %q", v.Cursor())
	}
}

func TestNextOccurrenceAndGroupingKeepTheMachine(t *testing.T) {
	v := newView(t, Options{})
	v.t().Select("docker/net:web_default/docker/id-web-api")
	press(v, "J")
	if v.Cursor() != "docker/net:monitoring/docker/id-web-api" {
		t.Fatalf("J went to %q", v.Cursor())
	}
	press(v, "J")
	if v.Cursor() != "docker/net:web_default/docker/id-web-api" {
		t.Fatalf("J did not wrap: %q", v.Cursor())
	}
	press(v, "g", "g") // compose: the same machine stays under the cursor
	if in, ok := v.Selected(); !ok || in.Name != "web-api" || v.Cursor() != "docker/compose:web/docker/id-web-api" {
		t.Fatalf("cursor %q after g", v.Cursor())
	}
	press(v, "up", "up")
	press(v, "g") // engine only, from a group: the engine
	if v.Cursor() != "docker" {
		t.Fatalf("cursor %q after g from a group", v.Cursor())
	}
}

func TestFilter(t *testing.T) {
	v := newView(t, Options{})
	press(v, "down")
	v.HandleAction(keys.ActFilter)
	if !v.Inputting() {
		t.Fatal("/ did not open the prompt")
	}
	press(v, "1", "0", ".", "1", "0")
	got := labels(v)
	want := []string{"viewDI", " ▦ Incus 6.0.5 · all projects", "  ◆ incusbr0  managed", "   ● k3s  vm  [lab]", "   ● web-lab"}
	if !slices.Equal(got, want) || len(v.t().Matches()) != 2 {
		t.Fatalf("filtered by IP:\n%s", strings.Join(got, "\n"))
	}
	if in, _ := v.Selected(); in.Name != "k3s" {
		t.Fatalf("cursor on %q, not the first match", in.Name)
	}
	press(v, "enter", "ctrl+n")
	if in, _ := v.Selected(); in.Name != "web-lab" {
		t.Fatalf("ctrl+n went to %q", in.Name)
	}
	// Live data keeps the filter and the cursor.
	v.Update(views.DataMsg{Snapshot: snapshotOf(world()...)})
	if in, _ := v.Selected(); in.Name != "web-lab" || len(labels(v)) != 5 {
		t.Fatalf("refresh lost the filter: %v", labels(v))
	}
	if _, _, handled := v.HandleAction(keys.ActBack); !handled || v.Filter() != "" || len(labels(v)) != 14 {
		t.Fatalf("esc did not clear the filter: %v", labels(v))
	}
	if in, _ := v.Selected(); in.Name != "web-lab" {
		t.Fatalf("cursor %q after clearing", in.Name)
	}
}

func TestCursorSurvivesStateChangesAndRemoval(t *testing.T) {
	v := newView(t, Options{})
	v.t().Select("docker/net:web_default/docker/id-web-db")
	ch := world()
	sy := ch[0].(store.Synced)
	sy.Instances[1].State = engine.StateStopped
	v.Update(views.DataMsg{Snapshot: snapshotOf(sy, ch[1])})
	if v.Cursor() != "docker/net:web_default/docker/id-web-db" {
		t.Fatalf("a state change moved the cursor to %q", v.Cursor())
	}
	sy.Instances = slices.Delete(slices.Clone(sy.Instances), 1, 2) // web-db removed
	v.Update(views.DataMsg{Snapshot: snapshotOf(sy, ch[1])})
	if v.Cursor() != "docker/net:web_default/docker/id-web-api" {
		t.Fatalf("after removal the cursor is on %q, not the sibling", v.Cursor())
	}
}

func TestExpansionIsPersisted(t *testing.T) {
	v := newView(t, Options{})
	v.t().Select("docker/net:monitoring")
	msgs := press(v, "space")
	if len(msgs) != 1 {
		t.Fatalf("messages %v", msgs)
	}
	st := msgs[0].(StateChangedMsg).State
	if st.Grouping != "network" || !slices.Equal(st.Toggled["network"], []string{"docker/net:monitoring"}) {
		t.Fatalf("state %+v", st)
	}
	msgs = press(v, "g")
	if st := msgs[len(msgs)-1].(StateChangedMsg).State; st.Grouping != "project" || len(st.Toggled["network"]) != 1 {
		t.Fatalf("state after g %+v", st)
	}
	// A new view restores both.
	v2 := newView(t, Options{State: config.TreeState{Grouping: "network", Toggled: map[string][]string{"network": {"docker/net:monitoring"}}}})
	if got := labels(v2); slices.Contains(got, "   ● grafana") {
		t.Fatalf("restored expansion ignored:\n%s", strings.Join(got, "\n"))
	}
	if press(v2, "down"); len(press(v2, "down")) != 0 {
		t.Fatal("moving the cursor saved state")
	}
}

func TestInspectorDebounce(t *testing.T) {
	v := newView(t, Options{InspectDelay: 80 * time.Millisecond})
	_, cmd, _ := v.HandleKey(uitest.Key("down"), 0)
	if v.inspected != rootID || cmd == nil {
		t.Fatalf("inspector followed at once: %q", v.inspected)
	}
	_, cmd2, _ := v.HandleKey(uitest.Key("down"), 0)
	v.Update(inspectMsg{seq: v.seq - 1}) // the first, superseded wait
	if v.inspected != rootID {
		t.Fatal("a stale debounce moved the inspector")
	}
	_ = cmd
	for _, m := range uitest.Collect(cmd2) {
		v.Update(m)
	}
	if v.inspected != v.Cursor() {
		t.Fatalf("inspector on %q, cursor on %q", v.inspected, v.Cursor())
	}
}

func TestRenderLayouts(t *testing.T) {
	s := theme.NewStyles(theme.Fallback(), 1)
	for _, sz := range [][2]int{{120, 30}, {80, 21}, {70, 10}} {
		v := newView(t, Options{})
		v.Update(views.ResizeMsg{Width: sz[0], Height: sz[1]})
		v.t().Select("docker/net:web_default/docker/id-web-api")
		out := v.Render(views.Context{Styles: s, Keys: keys.MustDefault(), Width: sz[0], Height: sz[1], Mode: layout.ModeFor(sz[0], sz[1]), Now: now})
		lines := strings.Split(out, "\n")
		if len(lines) != sz[1] {
			t.Fatalf("%v: %d lines", sz, len(lines))
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w != sz[0] {
				t.Fatalf("%v: line %q is %d wide", sz, ansi.Strip(l), w)
			}
		}
		plain := ansi.Strip(out)
		if !strings.Contains(plain, "Docker › web_default › web-api") {
			t.Fatalf("%v: no breadcrumb:\n%s", sz, plain)
		}
		if inspector := strings.Contains(plain, "Inspector"); inspector != (sz[1] >= 14) {
			t.Fatalf("%v: inspector shown = %v:\n%s", sz, inspector, plain)
		}
	}
}

func TestASCIIIcons(t *testing.T) {
	v := newView(t, Options{ASCII: true})
	s := theme.NewStyles(theme.Fallback(), 1)
	out := ansi.Strip(v.Render(views.Context{Styles: s, Keys: keys.MustDefault(), Width: 120, Height: 30, Now: now}))
	for _, want := range []string{"|-- [D] Docker", "`-- [I] Incus", "# monitoring", "* web-api  <>", "- old  stopped", "4 up 3 down 1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	for _, r := range out {
		if r > 0x7f {
			t.Fatalf("non-ASCII %q in:\n%s", r, out)
		}
	}
}

func TestTreeNodeIDsAreUnique(t *testing.T) {
	for g := range numGroupings {
		seen := map[string]bool{}
		var walk func(ns []*tree.Node)
		walk = func(ns []*tree.Node) {
			for _, n := range ns {
				if seen[n.ID] {
					t.Fatalf("%s: duplicate ID %q", g.ID(), n.ID)
				}
				seen[n.ID] = true
				walk(n.Children)
			}
		}
		walk(build(snapshotOf(world()...), g))
	}
}

func TestClearingTheFilterKeepsTheMatchInView(t *testing.T) {
	v := newView(t, Options{})
	v.t().Select("docker")
	press(v, "left") // Docker folded
	v.HandleAction(keys.ActFilter)
	press(v, "g", "r", "a", "enter")
	if in, _ := v.Selected(); in.Name != "grafana" {
		t.Fatalf("cursor on %q", in.Name)
	}
	v.HandleAction(keys.ActBack)
	if in, ok := v.Selected(); !ok || in.Name != "grafana" {
		t.Fatalf("after clearing, cursor on %q", v.Cursor())
	}
}
