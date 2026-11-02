package updates

import (
	"context"
	"errors"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

// ErrNotManaged reports that no package manager installed the engine (a
// static binary, a snap): engine_source = auto then asks upstream.
var ErrNotManaged = errors.New("not installed by a package manager")

// Package is what a package manager knows about an engine's package.
type Package struct {
	Manager   string // "apt", "dnf", "pacman", "zypper", "mock"
	Name      string // "docker-ce", "incus"
	Installed string // package version, e.g. "5:29.0.1-1~debian.12~bookworm"
	Candidate string // newest installable package version; "" or Installed when none
	Hint      string // the exact upgrade command for this manager
}

// Packages looks up engine packages. Implementations only read: they never
// refresh databases or run privileged commands.
type Packages interface {
	Lookup(ctx context.Context, id engine.ID) (Package, error)
}

// Release is one upstream release of an engine.
type Release struct {
	Version   string    // upstream version, e.g. "29.1.2", "6.0.6"
	Published time.Time // zero when unknown
	URL       string    // release notes
	Security  bool      // the title or notes name a CVE
}

// Upstream lists the releases of an engine, newest first (stable releases
// only: no drafts, prereleases or release candidates).
type Upstream interface {
	Releases(ctx context.Context, id engine.ID) ([]Release, error)
}
