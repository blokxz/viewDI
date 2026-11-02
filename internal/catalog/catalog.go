// Package catalog is the container library's data: a list of well-known
// Docker images and Incus system images, each with sensible defaults, read
// from TOML. The built-in entries live in the repository's library/
// directory (embedded, like themes/); users add their own under
// ~/.config/viewDI/library/. Nothing here touches a daemon or the network:
// the Library view turns an entry into an ordinary engine.CreateSpec.
package catalog

import (
	"fmt"
	"sort"
	"strings"

	"github.com/blokxz/viewDI/internal/engine"
)

// Source says where an entry came from.
type Source int

// Entry sources.
const (
	Builtin Source = iota
	User
)

// Limits that keep a hostile or broken file from costing anything.
const (
	MaxFileSize  = 256 << 10 // bytes in one TOML file
	MaxEntries   = 500       // entries in the whole catalog
	MaxCloudInit = 16 << 10  // bytes of cloud-init user-data
)

// Entry is one library item. Docker entries use Image, Ports, Env, Volumes
// and Restart; Incus entries use Image (an images: alias), Kinds, Limits,
// Profiles, Config and CloudInit. Validate enforces the split.
type Entry struct {
	ID          string    `toml:"id"`
	Name        string    `toml:"name"`
	Category    string    `toml:"category"`
	Description string    `toml:"description"`
	Engine      engine.ID `toml:"engine"`
	Docs        string    `toml:"docs"`
	// Note is shown after the instance is created. {{name}} is the real
	// instance name and {{port 5432}} the host port mapped to container
	// port 5432.
	Note  string `toml:"note"`
	Image string `toml:"image"`

	// Docker.
	Ports   []PortSpec   `toml:"ports"`
	Env     []EnvSpec    `toml:"env"`
	Volumes []VolumeSpec `toml:"volumes"`
	Restart string       `toml:"restart"`

	// Incus.
	Kinds       []string          `toml:"kinds"`        // "container", "vm"
	DefaultKind string            `toml:"default_kind"` // one of Kinds
	CPU         int               `toml:"cpu"`
	Memory      string            `toml:"memory"`
	Profiles    []string          `toml:"profiles"`
	Config      map[string]string `toml:"config"`
	CloudInit   string            `toml:"cloud_init"`

	Source Source `toml:"-"`
}

// PortSpec is a published port. Host 0 means the same number as Container.
type PortSpec struct {
	Container uint16 `toml:"container"`
	Host      uint16 `toml:"host"`
	Protocol  string `toml:"protocol"` // tcp (default) or udp
}

// EnvSpec is an environment variable of a Docker entry. Generate = "password"
// fills a random secret when the instance is created.
type EnvSpec struct {
	Name        string `toml:"name"`
	Value       string `toml:"value"`
	Description string `toml:"description"`
	Required    bool   `toml:"required"`
	Generate    string `toml:"generate"`
}

// VolumeSpec is a named volume mounted at Path. The volume is named
// "<instance>-<Name>" when created.
type VolumeSpec struct {
	Name string `toml:"name"`
	Path string `toml:"path"`
}

// HasKind reports whether an Incus entry supports kind ("container", "vm").
func (e Entry) HasKind(kind string) bool {
	for _, k := range e.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Catalog is the merged set of entries, in a stable order.
type Catalog struct {
	entries []Entry
}

// New builds a catalog from entries, a later entry replacing an earlier one
// with the same ID (so a user entry replaces a built-in one), sorted by
// category then name. Entries must already be valid.
func New(entries ...Entry) *Catalog {
	byID := map[string]int{}
	var out []Entry
	for _, e := range entries {
		if i, ok := byID[e.ID]; ok {
			out[i] = e
			continue
		}
		byID[e.ID] = len(out)
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return categoryLess(out[i], out[j])
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return &Catalog{entries: out}
}

// categoryLess keeps the user's own category last and the rest alphabetical.
func categoryLess(a, b Entry) bool {
	if (a.Source == User) != (b.Source == User) {
		return a.Source != User
	}
	return a.Category < b.Category
}

// Entries returns every entry. The slice must not be modified.
func (c *Catalog) Entries() []Entry { return c.entries }

// Get returns the entry with the given ID.
func (c *Catalog) Get(id string) (Entry, bool) {
	for _, e := range c.entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Categories lists the categories in display order, without repeats.
func (c *Catalog) Categories() []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range c.entries {
		if !seen[e.Category] {
			seen[e.Category] = true
			out = append(out, e.Category)
		}
	}
	return out
}

func (e Entry) String() string { return fmt.Sprintf("%s (%s)", e.ID, e.Engine) }
