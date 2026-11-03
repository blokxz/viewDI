package app

import (
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/theme"
	activityview "github.com/blokxz/viewDI/internal/ui/views/activity"
	"github.com/blokxz/viewDI/internal/updates"
)

// The u key of the Updates view upgrades an engine. It never runs anything
// unseen: the exact command is shown in a confirmation, then viewDI hands the
// terminal over to it (tea.ExecProcess) so sudo can ask for its password on the
// terminal itself. viewDI does not see the password, does not use a shell, and
// only runs the commands UpgradeArgs recognises.

// upgradeConfirmedMsg is the answer to the confirmation.
type upgradeConfirmedMsg struct {
	engine engine.ID
	args   []string
}

// upgradeDoneMsg reports how the command ended.
type upgradeDoneMsg struct {
	engine engine.ID
	err    error
}

func (m Model) upgradeEngine(msg activityview.UpgradeMsg) (tea.Model, tea.Cmd) {
	st := msg.Engine
	if st == nil {
		return m, toast.Show(theme.LevelInfo, "u upgrades an engine row; images use p, f and c")
	}
	label := st.Engine.Label()
	switch {
	case !st.HasUpdate():
		return m, toast.Show(theme.LevelInfo, label+" is up to date")
	case m.opts.ReadOnly:
		return m, toast.Show(theme.LevelWarn, "Read-only mode: upgrading is disabled")
	}
	args, err := updates.UpgradeArgs(st.Hint)
	if err != nil {
		return m, toast.Show(theme.LevelInfo, "No command to run for this source ("+textsafe.Clean(st.Source)+"): "+textsafe.Clean(st.Hint))
	}
	cmdline := strings.Join(args, " ")
	if m.opts.Mock != "" { // the update sources are made up: never run anything
		return m, toast.Show(theme.LevelInfo, "--mock: would run "+cmdline)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s → %s (%s)\n\n  %s\n\n", label, textsafe.Clean(st.Installed), textsafe.Clean(st.Available), textsafe.Clean(st.Source), cmdline)
	b.WriteString("viewDI pauses and runs this in your terminal. sudo may ask for your password there; viewDI never sees it.\n")
	if updates.FullUpgrade(args) {
		b.WriteString("\nThis upgrades EVERY package on the system, not only " + strings.ToLower(label) + " (Arch does not support partial upgrades).\n")
	}
	b.WriteString("\nThe daemon may restart; running instances can stop depending on its settings.")
	dialog := modal.NewConfirm(m.keys, "Upgrade "+label+"?", b.String(), "Upgrade", true, upgradeConfirmedMsg{engine: st.Engine, args: args})
	return m, modal.Open(dialog)
}

func (m Model) runUpgrade(msg upgradeConfirmedMsg) (tea.Model, tea.Cmd) {
	if m.opts.ReadOnly { // the mode can change between the question and the answer
		return m, toast.Show(theme.LevelWarn, "Read-only mode: upgrading is disabled")
	}
	done := func(err error) tea.Msg { return upgradeDoneMsg{engine: msg.engine, err: err} }
	if m.opts.RunProcess != nil {
		return m, m.opts.RunProcess(msg.args, done)
	}
	return m, tea.ExecProcess(exec.Command(msg.args[0], msg.args[1:]...), done)
}

func (m Model) upgradeDone(msg upgradeDoneMsg) (tea.Model, tea.Cmd) {
	label := msg.engine.Label()
	if msg.err != nil {
		return m, toast.Show(theme.LevelWarn, "The "+label+" upgrade did not finish: "+textsafe.Clean(msg.err.Error())+" (see the output above)")
	}
	model, check := m.checkUpdates()
	return model, tea.Batch(check, toast.Show(theme.LevelOK, label+" upgrade finished; checking again"))
}
