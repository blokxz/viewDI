// Package checker runs the update checks: engine versions against the
// package manager or the upstream release feeds, and image sources through
// the engines. It owns one goroutine (Run), started and canceled by main,
// and hands full reports to the UI over a channel. It never installs
// anything, and with updates.enabled = false it does nothing at all.
package checker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/updates"
)

// Defaults.
const (
	DefaultStartDelay   = 30 * time.Second
	DefaultImageTimeout = 30 * time.Second
	DefaultParallel     = 4
	maxCacheSize        = 4 << 20
	cacheVersion        = 1
	maxFailed           = 300 // characters of Report.Failed
)

// Options configure a Checker.
type Options struct {
	Config  config.UpdatesConfig
	Engines []engine.Engine
	// Packages and Upstream are the engine sources; nil means none.
	Packages updates.Packages
	Upstream updates.Upstream
	// CacheFile is updates.json; "" keeps nothing on disk (--mock).
	CacheFile string
	Clock     clock.Clock
	Logger    *slog.Logger
	// StartDelay is how long after startup the first check runs.
	StartDelay time.Duration
	// ImageTimeout bounds one image check; Parallel caps concurrent ones.
	ImageTimeout time.Duration
	Parallel     int
}

// Snapshotter is an Upstream whose state (ETags) is kept in the cache.
type Snapshotter interface {
	Snapshot() json.RawMessage
	Restore(b json.RawMessage)
}

// Checker schedules and runs the checks.
type Checker struct {
	opts    Options
	engines map[engine.ID]engine.Engine
	out     chan updates.Report
	wake    chan struct{}
	wakeImg chan struct{}

	// Owned by Run.
	rep  updates.Report
	seen []string
}

// New returns a checker; nothing runs before Run.
func New(opts Options) *Checker {
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.StartDelay <= 0 {
		opts.StartDelay = DefaultStartDelay
	}
	if opts.ImageTimeout <= 0 {
		opts.ImageTimeout = DefaultImageTimeout
	}
	if opts.Parallel <= 0 {
		opts.Parallel = DefaultParallel
	}
	c := &Checker{opts: opts, engines: map[engine.ID]engine.Engine{}, out: make(chan updates.Report, 1), wake: make(chan struct{}, 1), wakeImg: make(chan struct{}, 1)}
	for _, e := range opts.Engines {
		c.engines[e.ID()] = e
	}
	return c
}

// Enabled reports whether checks run at all.
func (c *Checker) Enabled() bool { return c.opts.Config.Enabled }

// Reports delivers every report: the cached one at startup, a Running one
// when a check starts, and the result when it ends.
func (c *Checker) Reports() <-chan updates.Report { return c.out }

// CheckNow asks for a check of everything as soon as possible. A request
// made while a check runs is dropped: that check is the answer.
func (c *Checker) CheckNow() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// CheckImagesNow asks for an image check as soon as possible, after a
// pull, refresh or recreate changed what runs. Engines are not checked
// (no package manager or release feed is queried). Like CheckNow, a
// request made while a check runs is dropped.
func (c *Checker) CheckImagesNow() {
	select {
	case c.wakeImg <- struct{}{}:
	default:
	}
}

// Run schedules checks until ctx ends. It returns at once when updates are
// disabled.
func (c *Checker) Run(ctx context.Context) {
	if !c.Enabled() {
		return
	}
	cfg := c.opts.Config
	c.load()
	if !c.rep.EnginesAt.IsZero() || !c.rep.ImagesAt.IsZero() {
		r := c.snapshot()
		r.FromCache = true
		c.publish(ctx, r)
	}
	now := c.opts.Clock.Now()
	first := now.Add(c.opts.StartDelay)
	nextEng, nextImg := first, first
	if !cfg.CheckOnStartup {
		nextEng = later(first, c.rep.EnginesAt, now, time.Duration(cfg.EngineInterval))
		nextImg = later(first, c.rep.ImagesAt, now, time.Duration(cfg.ImageInterval))
	}
	for {
		due := nextEng
		if cfg.CheckImages && nextImg.Before(due) {
			due = nextImg
		}
		manual, ok := c.wait(ctx, due.Sub(c.opts.Clock.Now()))
		if !ok {
			return
		}
		now = c.opts.Clock.Now()
		eng := manual == wakeAll || !now.Before(nextEng)
		img := cfg.CheckImages && (manual != wakeNone || !now.Before(nextImg))
		if !eng && !img {
			continue
		}
		c.run(ctx, eng, img)
		if ctx.Err() != nil {
			return
		}
		now = c.opts.Clock.Now()
		if eng {
			nextEng = now.Add(time.Duration(cfg.EngineInterval))
		}
		if img {
			nextImg = now.Add(time.Duration(cfg.ImageInterval))
		}
	}
}

// answered drops the requests made while a check ran: it is their answer.
// It runs before the report is handed over, so a request made after the
// UI has the report is never lost. A U made during an image-only check
// still runs.
func (c *Checker) answered(eng bool) {
	drain := []chan struct{}{c.wakeImg}
	if eng {
		drain = append(drain, c.wake)
	}
	for _, ch := range drain {
		select {
		case <-ch:
		default:
		}
	}
}

// later returns when a periodic check is next due without a startup
// check: interval after the cached run, but not before first.
func later(first, last, now time.Time, interval time.Duration) time.Time {
	t := now.Add(interval)
	if !last.IsZero() {
		t = last.Add(interval)
	}
	if t.Before(first) {
		return first
	}
	return t
}

// wakeKind is what ended a wait.
type wakeKind int

const (
	wakeNone   wakeKind = iota // the timer: a scheduled check
	wakeAll                    // CheckNow
	wakeImages                 // CheckImagesNow
)

// wait blocks for d, a CheckNow, a CheckImagesNow or the end of ctx.
func (c *Checker) wait(ctx context.Context, d time.Duration) (manual wakeKind, ok bool) {
	fired := make(chan struct{})
	t := c.opts.Clock.AfterFunc(max(d, 0), func() { close(fired) })
	defer t.Stop()
	select {
	case <-ctx.Done():
		return wakeNone, false
	case <-c.wake:
		return wakeAll, true
	case <-c.wakeImg:
		return wakeImages, true
	case <-fired:
		return wakeNone, true
	}
}

// publish hands r to the UI; it blocks until the UI takes it or ctx ends.
func (c *Checker) publish(ctx context.Context, r updates.Report) {
	select {
	case c.out <- r:
	case <-ctx.Done():
	}
}

// snapshot is a copy of the current report, safe to hand over.
func (c *Checker) snapshot() updates.Report {
	r := c.rep
	r.Engines = slices.Clone(r.Engines)
	r.Images = slices.Clone(r.Images)
	r.New = nil
	return r
}

// run checks engines and/or images, then publishes and caches the result.
func (c *Checker) run(ctx context.Context, eng, img bool) {
	r := c.snapshot()
	r.Running = true
	c.publish(ctx, r)

	var fails []string
	if eng {
		fails = append(fails, c.checkEngines(ctx)...)
	}
	if img {
		fails = append(fails, c.checkImages(ctx)...)
	}
	if ctx.Err() != nil {
		return
	}
	c.rep.Failed = ""
	if len(fails) > 0 {
		c.rep.Failed = truncate(strings.Join(fails, "; "), maxFailed)
		c.opts.Logger.Debug("update check failed", "errors", fails)
	}
	r = c.snapshot()
	c.seen = r.Diff(c.seen)
	c.save()
	c.answered(eng)
	c.publish(ctx, r)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// --- engines ---

// checkEngines refreshes every engine's status. An engine whose check
// fails keeps its last good result; its error is returned.
func (c *Checker) checkEngines(ctx context.Context) []string {
	var fails []string
	prev := map[engine.ID]updates.EngineStatus{}
	for _, s := range c.rep.Engines {
		prev[s.Engine] = s
	}
	var out []updates.EngineStatus
	for _, e := range c.opts.Engines {
		st := c.checkEngine(ctx, e)
		if st.Err != "" {
			fails = append(fails, e.ID().Label()+": "+st.Err)
			if p, ok := prev[e.ID()]; ok && p.Err == "" {
				st = p
			}
		}
		out = append(out, st)
	}
	c.rep.Engines = out
	c.rep.EnginesAt = c.opts.Clock.Now()
	return fails
}

// checkEngine asks the configured sources about one engine.
func (c *Checker) checkEngine(ctx context.Context, e engine.Engine) updates.EngineStatus {
	cfg := c.opts.Config
	id := e.ID()
	st := updates.EngineStatus{Engine: id, Channel: "stable"}
	ictx, cancel := clock.WithTimeout(ctx, c.opts.Clock, engine.DefaultTimeout)
	info, infoErr := e.Info(ictx)
	cancel()
	if infoErr == nil {
		st.Installed = info.Version
	}

	var errs []string
	if cfg.EngineSource != "upstream" && c.opts.Packages != nil {
		p, err := c.opts.Packages.Lookup(ctx, id)
		if err == nil {
			return c.fromPackage(ctx, st, p)
		}
		if cfg.EngineSource == "package" || !errors.Is(err, updates.ErrNotManaged) {
			errs = append(errs, err.Error())
		}
		if cfg.EngineSource == "package" {
			st.Err = strings.Join(errs, "; ")
			return st
		}
	} else if cfg.EngineSource == "package" {
		st.Err = "no package manager found"
		return st
	}
	switch {
	case c.opts.Upstream == nil:
		errs = append(errs, "no update source")
	case st.Installed == "":
		errs = append(errs, fmt.Sprintf("installed version unknown: %v", infoErr))
	default:
		rels, err := c.opts.Upstream.Releases(ctx, id)
		if err == nil {
			return fromReleases(st, rels, cfg.IncusChannel)
		}
		errs = append(errs, err.Error())
	}
	st.Err = strings.Join(errs, "; ")
	return st
}

// fromPackage fills st from a package lookup, with the release notes and
// security flag of the upstream release when the feed answers.
func (c *Checker) fromPackage(ctx context.Context, st updates.EngineStatus, p updates.Package) updates.EngineStatus {
	if st.Installed == "" {
		st.Installed = upstreamVersion(p.Installed)
	}
	if st.Engine == engine.Incus {
		st.Channel = updates.IncusChannel(st.Installed, c.opts.Config.IncusChannel)
	}
	st.Source, st.Hint = p.Manager, p.Hint
	if p.Candidate == "" || p.Candidate == p.Installed || updates.Newer(p.Candidate, p.Installed) {
		return st
	}
	st.Available = upstreamVersion(p.Candidate)
	if updates.Canonical(p.Candidate) == updates.Canonical(p.Installed) {
		st.Available = p.Candidate // a new package revision of the same release
	}
	security := false
	if c.opts.Upstream != nil {
		rels, err := c.opts.Upstream.Releases(ctx, st.Engine)
		if err != nil {
			c.opts.Logger.Debug("release notes unavailable", "engine", st.Engine, "err", err)
		}
		want := updates.Canonical(p.Candidate)
		for _, r := range rels {
			v := updates.Canonical(r.Version)
			if v == want {
				st.NotesURL, st.Released = r.URL, r.Published
			}
			if r.Security && semver.Compare(v, want) <= 0 && updates.Newer(p.Installed, r.Version) {
				security = true
			}
		}
	}
	st.Level = updates.LevelOf(p.Installed, p.Candidate, security)
	return st
}

// fromReleases fills st from the upstream feed: the newest release of the
// installed channel that is newer than what runs.
func fromReleases(st updates.EngineStatus, rels []updates.Release, override string) updates.EngineStatus {
	st.Source = "github"
	if st.Engine == engine.Incus {
		st.Channel = updates.IncusChannel(st.Installed, override)
	}
	inChannel := func(v string) bool {
		if st.Engine != engine.Incus || st.Channel != "LTS" {
			return true
		}
		cv, ci := updates.Canonical(v), updates.Canonical(st.Installed)
		if ci == "" || semver.MajorMinor(ci) != semver.Major(ci)+".0" {
			// An LTS override on a feature release: the newest LTS line.
			return semver.MajorMinor(cv) == semver.Major(cv)+".0"
		}
		return semver.MajorMinor(cv) == semver.MajorMinor(ci)
	}
	var best *updates.Release
	for i := range rels {
		if inChannel(rels[i].Version) && updates.Newer(st.Installed, rels[i].Version) {
			if best == nil || updates.Newer(best.Version, rels[i].Version) {
				best = &rels[i]
			}
		}
	}
	if best == nil {
		return st
	}
	security := false
	for _, r := range rels {
		if r.Security && inChannel(r.Version) && updates.Newer(st.Installed, r.Version) && !updates.Newer(best.Version, r.Version) {
			security = true
		}
	}
	st.Available, st.Released, st.NotesURL = best.Version, best.Published, best.URL
	st.Level = updates.LevelOf(st.Installed, best.Version, security)
	return st
}

// upstreamVersion is the upstream part of a package version
// ("5:29.1.2-1~debian.12" → "29.1.2"), or v when none is found.
func upstreamVersion(v string) string {
	if c := updates.Canonical(v); c != "" {
		// Keep the number of parts upstream uses ("6.18", not "6.18.0").
		s := strings.TrimPrefix(v, v[:strings.IndexByte(v, ':')+1])
		s = strings.TrimPrefix(strings.TrimPrefix(s, "docker-"), "v")
		end := 0
		for end < len(s) && (s[end] == '.' || (s[end] >= '0' && s[end] <= '9')) {
			end++
		}
		if t := strings.Trim(s[:end], "."); t != "" {
			return t
		}
		return strings.TrimPrefix(c, "v")
	}
	return v
}

// --- images ---

// checkImages checks every image source in use, at most Parallel at a
// time. A source whose check fails keeps its last result.
func (c *Checker) checkImages(ctx context.Context) []string {
	var fails []string
	groups := map[engine.ImageCheck][]engine.Instance{}
	listed := map[engine.ID]bool{}
	for _, e := range c.opts.Engines {
		lctx, cancel := clock.WithTimeout(ctx, c.opts.Clock, engine.DefaultTimeout)
		ins, err := e.ListInstances(lctx, engine.ListOptions{})
		cancel()
		if err != nil {
			fails = append(fails, e.ID().Label()+" images: "+err.Error())
			continue
		}
		listed[e.ID()] = true
		for _, in := range ins {
			chk, ok := engine.CheckOf(in)
			if !ok || updates.Ignored(c.opts.Config.IgnoreImages, chk.Ref) {
				continue
			}
			groups[chk] = append(groups[chk], in)
		}
	}
	checks := make([]engine.ImageCheck, 0, len(groups))
	for chk := range groups {
		checks = append(checks, chk)
	}
	slices.SortFunc(checks, func(a, b engine.ImageCheck) int {
		return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
	})

	type result struct {
		st  engine.ImageStatus
		err error
	}
	results := make([]result, len(checks))
	sem := make(chan struct{}, c.opts.Parallel)
	var wg sync.WaitGroup
	for i, chk := range checks {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i].err = ctx.Err()
				return
			}
			defer func() { <-sem }()
			cctx, cancel := clock.WithTimeout(ctx, c.opts.Clock, c.opts.ImageTimeout)
			defer cancel()
			st, err := c.engines[chk.Engine].ImageUpdateStatus(cctx, chk)
			results[i] = result{st, err}
		})
	}
	wg.Wait()

	prev := map[string]updates.ImageUpdate{}
	for _, u := range c.rep.Images {
		prev[u.Key()] = u
	}
	byKey := map[string]*updates.ImageUpdate{}
	var order []string
	add := func(u updates.ImageUpdate, ins []engine.Instance) {
		k := u.Key()
		cur, ok := byKey[k]
		if !ok {
			cur = &u
			byKey[k], order = cur, append(order, k)
		}
		for _, in := range ins {
			if !slices.Contains(cur.Instances, in.Key()) {
				cur.Instances = append(cur.Instances, in.Key())
				cur.Names = append(cur.Names, in.Name)
			}
		}
	}
	errs, firstErr := 0, ""
	failedKeys := map[string]bool{}
	for i, chk := range checks {
		res := results[i]
		if res.err != nil {
			errs++
			if firstErr == "" {
				firstErr = res.err.Error()
			}
			k := (updates.ImageUpdate{Check: chk}).Key()
			if p, ok := prev[k]; ok && !failedKeys[k] {
				failedKeys[k] = true
				add(p, nil)
			}
			continue
		}
		switch res.st.State {
		case engine.ImageOutdated:
			u := updates.ImageUpdate{
				Check: chk, Current: res.st.Current, Available: res.st.Available,
				Serial: res.st.Serial, NewSerial: res.st.NewSerial, Compose: compose(groups[chk]),
			}
			add(u, groups[chk])
		case engine.ImageUpToDate:
			// Pulled but not recreated: the containers that run another
			// image than the reference's still need the update.
			if stale := staleOf(groups[chk], res.st.LocalID); len(stale) > 0 {
				add(updates.ImageUpdate{Check: chk, Available: res.st.Current, Compose: compose(stale), Pulled: true}, stale)
			}
		}
	}
	// An engine that could not be listed keeps its last results.
	for _, p := range c.rep.Images {
		if !listed[p.Check.Engine] {
			if _, ok := byKey[p.Key()]; !ok {
				add(p, nil)
			}
		}
	}
	imgs := make([]updates.ImageUpdate, 0, len(order))
	for _, k := range order {
		u := *byKey[k]
		sortInstances(&u)
		imgs = append(imgs, u)
	}
	updates.SortImages(imgs)
	c.rep.Images = imgs
	c.rep.ImagesChecked, c.rep.ImageErrors, c.rep.ImageErr = len(checks), errs, firstErr
	c.rep.ImagesAt = c.opts.Clock.Now()
	if errs > 0 {
		fails = append(fails, fmt.Sprintf("%d of %d image checks failed: %s", errs, len(checks), firstErr))
	}
	return fails
}

// staleOf returns the Docker containers whose image is not local (the
// image ID their reference resolves to now).
func staleOf(ins []engine.Instance, local string) []engine.Instance {
	if local == "" {
		return nil
	}
	var out []engine.Instance
	for _, in := range ins {
		if x, ok := in.Extra.(*engine.DockerExtra); ok && x.ImageID != "" && x.ImageID != local {
			out = append(out, in)
		}
	}
	return out
}

// compose reports whether every instance is managed by Docker Compose.
func compose(ins []engine.Instance) bool {
	for _, in := range ins {
		x, ok := in.Extra.(*engine.DockerExtra)
		if !ok || x.ComposeProject == "" {
			return false
		}
	}
	return len(ins) > 0
}

func sortInstances(u *updates.ImageUpdate) {
	idx := make([]int, len(u.Instances))
	for i := range idx {
		idx[i] = i
	}
	slices.SortFunc(idx, func(a, b int) int { return strings.Compare(string(u.Instances[a]), string(u.Instances[b])) })
	keys, names := make([]engine.Key, len(idx)), make([]string, len(idx))
	for i, j := range idx {
		keys[i] = u.Instances[j]
		if j < len(u.Names) {
			names[i] = u.Names[j]
		}
	}
	u.Instances, u.Names = keys, names
}

// --- cache ---

// cacheFile is updates.json.
type cacheFile struct {
	Version       int                    `json:"version"`
	Engines       []updates.EngineStatus `json:"engines,omitempty"`
	Images        []updates.ImageUpdate  `json:"images,omitempty"`
	ImagesChecked int                    `json:"images_checked,omitempty"`
	ImageErrors   int                    `json:"image_errors,omitempty"`
	ImageErr      string                 `json:"image_err,omitempty"`
	EnginesAt     time.Time              `json:"engines_at"`
	ImagesAt      time.Time              `json:"images_at"`
	Failed        string                 `json:"failed,omitempty"`
	Seen          []string               `json:"seen,omitempty"`
	Upstream      json.RawMessage        `json:"upstream,omitempty"`
}

// load reads the cache. A missing file is a first run; an unreadable one
// is dropped with a debug-log line (it is only a cache).
func (c *Checker) load() {
	if c.opts.CacheFile == "" {
		return
	}
	b, err := config.ReadCapped(c.opts.CacheFile, maxCacheSize)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	var f cacheFile
	if err == nil {
		err = json.Unmarshal(b, &f)
	}
	if err == nil && f.Version != cacheVersion {
		err = fmt.Errorf("version %d", f.Version)
	}
	if err != nil {
		c.opts.Logger.Debug("update cache dropped", "file", c.opts.CacheFile, "err", err)
		return
	}
	c.rep = updates.Report{
		Engines: f.Engines, Images: f.Images,
		ImagesChecked: f.ImagesChecked, ImageErrors: f.ImageErrors, ImageErr: f.ImageErr,
		EnginesAt: f.EnginesAt, ImagesAt: f.ImagesAt, Failed: f.Failed,
	}
	c.seen = f.Seen
	if s, ok := c.opts.Upstream.(Snapshotter); ok {
		s.Restore(f.Upstream)
	}
}

// save writes the cache atomically; a failure is only logged.
func (c *Checker) save() {
	if c.opts.CacheFile == "" {
		return
	}
	f := cacheFile{
		Version: cacheVersion, Engines: c.rep.Engines, Images: c.rep.Images,
		ImagesChecked: c.rep.ImagesChecked, ImageErrors: c.rep.ImageErrors, ImageErr: c.rep.ImageErr,
		EnginesAt: c.rep.EnginesAt, ImagesAt: c.rep.ImagesAt, Failed: c.rep.Failed, Seen: c.seen,
	}
	if s, ok := c.opts.Upstream.(Snapshotter); ok {
		f.Upstream = s.Snapshot()
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err == nil {
		err = config.WriteFileAtomic(c.opts.CacheFile, b, 0o600)
	}
	if err != nil {
		c.opts.Logger.Debug("update cache not written", "file", c.opts.CacheFile, "err", err)
	}
}
