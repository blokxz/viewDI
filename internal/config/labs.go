package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// LabsVersion is bumped when the labs.json schema changes incompatibly.
const LabsVersion = 1

// Limits on labs.json: anything past them is dropped on load and refused
// by the editing operations.
const (
	MaxLabs       = 64
	MaxLabMembers = 256 // per lab
	MaxLabLinks   = 512 // per lab
	MaxLabName    = 40  // runes
	maxLabsSize   = 4 << 20
)

// Labs are the user's labs: blocks of machines from both engines, grouped
// and linked by hand. viewDI cannot see which machine talks to which, so
// links are only what the user declared. Labs never touch the daemons.
type Labs struct {
	Version int   `json:"version"`
	Labs    []Lab `json:"labs"`
}

// Lab is one block of machines and the links between them.
type Lab struct {
	ID      int         `json:"id"` // stable across renames
	Name    string      `json:"name"`
	Members []LabMember `json:"members"`
	Links   []LabLink   `json:"links,omitempty"`
}

// LabMember identifies a machine by engine, project and name, so a
// recreated container stays in its lab, and a deleted one stays listed as
// missing.
type LabMember struct {
	Engine  string `json:"engine"`
	Project string `json:"project,omitempty"` // Incus project; "" for Docker
	Name    string `json:"name"`
}

// String is the member's identity, e.g. "incus/lab/opnsense".
func (m LabMember) String() string { return m.Engine + "/" + m.Project + "/" + m.Name }

// LabLink is a link the user declared between two members of a lab.
type LabLink struct {
	A LabMember `json:"a"`
	B LabMember `json:"b"`
}

// Has reports whether the link touches m.
func (l LabLink) Has(m LabMember) bool { return l.A == m || l.B == m }

// Other returns the end of the link that is not m.
func (l LabLink) Other(m LabMember) LabMember {
	if l.A == m {
		return l.B
	}
	return l.A
}

func (l LabLink) same(a, b LabMember) bool { return (l.A == a && l.B == b) || (l.A == b && l.B == a) }

// HasMember reports whether m is in the lab.
func (l *Lab) HasMember(m LabMember) bool { return slices.Contains(l.Members, m) }

// LinksOf returns the lab's links that touch m.
func (l *Lab) LinksOf(m LabMember) []LabLink {
	var out []LabLink
	for _, k := range l.Links {
		if k.Has(m) {
			out = append(out, k)
		}
	}
	return out
}

// ValidLabName checks a lab name typed by the user.
func ValidLabName(name string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return errors.New("a lab needs a name")
	case utf8.RuneCountInString(name) > MaxLabName:
		return fmt.Errorf("at most %d characters", MaxLabName)
	case strings.ContainsFunc(name, func(r rune) bool { return unicode.IsControl(r) || r == utf8.RuneError }):
		return errors.New("no control characters")
	}
	return nil
}

// Clone returns a deep copy: the Tree view edits its own copy and hands
// the app another one to save.
func (ls Labs) Clone() Labs {
	out := Labs{Version: ls.Version, Labs: make([]Lab, len(ls.Labs))}
	for i, l := range ls.Labs {
		out.Labs[i] = Lab{ID: l.ID, Name: l.Name, Members: slices.Clone(l.Members), Links: slices.Clone(l.Links)}
	}
	return out
}

// Lab returns the lab with id.
func (ls *Labs) Lab(id int) (*Lab, bool) {
	for i := range ls.Labs {
		if ls.Labs[i].ID == id {
			return &ls.Labs[i], true
		}
	}
	return nil, false
}

// Of returns the labs m is a member of.
func (ls *Labs) Of(m LabMember) []*Lab {
	var out []*Lab
	for i := range ls.Labs {
		if ls.Labs[i].HasMember(m) {
			out = append(out, &ls.Labs[i])
		}
	}
	return out
}

// Add creates a lab and returns its ID.
func (ls *Labs) Add(name string) (int, error) {
	if err := ValidLabName(name); err != nil {
		return 0, err
	}
	if len(ls.Labs) >= MaxLabs {
		return 0, fmt.Errorf("at most %d labs", MaxLabs)
	}
	id := 1
	for _, l := range ls.Labs {
		id = max(id, l.ID+1)
	}
	ls.Labs = append(ls.Labs, Lab{ID: id, Name: strings.TrimSpace(name)})
	return id, nil
}

// Rename renames a lab.
func (ls *Labs) Rename(id int, name string) error {
	if err := ValidLabName(name); err != nil {
		return err
	}
	l, ok := ls.Lab(id)
	if !ok {
		return errors.New("no such lab")
	}
	l.Name = strings.TrimSpace(name)
	return nil
}

// Delete removes a lab.
func (ls *Labs) Delete(id int) {
	ls.Labs = slices.DeleteFunc(ls.Labs, func(l Lab) bool { return l.ID == id })
}

// AddMember puts m in a lab; it reports false if it already was.
func (ls *Labs) AddMember(id int, m LabMember) (bool, error) {
	l, ok := ls.Lab(id)
	if !ok {
		return false, errors.New("no such lab")
	}
	if l.HasMember(m) {
		return false, nil
	}
	if len(l.Members) >= MaxLabMembers {
		return false, fmt.Errorf("at most %d machines per lab", MaxLabMembers)
	}
	l.Members = append(l.Members, m)
	return true, nil
}

// RemoveMember takes m out of a lab with its links.
func (ls *Labs) RemoveMember(id int, m LabMember) {
	l, ok := ls.Lab(id)
	if !ok {
		return
	}
	l.Members = slices.DeleteFunc(l.Members, func(x LabMember) bool { return x == m })
	l.Links = slices.DeleteFunc(l.Links, func(k LabLink) bool { return k.Has(m) })
}

// Link declares a link between a and b in a lab, adding either to the lab
// if needed. It reports false if the link already existed.
func (ls *Labs) Link(id int, a, b LabMember) (bool, error) {
	if a == b {
		return false, errors.New("a machine cannot link to itself")
	}
	l, ok := ls.Lab(id)
	if !ok {
		return false, errors.New("no such lab")
	}
	if slices.ContainsFunc(l.Links, func(k LabLink) bool { return k.same(a, b) }) {
		return false, nil
	}
	if len(l.Links) >= MaxLabLinks {
		return false, fmt.Errorf("at most %d links per lab", MaxLabLinks)
	}
	need := 0
	for _, m := range []LabMember{a, b} {
		if !l.HasMember(m) {
			need++
		}
	}
	if len(l.Members)+need > MaxLabMembers {
		return false, fmt.Errorf("at most %d machines per lab", MaxLabMembers)
	}
	for _, m := range []LabMember{a, b} {
		if !l.HasMember(m) {
			l.Members = append(l.Members, m)
		}
	}
	l.Links = append(l.Links, LabLink{A: a, B: b})
	return true, nil
}

// Unlink removes the link between a and b from a lab.
func (ls *Labs) Unlink(id int, a, b LabMember) {
	if l, ok := ls.Lab(id); ok {
		l.Links = slices.DeleteFunc(l.Links, func(k LabLink) bool { return k.same(a, b) })
	}
}

// normalize repairs a hand-edited or damaged file: unique positive IDs,
// valid names, no duplicate members, links only between two distinct
// members, the limits. It returns what it had to fix.
func (ls *Labs) normalize() []string {
	var fixed []string
	if len(ls.Labs) > MaxLabs {
		ls.Labs = ls.Labs[:MaxLabs]
		fixed = append(fixed, fmt.Sprintf("only the first %d labs are kept", MaxLabs))
	}
	seen := map[int]bool{}
	next := 1
	for _, l := range ls.Labs {
		next = max(next, l.ID+1)
	}
	for i := range ls.Labs {
		l := &ls.Labs[i]
		if l.ID <= 0 || seen[l.ID] {
			l.ID = next
			next++
		}
		seen[l.ID] = true
		if ValidLabName(l.Name) != nil {
			name := strings.Map(func(r rune) rune {
				if unicode.IsControl(r) || r == utf8.RuneError {
					return -1
				}
				return r
			}, strings.TrimSpace(l.Name))
			if r := []rune(name); len(r) > MaxLabName {
				name = string(r[:MaxLabName])
			}
			if name == "" {
				name = fmt.Sprintf("lab %d", l.ID)
			}
			fixed = append(fixed, fmt.Sprintf("lab %d renamed to %q", l.ID, name))
			l.Name = name
		}
		members := l.Members[:0]
		for _, m := range l.Members {
			if m.Engine == "" || m.Name == "" || slices.Contains(members, m) {
				continue
			}
			members = append(members, m)
		}
		if len(members) > MaxLabMembers {
			members = members[:MaxLabMembers]
			fixed = append(fixed, fmt.Sprintf("lab %q keeps its first %d machines", l.Name, MaxLabMembers))
		}
		l.Members = members
		links := l.Links[:0]
		for _, k := range l.Links {
			if k.A == k.B || !l.HasMember(k.A) || !l.HasMember(k.B) ||
				slices.ContainsFunc(links, func(o LabLink) bool { return o.same(k.A, k.B) }) {
				continue
			}
			links = append(links, k)
		}
		if len(links) > MaxLabLinks {
			links = links[:MaxLabLinks]
			fixed = append(fixed, fmt.Sprintf("lab %q keeps its first %d links", l.Name, MaxLabLinks))
		}
		l.Links = links
	}
	return fixed
}

// LoadLabs reads labs.json. A missing file yields no labs. A corrupt file
// is renamed to labs.json.bak and a warning is returned, like state.json.
func LoadLabs(path string) (Labs, []Warning) {
	empty := Labs{Version: LabsVersion}
	data, err := ReadCapped(path, maxLabsSize)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, []Warning{{Source: path, Msg: fmt.Sprintf("cannot read labs: %v", err)}}
	}
	var ls Labs
	if err := json.Unmarshal(data, &ls); err != nil {
		w := Warning{Source: path, Msg: "corrupt labs file, starting without labs"}
		if rerr := os.Rename(path, path+".bak"); rerr == nil {
			w.Msg += " (saved as " + filepath.Base(path) + ".bak)"
		}
		return empty, []Warning{w}
	}
	var warns []Warning
	if ls.Version > LabsVersion {
		warns = append(warns, Warning{Source: path, Msg: fmt.Sprintf("written by a newer viewDI (version %d); unknown fields are dropped on the next save", ls.Version)})
	}
	for _, f := range ls.normalize() {
		warns = append(warns, Warning{Source: path, Msg: f})
	}
	ls.Version = LabsVersion
	return ls, warns
}

// SaveLabs writes labs.json atomically.
func SaveLabs(path string, ls Labs) error {
	ls.Version = LabsVersion
	if ls.Labs == nil {
		ls.Labs = []Lab{}
	}
	data, err := json.MarshalIndent(ls, "", "  ")
	if err != nil {
		return fmt.Errorf("save labs: %w", err)
	}
	return WriteFileAtomic(path, append(data, '\n'), 0o600)
}
