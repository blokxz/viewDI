package tree

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/form"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/picker"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// KindPrefix starts the kind of every picker and form the lab menu opens;
// the app hands their results back to this view.
const KindPrefix = "tree-lab-"

const (
	pickMenu   = KindPrefix + "menu"
	pickAdd    = KindPrefix + "add"
	pickLink   = KindPrefix + "link"
	pickUnlink = KindPrefix + "unlink"
	formNew    = KindPrefix + "new"
	formRename = KindPrefix + "rename"
)

// Lab menu entries.
const (
	menuNew    = "new"
	menuAdd    = "add"
	menuLink   = "link"
	menuUnlink = "unlink"
	menuRemove = "remove"
	menuRename = "rename"
	menuDelete = "delete"
)

// labDeleteMsg is sent when the user confirms deleting a lab.
type labDeleteMsg struct{ lab int }

// labTarget is what the lab menu acts on: a machine (or a missing member)
// and the lab it is seen in, either may be absent.
type labTarget struct {
	member  config.LabMember
	has     bool // member is set
	display string
	lab     int // 0: none
}

// target resolves the node under the cursor: a lab, a lab member, or a
// machine (whose lab is the one it is listed under, or its only lab).
func (v *View) target() labTarget {
	n, ok := v.t().Current()
	if !ok {
		return labTarget{}
	}
	it := data(n)
	var tg labTarget
	switch it.kind {
	case kLab:
		tg.lab = it.lab
		return tg
	case kMissing:
		return labTarget{member: it.member, has: true, display: it.member.Name, lab: it.lab}
	case kInstance, kPort, kMount, kAlso:
		in, ok := v.snap.Lookup(it.key)
		if !ok {
			return tg
		}
		tg.member, tg.has, tg.display = memberOf(in), true, in.Name
		if p := v.t().Parent(n.ID); it.kind != kInstance && p != nil {
			it = data(p) // a leaf: its machine's lab
		}
		tg.lab = it.lab
		if tg.lab == 0 {
			if of := v.labs.Of(tg.member); len(of) == 1 {
				tg.lab = of[0].ID
			}
		}
	}
	return tg
}

func (v *View) labName(id int) string {
	if l, ok := v.labs.Lab(id); ok {
		return l.Name
	}
	return ""
}

// labMenu opens the lab menu for the node under the cursor.
func (v *View) labMenu() tea.Cmd {
	tg := v.target()
	v.menu = tg
	name := textsafe.Clean(tg.display)
	items := []picker.Item{{ID: menuNew, Label: "New lab…"}}
	if tg.has {
		items[0].Label = "New lab with " + name + "…"
		others := 0
		for _, l := range v.labs.Labs {
			if !l.HasMember(tg.member) {
				others++
			}
		}
		if others > 0 {
			items = append(items, picker.Item{ID: menuAdd, Label: "Add " + name + " to a lab…"})
		}
	}
	if l, ok := v.labs.Lab(tg.lab); ok {
		lab := textsafe.Clean(l.Name)
		if tg.has {
			items = append(items, picker.Item{ID: menuLink, Label: "Link " + name + " with…", Detail: "in " + lab})
			if len(l.LinksOf(tg.member)) > 0 {
				items = append(items, picker.Item{ID: menuUnlink, Label: "Remove a link…", Detail: "in " + lab})
			}
			if l.HasMember(tg.member) {
				items = append(items, picker.Item{ID: menuRemove, Label: "Remove " + name + " from " + lab})
			}
		}
		items = append(items,
			picker.Item{ID: menuRename, Label: "Rename lab " + lab + "…"},
			picker.Item{ID: menuDelete, Label: "Delete lab " + lab + "…"},
		)
	}
	return modal.Open(picker.New(v.keys, pickMenu, "Labs", items, ""))
}

// labPicked handles the lab pickers' choices.
func (v *View) labPicked(msg picker.SelectedMsg) tea.Cmd {
	tg := v.menu
	switch msg.Kind {
	case pickMenu:
		return v.menuChosen(msg.ID)
	case pickAdd:
		var id int
		if _, err := fmt.Sscan(msg.ID, &id); err != nil || !tg.has {
			return nil
		}
		if _, err := v.labs.AddMember(id, tg.member); err != nil {
			return toast.Show(theme.LevelWarn, err.Error())
		}
		return v.labsChanged(fmt.Sprintf("%s added to %s", tg.display, v.labName(id)))
	case pickLink:
		other, ok := v.pick[msg.ID]
		if !ok || !tg.has {
			return nil
		}
		added, err := v.labs.Link(tg.lab, tg.member, other)
		switch {
		case err != nil:
			return toast.Show(theme.LevelWarn, err.Error())
		case !added:
			return toast.Show(theme.LevelInfo, tg.display+" and "+other.Name+" are already linked")
		}
		return v.labsChanged(fmt.Sprintf("Linked %s ↔ %s", tg.display, other.Name))
	case pickUnlink:
		other, ok := v.pick[msg.ID]
		if !ok || !tg.has {
			return nil
		}
		v.labs.Unlink(tg.lab, tg.member, other)
		return v.labsChanged(fmt.Sprintf("Link %s ↔ %s removed", tg.display, other.Name))
	}
	return nil
}

func (v *View) menuChosen(id string) tea.Cmd {
	tg := v.menu
	switch id {
	case menuNew:
		f := form.Field{ID: "name", Label: "Name", Required: true, Placeholder: "e.g. perimeter", Validate: config.ValidLabName}
		intro := "A lab groups machines from both engines. Links between them are yours to declare."
		return modal.Open(form.New(v.keys, formNew, "New lab", intro, "Create", []form.Field{f}))
	case menuAdd:
		var items []picker.Item
		for _, l := range v.labs.Labs {
			if !l.HasMember(tg.member) {
				items = append(items, picker.Item{ID: fmt.Sprint(l.ID), Label: textsafe.Clean(l.Name), Detail: fmt.Sprintf("%d machines", len(l.Members))})
			}
		}
		return modal.Open(picker.New(v.keys, pickAdd, "Add "+textsafe.Clean(tg.display)+" to", items, ""))
	case menuLink:
		return v.linkPicker()
	case menuUnlink:
		l, ok := v.labs.Lab(tg.lab)
		if !ok {
			return nil
		}
		v.pick = map[string]config.LabMember{}
		var items []picker.Item
		for _, k := range l.LinksOf(tg.member) {
			o := k.Other(tg.member)
			v.pick[o.String()] = o
			items = append(items, picker.Item{ID: o.String(), Label: textsafe.Clean(o.Name), Detail: o.Engine})
		}
		return modal.Open(picker.New(v.keys, pickUnlink, "Remove the link of "+textsafe.Clean(tg.display)+" with", items, ""))
	case menuRemove:
		name := v.labName(tg.lab)
		v.labs.RemoveMember(tg.lab, tg.member)
		return v.labsChanged(fmt.Sprintf("%s removed from %s", tg.display, name))
	case menuRename:
		f := form.Field{ID: "name", Label: "Name", Required: true, Value: v.labName(tg.lab), Validate: config.ValidLabName}
		return modal.Open(form.New(v.keys, formRename, "Rename lab", "", "Rename", []form.Field{f}).WithContext(tg.lab))
	case menuDelete:
		l, ok := v.labs.Lab(tg.lab)
		if !ok {
			return nil
		}
		body := fmt.Sprintf("Delete lab %s?\n\nIts %d machines are not touched; its %d links are forgotten.", textsafe.Clean(l.Name), len(l.Members), len(l.Links))
		return modal.Open(modal.NewConfirm(v.keys, "Delete lab", body, "Delete", true, labDeleteMsg{lab: l.ID}))
	}
	return nil
}

// linkPicker lists the machines of both engines (and the lab's missing
// members) the target can link with.
func (v *View) linkPicker() tea.Cmd {
	tg := v.menu
	l, ok := v.labs.Lab(tg.lab)
	if !ok || v.snap == nil {
		return nil
	}
	v.pick = map[string]config.LabMember{}
	var items []picker.Item
	add := func(m config.LabMember, detail string) {
		if m == tg.member {
			return
		}
		if _, dup := v.pick[m.String()]; dup {
			return
		}
		v.pick[m.String()] = m
		if slices.ContainsFunc(l.Links, func(k config.LabLink) bool { return k.Has(tg.member) && k.Has(m) }) {
			detail += " · linked"
		}
		items = append(items, picker.Item{ID: m.String(), Label: textsafe.Clean(m.Name), Detail: detail})
	}
	ins := slices.Clone(v.snap.Instances)
	slices.SortFunc(ins, func(a, b engine.Instance) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.Key(), b.Key()))
	})
	for _, in := range ins {
		kind := "container"
		if in.Kind == engine.KindVM {
			kind = "vm"
		}
		d := string(in.Engine) + " " + kind
		if in.Project != "" && in.Project != "default" {
			d += " [" + in.Project + "]"
		}
		add(memberOf(in), d)
	}
	for _, m := range l.Members {
		add(m, m.Engine+" · missing")
	}
	return modal.Open(picker.New(v.keys, pickLink, "Link "+textsafe.Clean(tg.display)+" with", items, ""))
}

// labSubmitted handles the new-lab and rename forms.
func (v *View) labSubmitted(msg form.SubmittedMsg) tea.Cmd {
	name := strings.TrimSpace(msg.Values["name"])
	switch msg.Kind {
	case formNew:
		id, err := v.labs.Add(name)
		if err != nil {
			return toast.Show(theme.LevelWarn, err.Error())
		}
		text := "Lab " + name + " created"
		if tg := v.menu; tg.has {
			if _, err := v.labs.AddMember(id, tg.member); err == nil {
				text += " with " + tg.display
			}
		}
		cmd := v.labsChanged(text)
		if v.grouping == ByLab {
			v.t().Select(labNodeID(id))
			v.inspected = v.Cursor()
		}
		return cmd
	case formRename:
		id, _ := msg.Context.(int)
		old := v.labName(id)
		if err := v.labs.Rename(id, name); err != nil {
			return toast.Show(theme.LevelWarn, err.Error())
		}
		return v.labsChanged("Lab " + old + " renamed to " + name)
	}
	return nil
}

func (v *View) labDelete(id int) tea.Cmd {
	name := v.labName(id)
	if name == "" {
		return nil
	}
	v.labs.Delete(id)
	return v.labsChanged("Lab " + name + " deleted")
}

// labsChanged rebuilds the tree after an edit, and asks the app to save
// the labs.
func (v *View) labsChanged(text string) tea.Cmd {
	v.rebuild()
	ls := v.labs.Clone()
	return tea.Batch(
		func() tea.Msg { return LabsChangedMsg{Labs: ls} },
		toast.Show(theme.LevelOK, textsafe.Clean(text)),
	)
}
