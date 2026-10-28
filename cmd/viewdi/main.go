// Command viewdi is a terminal UI that manages and monitors Docker and Incus
// side by side.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/app"
	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/trace"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/themes"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

const maxLogSize = 10 << 20

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	start := time.Now()
	f, err := parseFlags(args, stdout)
	if errors.Is(err, errHelp) {
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "viewdi:", err)
		return 2
	}
	if f.Version {
		_, _ = fmt.Fprintln(stdout, "viewdi", version)
		return 0
	}

	paths := config.DefaultPaths()
	logger, closeLog := openLog(f.Debug, paths.LogFile(), stderr)
	defer closeLog()

	var warns []config.Warning
	add := func(w []config.Warning) { warns = append(warns, w...) }

	cfg, w := config.Load(paths.ConfigFile())
	add(w)
	state, w := config.LoadState(paths.StateFile())
	add(w)
	reg, w := theme.LoadRegistry(themes.FS, paths.ThemesDir())
	add(w)
	// Precedence: --theme > last picker choice (state.json) > config.toml > default.
	th, w := reg.Resolve(f.Theme, state.Theme, cfg.UI.Theme)
	add(w)
	overrides, w := keys.LoadOverrides(paths.KeysFile())
	add(w)
	km, w := keys.Apply(keys.Defaults(), overrides)
	add(w)

	refresh := time.Duration(cfg.UI.Refresh)
	if f.Refresh > 0 {
		refresh = f.Refresh
	}
	if os.Geteuid() == 0 {
		add([]config.Warning{{Msg: "running as root is not needed and not recommended"}})
	}
	for _, w := range warns {
		logger.Warn("startup", "warning", w.String())
	}
	logger.Info("starting", "version", version, "mock", f.Mock, "read_only", f.ReadOnly,
		"theme", th.ID, "refresh", refresh, "fps", cfg.UI.FPS,
		"docker_host", f.DockerHost, "incus_socket", f.IncusSocket)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if f.Pprof != "" {
		stopPprof, err := startPprof(ctx, f.Pprof, logger)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "viewdi: --pprof:", err)
			return 1
		}
		defer stopPprof()
	}

	progOpts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithFPS(cfg.UI.FPS)}
	if f.Trace != "" {
		path := f.Trace
		if path == traceAuto {
			path = paths.TraceFile(start.Format("20060102-150405"))
		}
		tw, err := trace.Open(path)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "viewdi:", err)
			return 1
		}
		defer func() {
			if err := tw.Close(); err != nil {
				logger.Warn("trace close", "err", err)
			}
			_, _ = fmt.Fprintln(stderr, "viewdi: trace written to", path)
		}()
		progOpts = append(progOpts, tea.WithFilter(tw.Filter))
	}

	model := app.New(app.Options{
		Themes:         reg,
		Theme:          th,
		Keys:           km,
		State:          state,
		Warnings:       warns,
		Mock:           f.Mock,
		ReadOnly:       f.ReadOnly,
		Debug:          f.Debug,
		TerminalColors: os.Getenv("NO_COLOR") == "",
		SaveState:      func(st config.State) error { return config.SaveState(paths.StateFile(), st) },
		Logger:         logger,
	})
	p := tea.NewProgram(model, progOpts...)

	// Bubble Tea handles SIGINT/SIGTERM. SIGHUP (terminal closed) quits
	// cleanly; an external SIGTSTP suspends through Bubble Tea so the
	// terminal is restored first (F14).
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTSTP)
	defer signal.Stop(sigs)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case s := <-sigs:
				if s == syscall.SIGHUP {
					p.Quit()
					return
				}
				p.Send(tea.SuspendMsg{})
			}
		}
	}()

	logger.Info("program start", "startup", time.Since(start))
	if _, err := p.Run(); err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		logger.Error("program exited", "err", err)
		_, _ = fmt.Fprintln(stderr, "viewdi:", err)
		return 1
	}
	logger.Info("exit")
	return 0
}

// openLog returns a file logger with --debug and a discarding one otherwise.
// stdout and stderr belong to the TUI, so nothing is ever logged there while
// it runs. The previous log is rotated to viewdi.log.1 when it exceeds 10 MiB.
func openLog(debug bool, path string, stderr io.Writer) (*slog.Logger, func()) {
	if !debug {
		return slog.New(slog.DiscardHandler), func() {}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		_, _ = fmt.Fprintln(stderr, "viewdi: debug log disabled:", err)
		return slog.New(slog.DiscardHandler), func() {}
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogSize {
		_ = os.Rename(path, path+".1")
	}
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "viewdi: debug log disabled:", err)
		return slog.New(slog.DiscardHandler), func() {}
	}
	return slog.New(slog.NewTextHandler(fh, &slog.HandlerOptions{Level: slog.LevelDebug})), func() { _ = fh.Close() }
}

// startPprof serves net/http/pprof on a loopback address until ctx ends.
func startPprof(ctx context.Context, addr string, logger *slog.Logger) (stop func(), err error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn("pprof server", "err", err)
		}
	}()
	logger.Info("pprof listening", "addr", ln.Addr().String())
	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		<-done
	}, nil
}
