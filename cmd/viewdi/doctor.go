package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/provider/docker"
	"github.com/blokxz/viewDI/internal/provider/incus"
	"github.com/blokxz/viewDI/internal/supervisor"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/updates/pkgmgr"
)

// `viewdi doctor` checks what viewDI needs from the machine and says how to
// fix what is missing, so a first run (or a bug report) starts from facts:
// the sockets it found, who owns them, and whether this session may use them.

type level int

const (
	levelOK level = iota
	levelWarn
	levelFail
)

type finding struct {
	level  level
	name   string
	detail string
	fix    string
}

// doctorEnv is everything doctor touches outside the process, so tests can
// replace it.
type doctorEnv struct {
	getenv   func(string) string
	lookPath func(string) (string, error)
	exists   func(path string) bool
	dockerAt func(ctx context.Context, host string) (engine.Info, error)
	incusAt  func(ctx context.Context, socket string) (engine.Info, error)
	paths    config.Paths
}

func systemDoctorEnv() doctorEnv {
	return doctorEnv{
		getenv:   os.Getenv,
		lookPath: exec.LookPath,
		exists:   func(p string) bool { _, err := os.Stat(p); return err == nil },
		paths:    config.DefaultPaths(),
		dockerAt: func(ctx context.Context, host string) (engine.Info, error) {
			a, err := docker.New(host)
			if err != nil {
				return engine.Info{}, err
			}
			defer func() { _ = a.Close() }()
			if err := a.Ping(ctx); err != nil {
				return engine.Info{}, err
			}
			return a.Info(ctx)
		},
		incusAt: func(ctx context.Context, socket string) (engine.Info, error) {
			a, err := incus.New(ctx, socket)
			if err != nil {
				return engine.Info{}, err
			}
			defer func() { _ = a.Close() }()
			if err := a.Ping(ctx); err != nil {
				return engine.Info{}, err
			}
			return a.Info(ctx)
		},
	}
}

// doctor runs every check. It returns the findings in display order.
func doctor(ctx context.Context, env doctorEnv, f flags, cfg config.Config) []finding {
	var out []finding
	add := func(l level, name, detail, fix string) { out = append(out, finding{l, name, detail, fix}) }

	// Docker.
	host, src := docker.Discover(f.DockerHost, cfg.Docker.Host, env.paths.RuntimeDir, env.getenv)
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	info, err := env.dockerAt(dctx, host)
	cancel()
	if err == nil {
		add(levelOK, "Docker", fmt.Sprintf("%s %s at %s (%s)", info.Engine, info.Version, textsafe.Clean(host), src), "")
	} else {
		add(levelFail, "Docker", engineProblem(err), supervisor.Hint(engine.Docker, engine.Classify(err), host))
	}
	if docker.PlainRemote(host, env.getenv) {
		add(levelWarn, "Docker over TCP", textsafe.Clean(host)+" has no TLS", "Set DOCKER_TLS_VERIFY, or use a Unix socket or ssh://.")
	}

	// Incus.
	sock, isrc := incus.Discover(f.IncusSocket, cfg.Incus.Socket, env.getenv, incus.StatProbe)
	ictx, cancel := context.WithTimeout(ctx, 5*time.Second)
	info, err = env.incusAt(ictx, sock)
	cancel()
	if err == nil {
		add(levelOK, "Incus", fmt.Sprintf("%s %s at %s (%s)", info.Engine, info.Version, textsafe.Clean(sock), isrc), "")
		if incus.IsUserSocket(sock) {
			add(levelWarn, "Incus socket", "this is the restricted unix.socket.user (incus group)", "Join incus-admin for full access: sudo usermod -aG incus-admin $USER, then log in again.")
		}
	} else {
		add(levelFail, "Incus", engineProblem(err), supervisor.Hint(engine.Incus, engine.Classify(err), sock))
	}

	// The machine.
	if env.exists("/sys/fs/cgroup/cgroup.controllers") {
		add(levelOK, "cgroup v2", "unified hierarchy: metrics are read from the kernel", "")
	} else {
		add(levelWarn, "cgroup v2", "not available: metrics come from the engine APIs (slower, less exact)", "Boot with systemd.unified_cgroup_hierarchy=1.")
	}
	term := env.getenv("TERM")
	if term == "" || term == "dumb" {
		add(levelWarn, "Terminal", fmt.Sprintf("TERM=%q", term), "Run viewDI in a real terminal emulator.")
	} else {
		add(levelOK, "Terminal", "TERM="+term, "")
	}
	if asciiTerminal(env.getenv) {
		add(levelWarn, "Locale", "not UTF-8 (or TERM=linux): the tree uses ASCII icons", "Use a UTF-8 locale (LANG=en_US.UTF-8) and a Nerd Font.")
	}
	if env.getenv("NO_COLOR") != "" {
		add(levelOK, "Colour", "NO_COLOR is set: the monochrome theme is used", "")
	}

	// Optional helpers.
	if _, err := env.lookPath("xdg-open"); err != nil {
		add(levelWarn, "xdg-open", "not found: release notes cannot open in a browser (the URL is shown instead)", "Install xdg-utils.")
	}
	if _, err := env.lookPath("notify-send"); err != nil {
		add(levelWarn, "notify-send", "not found: desktop notifications use D-Bus only", "Install libnotify (optional).")
	}
	if s := pkgmgr.Detect(env.lookPath, pkgmgr.Exec{}); s != nil {
		add(levelOK, "Package manager", s.Manager()+": engine updates are checked against it", "")
	} else {
		add(levelWarn, "Package manager", "none of apt, pacman, dnf, zypper found: engine updates use GitHub releases", "")
	}

	// Configuration.
	if _, warns := config.Load(env.paths.ConfigFile()); len(warns) > 0 {
		for _, w := range warns {
			add(levelWarn, "config.toml", textsafe.Clean(w.String()), "Fix the file or delete the line; viewDI uses the default meanwhile.")
		}
	} else {
		add(levelOK, "config.toml", "no problems found", "")
	}
	return out
}

func engineProblem(err error) string {
	return textsafe.Clean(strings.TrimSpace(err.Error()))
}

// renderDoctor prints the findings and returns the exit code: 1 when no
// engine can be used at all, 0 otherwise.
func renderDoctor(w io.Writer, fs []finding) int {
	tag := map[level]string{levelOK: "  ok  ", levelWarn: " warn ", levelFail: " FAIL "}
	engines, usable := 0, 0
	for _, f := range fs {
		fmt.Fprintf(w, "[%s] %-16s %s\n", tag[f.level], f.name, f.detail)
		if f.fix != "" {
			fmt.Fprintf(w, "         %-16s -> %s\n", "", f.fix)
		}
		if f.name == "Docker" || f.name == "Incus" {
			engines++
			if f.level == levelOK {
				usable++
			}
		}
	}
	switch {
	case usable == 0 && engines > 0:
		fmt.Fprintln(w, "\nviewDI cannot reach Docker or Incus. Fix one of them above, then run `viewdi doctor` again.")
		return 1
	case usable < engines:
		fmt.Fprintln(w, "\nviewDI works with the engine that is ok; the other will show as unavailable until you fix it.")
	default:
		fmt.Fprintln(w, "\nEverything viewDI needs is in place.")
	}
	return 0
}

// runDoctor is `viewdi doctor [--docker-host URI] [--incus-socket PATH]`.
func runDoctor(args []string, stdout, stderr io.Writer) int {
	f, err := parseFlags(args, io.Discard)
	if err != nil {
		fmt.Fprintln(stderr, "viewdi: doctor:", err)
		return 2
	}
	env := systemDoctorEnv()
	cfg, _ := config.Load(env.paths.ConfigFile())
	return renderDoctor(stdout, doctor(context.Background(), env, f, cfg))
}
