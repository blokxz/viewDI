// Package pkgmgr asks the distribution's package manager which engine
// version is installed and which one could be installed. It only reads:
// it never refreshes a package database (pacman is therefore only as fresh
// as the last sync the user ran) and never runs privileged commands. The
// upgrade itself is left to the user, through the hint.
package pkgmgr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/updates"
)

// Limits on every command.
const (
	Timeout   = 20 * time.Second
	MaxOutput = 1 << 20 // bytes kept of a command's output
)

// Result is the outcome of a command that ran: its (capped) standard
// output and exit code.
type Result struct {
	Out  []byte
	Code int
}

// Runner runs a command. err is only for a command that could not run or
// did not finish (missing binary, timeout); a non-zero exit is Result.Code.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
}

// Exec runs real commands with LC_ALL=C (stable, parsable output), the
// Timeout and the MaxOutput cap. Standard input is empty, so a command can
// never wait for an answer.
type Exec struct{}

// Run implements Runner.
func (Exec) Run(ctx context.Context, name string, args ...string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out := &capped{max: MaxOutput}
	cmd.Stdout = out
	cmd.Stdin = nil
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return Result{}, fmt.Errorf("%s: %w", name, ctx.Err())
	case errors.As(err, &exit):
		return Result{Out: out.Bytes(), Code: exit.ExitCode()}, nil
	case err != nil:
		return Result{}, fmt.Errorf("%s: %w", name, err)
	}
	return Result{Out: out.Bytes()}, nil
}

// capped keeps the first max bytes written and discards the rest.
type capped struct {
	bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.Len(); room > 0 {
		c.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

var _ io.Writer = (*capped)(nil)

// Packages are the package names each engine ships under, in the order
// they are tried.
var Packages = map[engine.ID][]string{
	engine.Docker: {"docker-ce", "docker.io", "docker", "moby-engine"},
	engine.Incus:  {"incus"},
}

// manager is one supported package manager.
type manager struct {
	name   string
	binary string // detected on PATH
	lookup func(ctx context.Context, r Runner, pkgs []string) (updates.Package, error)
}

// managers in detection order.
var managers = []manager{
	{"pacman", "pacman", lookupPacman},
	{"apt", "apt-cache", lookupApt},
	{"zypper", "zypper", lookupZypper},
	{"dnf", "dnf", lookupDnf},
}

// Source is a Packages backed by the host's package manager.
type Source struct {
	m manager
	r Runner
}

// Detect returns the source for the first package manager found on PATH
// (through lookPath, exec.LookPath in production), or nil when none is.
func Detect(lookPath func(string) (string, error), r Runner) *Source {
	for _, m := range managers {
		if _, err := lookPath(m.binary); err == nil {
			return &Source{m: m, r: r}
		}
	}
	return nil
}

// Manager names the detected package manager.
func (s *Source) Manager() string { return s.m.name }

// Lookup implements updates.Packages. It returns updates.ErrNotManaged
// when none of the engine's packages is installed.
func (s *Source) Lookup(ctx context.Context, id engine.ID) (updates.Package, error) {
	pkgs := Packages[id]
	if len(pkgs) == 0 {
		return updates.Package{}, updates.ErrNotManaged
	}
	p, err := s.m.lookup(ctx, s.r, pkgs)
	if err != nil {
		return updates.Package{}, fmt.Errorf("%s: %w", s.m.name, err)
	}
	p.Manager = s.m.name
	return p, nil
}

// clean sanitises one field of untrusted command output.
func clean(s string) string {
	s = textsafe.Clean(strings.TrimSpace(s))
	if len(s) > 128 {
		s = s[:128]
	}
	return s
}

// fields splits a line of untrusted output into sanitised fields.
func fields(line string) []string { return strings.Fields(textsafe.Clean(line)) }
