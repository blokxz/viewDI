package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/blokxz/viewDI/internal/app"
	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/updates/checker"
	"github.com/blokxz/viewDI/internal/updates/fakesrc"
	"github.com/blokxz/viewDI/internal/updates/github"
	"github.com/blokxz/viewDI/internal/updates/notifier"
	"github.com/blokxz/viewDI/internal/updates/pkgmgr"
)

// mockStartDelay lets the --mock demo show its first report quickly.
const mockStartDelay = 3 * time.Second

// startUpdates builds and runs the update checker. With updates.enabled =
// false it returns a nil checker: no source is built, nothing runs and
// nothing reaches the network. --mock always checks, against fake sources
// that run no subprocess and make no network call. The stop function waits
// for the checker goroutine.
func startUpdates(ctx context.Context, f flags, cfg config.Config, paths config.Paths, engines []engine.Engine, logger *slog.Logger) (*checker.Checker, func()) {
	if f.Mock != "" {
		cfg.Updates.Enabled = true
	}
	if !cfg.Updates.Enabled {
		logger.Info("update checks disabled")
		return nil, func() {}
	}
	opts := checker.Options{Config: cfg.Updates, Engines: engines, Logger: logger}
	if f.Mock != "" {
		src := fakesrc.ForScenario(f.Mock)
		opts.Packages, opts.Upstream = src, src
		opts.StartDelay = mockStartDelay
	} else {
		// A nil *pkgmgr.Source must not become a non-nil interface.
		if s := pkgmgr.Detect(exec.LookPath, pkgmgr.Exec{}); s != nil {
			logger.Info("package manager", "name", s.Manager())
			opts.Packages = s
		}
		opts.Upstream = github.New(&http.Client{Timeout: 30 * time.Second}, os.Getenv("GITHUB_TOKEN"), "", nil)
		opts.CacheFile = paths.UpdatesCache()
	}
	c := checker.New(opts)
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	return c, func() { cancel(); <-done }
}

// desktopNotifier returns the notifier main wires into the UI, or nil:
// notify_desktop is opt-in, and --mock never notifies without
// --mock-notify (its updates are made up).
func desktopNotifier(f flags, cfg config.Config, logger *slog.Logger) app.Notifier {
	switch {
	case f.Mock != "" && !f.MockNotify:
		return nil
	case f.Mock == "" && (!cfg.Updates.Enabled || !cfg.Updates.NotifyDesktop):
		return nil
	}
	logger.Info("desktop notifications on", "min_level", cfg.Updates.NotifyMinLevel)
	return &notifier.Desktop{Logger: logger}
}

// updater adapts a possibly nil checker to app.Updater, so a nil checker
// becomes a nil interface (checks off).
func updater(c *checker.Checker) app.Updater {
	if c == nil {
		return nil
	}
	return c
}

// openURL opens a link in the desktop browser. It runs inside a tea.Cmd,
// never in Update.
func openURL(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// xdg-open hands the URL to the browser and returns; its output would
	// corrupt the TUI, so it is discarded.
	return exec.CommandContext(ctx, "xdg-open", url).Run()
}
