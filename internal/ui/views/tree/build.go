package tree

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/components/tree"
)

// Grouping is how the tree groups instances under each engine.
type Grouping int

// Groupings, in the order g cycles through them.
const (
	ByNetwork Grouping = iota
	ByProject
	ByCompose
	ByEngine
	ByLab
	numGroupings
)

var groupingIDs = [numGroupings]string{"network", "project", "compose", "engine", "lab"}

// ID is the grouping's persisted name.
func (g Grouping) ID() string { return groupingIDs[g] }

// Label is what the tab info shows.
func (g Grouping) Label() string {
	if g == ByEngine {
		return "engine only"
	}
	return g.ID()
}

func groupingByID(id string) (Grouping, bool) {
	i := slices.Index(groupingIDs[:], id)
	return Grouping(max(i, 0)), i >= 0
}

// kind is a node's type.
type kind int

const (
	kRoot kind = iota
	kEngine
	kNetwork
	kProject
	kCompose
	kOther // "(no network)", "(standalone)"
	kInstance
	kPort
	kMount
	kAlso
	kLab     // a lab the user made
	kMissing // a lab member no engine reports (deleted, or its engine is down)
)

// item is a node's payload. Instances are looked up in the snapshot by key
// when drawn, so a node never holds stale instance data.
type item struct {
	kind      kind
	engine    engine.ID
	name      string // group name
	net       engine.Network
	key       engine.Key // instance, and the instance a leaf belongs to
	secondary bool       // not the instance's primary network (⇄)
	port      engine.Port
	mount     engine.Mount
	also      string           // the other network of an "also on" leaf
	lab       int              // the lab of a lab, or of a member listed under it
	member    config.LabMember // a lab member (instance or missing)
}

func data(n *tree.Node) item {
	it, _ := n.Data.(item)
	return it
}

// rootID is the ID of the single root node.
const rootID = "viewDI"

// noNetwork and standalone name the catch-all groups.
const (
	noNetwork  = "(no network)"
	standalone = "(standalone)"
	notInLab   = "(not in a lab)"
)

// build returns the tree for a snapshot. Node IDs are paths of stable
// parts (engine, group name, instance key, leaf identity), so the cursor
// and the expansion survive refreshes.
func build(snap *store.Snapshot, g Grouping, labs *config.Labs) []*tree.Node {
	root := &tree.Node{ID: rootID, DefaultOpen: true, Data: item{kind: kRoot}}
	if snap == nil {
		return []*tree.Node{root}
	}
	if g == ByLab {
		root.Children = byLab(snap, labs)
		return []*tree.Node{root}
	}
	byEngine := map[engine.ID][]engine.Instance{}
	for _, in := range snap.Instances {
		byEngine[in.Engine] = append(byEngine[in.Engine], in)
	}
	for _, ev := range snap.Engines {
		en := &tree.Node{ID: string(ev.ID), DefaultOpen: true, Data: item{kind: kEngine, engine: ev.ID}}
		ins := byEngine[ev.ID]
		slices.SortFunc(ins, func(a, b engine.Instance) int {
			return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.Key(), b.Key()))
		})
		switch {
		case g == ByNetwork:
			en.Children = byNetwork(en.ID, ev, ins)
		case g == ByProject && ev.ID == engine.Incus:
			en.Children = grouped(en.ID, ev.ID, kProject, ins, func(in engine.Instance) string { return cmp.Or(in.Project, "default") }, "")
		case g == ByCompose && ev.ID == engine.Docker:
			en.Children = grouped(en.ID, ev.ID, kCompose, ins, composeProject, standalone)
		default:
			for _, in := range ins {
				en.Children = append(en.Children, instanceNode(en.ID, in, primary(in)))
			}
		}
		root.Children = append(root.Children, en)
	}
	return []*tree.Node{root}
}

// primary is the network an instance is listed under first: the
// alphabetically first one.
func primary(in engine.Instance) string {
	if len(in.Networks) == 0 {
		return ""
	}
	first := in.Networks[0].Network
	for _, a := range in.Networks[1:] {
		first = min(first, a.Network)
	}
	return first
}

func composeProject(in engine.Instance) string {
	if x, ok := in.Extra.(*engine.DockerExtra); ok && x.ComposeProject != "" {
		return x.ComposeProject
	}
	return in.Labels["com.docker.compose.project"]
}

// byNetwork lists an engine's networks that have members, each with its
// members, then the instances without a network.
func byNetwork(parent string, ev store.EngineView, ins []engine.Instance) []*tree.Node {
	members := map[string][]engine.Instance{}
	var loose []engine.Instance
	for _, in := range ins {
		if len(in.Networks) == 0 {
			loose = append(loose, in)
		}
		seen := map[string]bool{}
		for _, a := range in.Networks {
			if !seen[a.Network] {
				seen[a.Network] = true
				members[a.Network] = append(members[a.Network], in)
			}
		}
	}
	nets := map[string]engine.Network{}
	for _, n := range ev.Networks {
		nets[n.Name] = n
	}
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	slices.Sort(names)
	var out []*tree.Node
	for _, name := range names {
		net, ok := nets[name]
		if !ok {
			net = engine.Network{Name: name, Engine: ev.ID}
		}
		gn := &tree.Node{ID: parent + "/net:" + name, DefaultOpen: true, Data: item{kind: kNetwork, engine: ev.ID, name: name, net: net}}
		for _, in := range members[name] {
			gn.Children = append(gn.Children, instanceNode(gn.ID, in, name))
		}
		out = append(out, gn)
	}
	if len(loose) > 0 {
		gn := &tree.Node{ID: parent + "/" + noNetwork, DefaultOpen: true, Data: item{kind: kOther, engine: ev.ID, name: noNetwork}}
		for _, in := range loose {
			gn.Children = append(gn.Children, instanceNode(gn.ID, in, ""))
		}
		out = append(out, gn)
	}
	return out
}

// grouped groups instances by a key; an empty key goes to the catch-all
// group named other (last), or to "" groups dropped when other is "".
func grouped(parent string, id engine.ID, k kind, ins []engine.Instance, key func(engine.Instance) string, other string) []*tree.Node {
	groups := map[string][]engine.Instance{}
	for _, in := range ins {
		groups[key(in)] = append(groups[key(in)], in)
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		if name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if len(groups[""]) > 0 {
		names = append(names, "")
	}
	prefix := map[kind]string{kProject: "/project:", kCompose: "/compose:"}[k]
	var out []*tree.Node
	for _, name := range names {
		gk, label, gid := k, name, parent+prefix+name
		if name == "" {
			gk, label, gid = kOther, other, parent+"/"+other
		}
		gn := &tree.Node{ID: gid, DefaultOpen: true, Data: item{kind: gk, engine: id, name: label}}
		for _, in := range groups[name] {
			gn.Children = append(gn.Children, instanceNode(gn.ID, in, primary(in)))
		}
		out = append(out, gn)
	}
	return out
}

// instanceNode builds an instance with its leaves: ports, mounts, and the
// other networks it is on. net is the network it is listed under ("" if
// none); listing it elsewhere makes it a secondary occurrence.
func instanceNode(parent string, in engine.Instance, net string) *tree.Node {
	k := in.Key()
	n := &tree.Node{ID: parent + "/" + string(k), Data: item{kind: kInstance, engine: in.Engine, key: k, name: net, secondary: net != "" && net != primary(in)}}
	ids := map[string]int{}
	leaf := func(id string, it item) {
		if c := ids[id]; c > 0 { // duplicates (the same mapping twice) stay distinct
			ids[id]++
			id = fmt.Sprintf("%s#%d", id, c)
		} else {
			ids[id] = 1
		}
		n.Children = append(n.Children, &tree.Node{ID: id, Data: it})
	}
	for _, p := range in.Ports {
		leaf(fmt.Sprintf("%s/port:%s:%d:%d/%s", n.ID, p.HostIP, p.HostPort, p.ContainerPort, p.Protocol), item{kind: kPort, engine: in.Engine, key: k, port: p})
	}
	for _, m := range in.Mounts {
		leaf(n.ID+"/mount:"+m.Destination, item{kind: kMount, engine: in.Engine, key: k, mount: m})
	}
	seen := map[string]bool{net: true}
	others := make([]string, 0, len(in.Networks))
	for _, a := range in.Networks {
		if !seen[a.Network] {
			seen[a.Network] = true
			others = append(others, a.Network)
		}
	}
	slices.Sort(others)
	for _, o := range others {
		n.Children = append(n.Children, &tree.Node{ID: n.ID + "/also:" + o, Data: item{kind: kAlso, engine: in.Engine, key: k, also: o}})
	}
	return n
}

// memberOf is the lab identity of an instance.
func memberOf(in engine.Instance) config.LabMember {
	return config.LabMember{Engine: string(in.Engine), Project: in.Project, Name: in.Name}
}

// labNodeID is the node ID of a lab: by its ID, so renames keep folds.
func labNodeID(id int) string { return fmt.Sprintf("lab:%d", id) }

// byLab lists each lab with its members (machines the engines report, and
// "missing" ones they do not), then the machines in no lab.
func byLab(snap *store.Snapshot, labs *config.Labs) []*tree.Node {
	ins := map[config.LabMember]engine.Instance{}
	all := slices.Clone(snap.Instances)
	slices.SortFunc(all, func(a, b engine.Instance) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.Key(), b.Key()))
	})
	for _, in := range all {
		if _, dup := ins[memberOf(in)]; !dup {
			ins[memberOf(in)] = in
		}
	}
	inLab := map[config.LabMember]bool{}
	var out []*tree.Node
	if labs != nil {
		for _, l := range labs.Labs {
			ln := &tree.Node{ID: labNodeID(l.ID), DefaultOpen: true, Data: item{kind: kLab, lab: l.ID, name: l.Name}}
			members := slices.Clone(l.Members)
			slices.SortFunc(members, func(a, b config.LabMember) int {
				return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.String(), b.String()))
			})
			for _, m := range members {
				inLab[m] = true
				in, ok := ins[m]
				if !ok {
					ln.Children = append(ln.Children, &tree.Node{
						ID:   ln.ID + "/missing:" + m.String(),
						Data: item{kind: kMissing, engine: engine.ID(m.Engine), lab: l.ID, member: m, name: m.Name},
					})
					continue
				}
				n := instanceNode(ln.ID, in, primary(in))
				it := data(n)
				it.lab, it.member = l.ID, m
				n.Data = it
				ln.Children = append(ln.Children, n)
			}
			out = append(out, ln)
		}
	}
	rest := &tree.Node{ID: notInLab, Data: item{kind: kOther, name: notInLab}}
	for _, in := range all {
		if !inLab[memberOf(in)] {
			rest.Children = append(rest.Children, instanceNode(rest.ID, in, primary(in)))
		}
	}
	if len(rest.Children) > 0 {
		out = append(out, rest)
	}
	return out
}
