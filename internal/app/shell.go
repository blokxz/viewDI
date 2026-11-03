package app

import (
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// A shell is not destructive, but it can change anything inside the
// machine, so it is off in --read-only. viewDI never builds a shell string:
// the argv is fixed, with the machine's ID or name (validated) as the only
// variable part, and the terminal is handed over like the engine upgrade.

// safeIdent is what an instance ID, name or Incus project may contain before
// it goes into an argv: no leading dash (an option), no spaces, no controls.
var safeIdent = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// shellScript starts bash when the image has it, else sh. It is a constant.
const shellScript = "command -v bash >/dev/null 2>&1 && exec bash || exec sh"

// shellArgv is the command that opens a shell in in.
func shellArgv(in engine.Instance) ([]string, error) {
	if in.State != engine.StateRunning {
		return nil, errors.New("it is not running")
	}
	switch in.Engine {
	case engine.Docker:
		if !safeIdent.MatchString(in.ID) {
			return nil, errors.New("its ID is not safe to pass to docker")
		}
		return []string{"docker", "exec", "-it", in.ID, "sh", "-c", shellScript}, nil
	case engine.Incus:
		if !safeIdent.MatchString(in.Name) || in.Project != "" && !safeIdent.MatchString(in.Project) {
			return nil, errors.New("its name is not safe to pass to incus")
		}
		argv := []string{"incus", "exec", in.Name}
		if in.Project != "" {
			argv = append(argv, "--project", in.Project)
		}
		return append(argv, "--", "sh", "-c", shellScript), nil
	}
	return nil, errors.New("unknown engine")
}

// shellDoneMsg reports how a shell ended.
type shellDoneMsg struct {
	name string
	err  error
}

// openShell hands the terminal to a shell in in.
func (m Model) openShell(in engine.Instance) (tea.Model, tea.Cmd) {
	name := textsafe.Clean(in.Name)
	if m.opts.ReadOnly {
		return m, toast.Show(theme.LevelWarn, "Read-only mode: opening a shell is disabled")
	}
	argv, err := shellArgv(in)
	if err != nil {
		return m, toast.Show(theme.LevelWarn, "Cannot open a shell in "+name+": "+err.Error())
	}
	if m.opts.Mock != "" { // the machines are made up
		return m, toast.Show(theme.LevelInfo, "--mock: would run "+strings.Join(argv[:len(argv)-1], " "))
	}
	done := func(err error) tea.Msg { return shellDoneMsg{name: name, err: err} }
	if m.opts.RunProcess != nil {
		return m, m.opts.RunProcess(argv, done)
	}
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // a fixed argv with a validated ID, no shell
	cmd.Env = append(os.Environ(), m.opts.ShellEnv[in.Engine]...)
	return m, tea.ExecProcess(cmd, done)
}

func (m Model) shellDone(msg shellDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, toast.Show(theme.LevelInfo, "The shell in "+msg.name+" ended: "+textsafe.Clean(msg.err.Error()))
	}
	return m, nil
}
