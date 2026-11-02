// Package updates holds the update checker's domain model: what an engine
// or image update is, how serious it is, how new results are told apart
// from ones the user has already seen, and the user's snooze and ignore
// decisions. It is pure (no I/O), so the UI may import it.
//
// The checker that produces reports lives in updates/checker; its sources
// in updates/pkgmgr (package managers), updates/github (release feeds) and
// updates/fakesrc (--mock). viewDI never installs anything: a report only
// carries versions, release-note links and upgrade hints.
package updates

import (
	"cmp"
	"path"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/blokxz/viewDI/internal/engine"
)

// Level ranks an update. Desktop notifications honour a minimum level.
type Level int

// Levels, least serious first.
const (
	LevelPatch Level = iota
	LevelMinor
	LevelMajor
	LevelSecurity
)

func (l Level) String() string {
	switch l {
	case LevelMinor:
		return "minor"
	case LevelMajor:
		return "major"
	case LevelSecurity:
		return "security"
	}
	return "patch"
}

// ParseLevel reads a notify_min_level value; unknown values are LevelMinor.
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "patch":
		return LevelPatch
	case "major":
		return LevelMajor
	case "security":
		return LevelSecurity
	}
	return LevelMinor
}

// EngineStatus is the result for one engine. Engines are always reported,
// up to date or not, so the Updates view lists both.
type EngineStatus struct {
	Engine    engine.ID
	Installed string // version the daemon reports
	Available string // newest installable version; "" when up to date or unknown
	Channel   string // "stable", "LTS" or "feature"
	Source    string // "apt", "dnf", "pacman", "zypper", "github", "mock"
	Level     Level
	Released  time.Time // release date of Available, when known
	NotesURL  string    // release notes of Available
	Hint      string    // the exact upgrade command, e.g. "sudo apt install --only-upgrade docker-ce"
	Err       string    // why the check failed; "" on success
}

// HasUpdate reports whether a newer version is available.
func (s EngineStatus) HasUpdate() bool { return s.Available != "" && s.Err == "" }

// Key identifies the update for seen, snooze and ignore bookkeeping.
func (s EngineStatus) Key() string { return "engine/" + string(s.Engine) }

// ImageUpdate is an image with a newer build at its source, and the
// instances that run it.
type ImageUpdate struct {
	Check     engine.ImageCheck
	Instances []engine.Key // sorted
	Names     []string     // instance names, same order
	Current   string       // digest or fingerprint that runs
	Available string       // digest or fingerprint offered
	Serial    string       // Incus build serials, for display
	NewSerial string
	Compose   bool // every instance is managed by Docker Compose
}

// Key identifies the update for seen, snooze and ignore bookkeeping.
func (u ImageUpdate) Key() string {
	k := "image/" + string(u.Check.Engine) + "/" + u.Check.Ref
	if u.Check.Arch != "" {
		k += "/" + u.Check.Arch
	}
	if u.Check.VM {
		k += "/vm"
	}
	return k
}

// Report is one checker result. Reports always carry the full current
// set; New lists the keys (with their version) the user has not seen yet.
type Report struct {
	Engines       []EngineStatus
	Images        []ImageUpdate
	ImagesChecked int    // image sources checked in the last image run
	ImageErrors   int    // image sources whose check failed
	ImageErr      string // the first image error, for the view header
	EnginesAt     time.Time
	ImagesAt      time.Time
	// Failed is the message of the last failed run ("" when it worked);
	// the previous results are kept.
	Failed string
	// Running is true while a check is in progress.
	Running bool
	// New are the updates found in this run that were not in the last
	// seen set: toast and notify these, never the others.
	New []Found
	// FromCache marks the report built at startup from updates.json.
	FromCache bool
}

// Found is a newly discovered update.
type Found struct {
	Key     string
	Version string // the available version or digest
	Text    string // one line for the toast: "Incus 6.0.6 (security)"
	Level   Level
}

// Seen returns the "key@version" set of every update in r.
func (r Report) Seen() []string {
	var out []string
	for _, e := range r.Engines {
		if e.HasUpdate() {
			out = append(out, e.Key()+"@"+e.Available)
		}
	}
	for _, i := range r.Images {
		out = append(out, i.Key()+"@"+i.Available)
	}
	slices.Sort(out)
	return out
}

// Diff fills r.New with the updates of r whose "key@version" is not in
// seen, and returns r's own seen set.
func (r *Report) Diff(seen []string) []string {
	old := map[string]bool{}
	for _, s := range seen {
		old[s] = true
	}
	r.New = nil
	for _, e := range r.Engines {
		if e.HasUpdate() && !old[e.Key()+"@"+e.Available] {
			r.New = append(r.New, Found{Key: e.Key(), Version: e.Available, Text: EngineText(e), Level: e.Level})
		}
	}
	for _, i := range r.Images {
		if !old[i.Key()+"@"+i.Available] {
			r.New = append(r.New, Found{Key: i.Key(), Version: i.Available, Text: i.Check.Ref, Level: LevelPatch})
		}
	}
	return r.Seen()
}

// EngineText is the one-line summary of an engine update: "Incus 6.0.6
// (security)".
func EngineText(e EngineStatus) string {
	s := e.Engine.Label() + " " + e.Available
	if e.Level == LevelSecurity {
		s += " (security)"
	}
	return s
}

// LevelOf ranks the change from installed to available. Versions are
// upstream versions ("29.1.2", "6.0.6", "6.18"); security comes from the
// release notes, not from the numbers.
func LevelOf(installed, available string, security bool) Level {
	if security {
		return LevelSecurity
	}
	a, b := Canonical(installed), Canonical(available)
	switch {
	case a == "" || b == "":
		return LevelMinor
	case semver.Major(a) != semver.Major(b):
		return LevelMajor
	case semver.MajorMinor(a) != semver.MajorMinor(b):
		return LevelMinor
	}
	return LevelPatch
}

// Canonical turns an upstream or package version into a semver string
// ("v29.1.2"): the epoch ("5:"), a "v" or "docker-v" prefix and the
// package revision ("-1~debian.12") are dropped, missing parts are zero.
// It returns "" when no version number is found.
func Canonical(v string) string {
	v = strings.TrimSpace(v)
	if _, after, ok := strings.Cut(v, ":"); ok {
		v = after
	}
	v = strings.TrimPrefix(v, "docker-")
	v = strings.TrimPrefix(v, "v")
	end := 0
	for end < len(v) && (v[end] == '.' || (v[end] >= '0' && v[end] <= '9')) {
		end++
	}
	v = strings.Trim(v[:end], ".")
	if v == "" {
		return ""
	}
	parts := strings.Split(v, ".")
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	s := "v" + strings.Join(parts[:3], ".")
	if !semver.IsValid(s) {
		return ""
	}
	return s
}

// Newer reports whether available is a newer upstream version than
// installed. Versions that cannot be read are never newer.
func Newer(installed, available string) bool {
	a, b := Canonical(installed), Canonical(available)
	return a != "" && b != "" && semver.Compare(b, a) > 0
}

// IncusChannel returns the release channel of an installed Incus version:
// "LTS" for 6.0.x, "feature" otherwise. override ("lts", "stable") wins.
func IncusChannel(installed, override string) string {
	switch strings.ToLower(override) {
	case "lts":
		return "LTS"
	case "stable", "feature":
		return "feature"
	}
	if c := Canonical(installed); c != "" && semver.MajorMinor(c) == semver.Major(c)+".0" {
		return "LTS"
	}
	return "feature"
}

// Ignored reports whether ref matches one of the ignore_images glob
// patterns ("postgres:*", "registry.local/*"). A pattern matches the whole
// reference or a leading part of it that ends at a "/" ("registry.local/*"
// covers "registry.local/team/app:1"). A pattern without a remote also
// matches an Incus reference's alias ("debian/*").
func Ignored(patterns []string, ref string) bool {
	refs := []string{ref}
	if alias, ok := strings.CutPrefix(ref, "images:"); ok {
		refs = append(refs, alias)
	}
	for _, p := range patterns {
		for _, r := range refs {
			if globPrefix(p, r) {
				return true
			}
		}
	}
	return false
}

// globPrefix matches p against s and against every prefix of s that is
// followed by a "/".
func globPrefix(p, s string) bool {
	if ok, _ := path.Match(p, s); ok {
		return true
	}
	for i := range len(s) {
		if s[i] == '/' {
			if ok, _ := path.Match(p, s[:i]); ok {
				return true
			}
		}
	}
	return false
}

// SortImages orders image updates by engine, then reference.
func SortImages(imgs []ImageUpdate) {
	slices.SortFunc(imgs, func(a, b ImageUpdate) int {
		return cmp.Or(cmp.Compare(a.Check.Engine, b.Check.Engine), cmp.Compare(a.Check.Ref, b.Check.Ref), cmp.Compare(a.Key(), b.Key()))
	})
}
