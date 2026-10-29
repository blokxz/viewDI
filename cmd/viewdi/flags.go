package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"time"

	"github.com/spf13/pflag"

	"github.com/blokxz/viewDI/internal/config"
)

// Scenarios accepted by --mock (docs/05-quality-plan.md §1.2).
var mockScenarios = []string{"homelab", "large", "chaos", "logflood", "offline"}

const traceAuto = "auto"

// flags holds the parsed command line.
type flags struct {
	Mock        string // "" = real daemons
	ReadOnly    bool
	Debug       bool
	Trace       string // "" = off, traceAuto = default path
	Pprof       string
	Theme       string
	Refresh     time.Duration // 0 = from config
	DockerHost  string
	IncusSocket string
	Version     bool
}

// errHelp is returned when --help was requested and printed.
var errHelp = errors.New("help requested")

func parseFlags(args []string, out io.Writer) (flags, error) {
	var f flags
	fs := pflag.NewFlagSet("viewdi", pflag.ContinueOnError)
	fs.SetOutput(out)
	fs.SortFlags = false

	fs.StringVar(&f.Mock, "mock", "", "use fake engines instead of real daemons; optional scenario: "+fmt.Sprint(mockScenarios)+" (write --mock=<scenario>)")
	fs.Lookup("mock").NoOptDefVal = "homelab"
	fs.BoolVar(&f.ReadOnly, "read-only", false, "disable every mutating action (monitoring only)")
	fs.BoolVar(&f.Debug, "debug", false, "write a debug log to the state directory and enable the ctrl+d overlay")
	fs.StringVar(&f.Trace, "trace", "", "record every UI message to a file for replay (write --trace=<file>; default under the state directory)")
	fs.Lookup("trace").NoOptDefVal = traceAuto
	fs.StringVar(&f.Pprof, "pprof", "", "serve pprof on a loopback address, e.g. 127.0.0.1:6060")
	fs.StringVar(&f.Theme, "theme", "", "theme ID for this session (overrides state.json and config.toml)")
	fs.DurationVar(&f.Refresh, "refresh", 0, fmt.Sprintf("metrics refresh interval, %s–%s (overrides config.toml)", config.MinRefresh, config.MaxRefresh))
	fs.StringVar(&f.DockerHost, "docker-host", "", "Docker daemon URI (overrides DOCKER_HOST)")
	fs.StringVar(&f.IncusSocket, "incus-socket", "", "Incus unix socket path (overrides INCUS_SOCKET)")
	fs.BoolVar(&f.Version, "version", false, "print the version and exit")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(out, "viewDI — manage and monitor Docker and Incus from one TUI\n\nUsage:\n  viewdi [flags]\n\nFlags:\n%s", fs.FlagUsages())
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return f, errHelp
		}
		return f, err
	}
	if fs.NArg() > 0 {
		hint := ""
		if fs.Changed("mock") && slices.Contains(mockScenarios, fs.Arg(0)) {
			hint = fmt.Sprintf(" (did you mean --mock=%s?)", fs.Arg(0))
		}
		return f, fmt.Errorf("unexpected argument %q%s", fs.Arg(0), hint)
	}
	if f.Mock != "" && !slices.Contains(mockScenarios, f.Mock) {
		return f, fmt.Errorf("--mock: unknown scenario %q (want one of %v)", f.Mock, mockScenarios)
	}
	if fs.Changed("refresh") && (f.Refresh < config.MinRefresh || f.Refresh > config.MaxRefresh) {
		return f, fmt.Errorf("--refresh: %s is outside %s–%s", f.Refresh, config.MinRefresh, config.MaxRefresh)
	}
	if f.Pprof != "" {
		if err := checkLoopback(f.Pprof); err != nil {
			return f, fmt.Errorf("--pprof: %w", err)
		}
	}
	return f, nil
}

// checkLoopback accepts only host:port addresses that resolve to loopback, so
// profiling data is never exposed on the network.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%q is not a loopback address (use 127.0.0.1:<port>)", addr)
	}
	return nil
}
