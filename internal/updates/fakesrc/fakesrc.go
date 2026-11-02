// Package fakesrc provides update sources for --mock and tests: no
// subprocess, no network. What they report follows the mock scenario:
// homelab finds an Incus LTS security fix and a Docker minor release,
// offline fails every check. Image checks come from the fake engines.
package fakesrc

import (
	"context"
	"fmt"
	"sync"
	"syscall"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/updates"
)

// Source is both an updates.Packages and an updates.Upstream.
type Source struct {
	mu       sync.Mutex
	packages map[engine.ID]updates.Package
	releases map[engine.ID][]updates.Release
	err      error
	calls    int
}

// New returns a source with nothing to report: every engine is not
// managed by a package manager and has no releases.
func New() *Source {
	return &Source{packages: map[engine.ID]updates.Package{}, releases: map[engine.ID][]updates.Release{}}
}

// ForScenario returns the source of a mock scenario.
func ForScenario(name string) *Source {
	s := New()
	if name == "offline" {
		s.Fail(fmt.Errorf("dial tcp: lookup api.github.com: %w", syscall.ENETUNREACH))
		return s
	}
	day := func(d int) time.Time { return time.Date(2026, 10, d, 9, 0, 0, 0, time.UTC) }
	// The fake engines report Docker 29.0.1 and Incus 6.0.5.
	s.SetPackage(engine.Docker, updates.Package{
		Manager: "mock", Name: "docker-ce",
		Installed: "5:29.0.1-1~debian.12~bookworm", Candidate: "5:29.1.0-1~debian.12~bookworm",
		Hint: "sudo apt install --only-upgrade docker-ce",
	})
	s.SetPackage(engine.Incus, updates.Package{
		Manager: "mock", Name: "incus",
		Installed: "1:6.0.5-debian12-202609", Candidate: "1:6.0.6-debian12-202610",
		Hint: "sudo apt install --only-upgrade incus",
	})
	s.SetReleases(engine.Docker, []updates.Release{
		{Version: "29.1.0", Published: day(22), URL: "https://github.com/moby/moby/releases/tag/docker-v29.1.0"},
		{Version: "29.0.1", Published: day(2), URL: "https://github.com/moby/moby/releases/tag/docker-v29.0.1"},
	})
	s.SetReleases(engine.Incus, []updates.Release{
		{Version: "6.18", Published: day(24), URL: "https://github.com/lxc/incus/releases/tag/v6.18"},
		{Version: "6.0.6", Published: day(17), URL: "https://github.com/lxc/incus/releases/tag/v6.0.6", Security: true},
		{Version: "6.0.5", Published: day(1), URL: "https://github.com/lxc/incus/releases/tag/v6.0.5"},
	})
	return s
}

// SetPackage sets what the package manager knows about engine id.
func (s *Source) SetPackage(id engine.ID, p updates.Package) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.packages[id] = p
}

// SetReleases sets the upstream releases of engine id, newest first.
func (s *Source) SetReleases(id engine.ID, rels []updates.Release) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases[id] = rels
}

// Fail makes every lookup fail with err (nil: answer again).
func (s *Source) Fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// Calls returns how many lookups were made.
func (s *Source) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// Lookup implements updates.Packages.
func (s *Source) Lookup(ctx context.Context, id engine.ID) (updates.Package, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if err := ctx.Err(); err != nil {
		return updates.Package{}, err
	}
	if s.err != nil {
		return updates.Package{}, fmt.Errorf("mock package manager: %w", s.err)
	}
	p, ok := s.packages[id]
	if !ok {
		return updates.Package{}, updates.ErrNotManaged
	}
	return p, nil
}

// Releases implements updates.Upstream.
func (s *Source) Releases(ctx context.Context, id engine.ID) ([]updates.Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.err != nil {
		return nil, fmt.Errorf("mock releases: %w", s.err)
	}
	return append([]updates.Release(nil), s.releases[id]...), nil
}
