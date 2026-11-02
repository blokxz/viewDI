// Package github reads the release feeds of the engines on GitHub
// (moby/moby, lxc/incus). Requests are conditional (If-None-Match against
// the ETag of the last answer), so a steady state costs no rate limit; a
// GITHUB_TOKEN may be passed in from the environment and is never stored.
// A rate-limit answer stops every request until the reset time.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/updates"
)

// Limits.
const (
	Timeout   = 20 * time.Second
	MaxBody   = 4 << 20 // bytes read of one answer
	PerPage   = 50
	DefaultRL = time.Hour // back-off when a rate-limit answer has no reset time
)

// DefaultBase is the GitHub REST API.
const DefaultBase = "https://api.github.com"

// repos are the release feeds per engine.
var repos = map[engine.ID]string{
	engine.Docker: "moby/moby",
	engine.Incus:  "lxc/incus",
}

// ErrRateLimited is returned while GitHub's rate limit is exhausted.
var ErrRateLimited = errors.New("GitHub rate limit reached")

var cve = regexp.MustCompile(`CVE-\d{4}-\d{4,}`)

// Source is an updates.Upstream backed by the GitHub releases API.
type Source struct {
	client *http.Client
	token  string
	base   string
	clock  clock.Clock

	mu      sync.Mutex
	entries map[engine.ID]entry
	until   time.Time // no requests before this time (rate limit)
}

// entry is the last good answer for one feed.
type entry struct {
	ETag     string            `json:"etag"`
	Releases []updates.Release `json:"releases"`
}

// New returns a source. client is the only way out to the network; token
// ("" for none) is sent as a bearer token. base "" is DefaultBase.
func New(client *http.Client, token, base string, clk clock.Clock) *Source {
	if base == "" {
		base = DefaultBase
	}
	if clk == nil {
		clk = clock.Real{}
	}
	return &Source{client: client, token: token, base: strings.TrimSuffix(base, "/"), clock: clk, entries: map[engine.ID]entry{}}
}

// Releases implements updates.Upstream.
func (s *Source) Releases(ctx context.Context, id engine.ID) ([]updates.Release, error) {
	repo, ok := repos[id]
	if !ok {
		return nil, fmt.Errorf("releases: no feed for %s", id)
	}
	s.mu.Lock()
	until, prev := s.until, s.entries[id]
	s.mu.Unlock()
	if now := s.clock.Now(); now.Before(until) {
		return nil, fmt.Errorf("releases %s: %w until %s", repo, ErrRateLimited, until.Local().Format("15:04"))
	}

	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/repos/"+repo+"/releases?per_page="+strconv.Itoa(PerPage), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("releases %s: %w", repo, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "viewDI")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	if prev.ETag != "" {
		req.Header.Set("If-None-Match", prev.ETag)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("releases %s: %w", repo, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotModified && prev.ETag != "":
		return slices.Clone(prev.Releases), nil
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusForbidden && (resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != ""):
		until := s.resetTime(resp.Header)
		s.mu.Lock()
		s.until = until
		s.mu.Unlock()
		return nil, fmt.Errorf("releases %s: %w until %s", repo, ErrRateLimited, until.Local().Format("15:04"))
	default:
		return nil, fmt.Errorf("releases %s: HTTP %d", repo, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return nil, fmt.Errorf("releases %s: %w", repo, err)
	}
	rels, err := Parse(id, body)
	if err != nil {
		return nil, fmt.Errorf("releases %s: %w", repo, err)
	}
	s.mu.Lock()
	s.entries[id] = entry{ETag: resp.Header.Get("ETag"), Releases: rels}
	s.mu.Unlock()
	return slices.Clone(rels), nil
}

// resetTime reads when the rate limit lifts: Retry-After (seconds), else
// X-RateLimit-Reset (Unix time), else DefaultRL from now.
func (s *Source) resetTime(h http.Header) time.Time {
	now := s.clock.Now()
	if v, err := strconv.Atoi(h.Get("Retry-After")); err == nil && v > 0 {
		return now.Add(time.Duration(min(v, 86400)) * time.Second)
	}
	if v, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		if t := time.Unix(v, 0); t.After(now) && t.Before(now.Add(24*time.Hour)) {
			return t
		}
	}
	return now.Add(DefaultRL)
}

// Snapshot returns the ETags and releases to keep in the update cache, so
// the first check after a restart is conditional too.
func (s *Source) Snapshot() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(s.entries)
	if err != nil {
		return nil
	}
	return b
}

// Restore loads what Snapshot returned; anything unreadable is ignored.
func (s *Source) Restore(b json.RawMessage) {
	var m map[engine.ID]entry
	if len(b) == 0 || json.Unmarshal(b, &m) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range m {
		if _, ok := repos[id]; ok && e.ETag != "" {
			s.entries[id] = e
		}
	}
}

// release is the part of a GitHub release viewDI reads.
type release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
}

// Parse reads a releases answer: the stable releases of engine id, newest
// first. Docker releases are the docker-v* tags of moby/moby; drafts,
// prereleases and release candidates are dropped.
func Parse(id engine.ID, body []byte) ([]updates.Release, error) {
	var in []release
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var out []updates.Release
	for _, r := range in {
		v, ok := version(id, r.TagName)
		if !ok || r.Draft || r.Prerelease {
			continue
		}
		u := updates.Release{Version: v, Published: r.PublishedAt, Security: cve.MatchString(r.Name) || cve.MatchString(r.Body)}
		if strings.HasPrefix(r.HTMLURL, "https://github.com/") && len(r.HTMLURL) < 512 && !strings.ContainsAny(r.HTMLURL, " \"'<>\x00\x1b") {
			u.URL = r.HTMLURL
		}
		out = append(out, u)
	}
	slices.SortStableFunc(out, func(a, b updates.Release) int {
		return semver.Compare(updates.Canonical(b.Version), updates.Canonical(a.Version))
	})
	return slices.CompactFunc(out, func(a, b updates.Release) bool { return a.Version == b.Version }), nil
}

// version returns the upstream version of a release tag, if it is a
// stable release of engine id: "docker-v29.1.2" → "29.1.2", "v6.0.6" →
// "6.0.6".
func version(id engine.ID, tag string) (string, bool) {
	switch id {
	case engine.Docker:
		var ok bool
		if tag, ok = strings.CutPrefix(tag, "docker-v"); !ok {
			return "", false
		}
	case engine.Incus:
		tag = strings.TrimPrefix(tag, "v")
	default:
		return "", false
	}
	if tag == "" || len(tag) > 32 || strings.Trim(tag, "0123456789.") != "" {
		return "", false
	}
	if updates.Canonical(tag) == "" {
		return "", false
	}
	return tag, true
}
