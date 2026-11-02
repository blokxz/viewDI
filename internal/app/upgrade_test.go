package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func upgradeDriver(t *testing.T, ran *[][]string, result error, mutates ...func(*Options)) (*driver, *stubUpdater) {
	t.Helper()
	upd := &stubUpdater{}
	run := func(argv []string, done func(error) tea.Msg) tea.Cmd {
		*ran = append(*ran, argv)
		return func() tea.Msg { return done(result) }
	}
	d := newDriver(t, append([]func(*Options){withEngines(nil), withUpdater(upd), func(o *Options) { o.RunProcess = run }}, mutates...)...)
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.send(UpdatesMsg{Report: homelabReport(t)})
	d.press("6")
	return d, upd
}

func TestUpgradeAsksThenRunsTheExactCommand(t *testing.T) {
	var ran [][]string
	d, upd := upgradeDriver(t, &ran, nil)
	before := upd.checks
	d.press("u")
	screen := d.screen()
	for _, want := range []string{"Upgrade Docker?", "sudo apt install --only-upgrade docker-ce", "never sees it"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("dialog lacks %q:\n%s", want, screen)
		}
	}
	if len(ran) != 0 {
		t.Fatalf("ran before the answer: %v", ran)
	}
	d.press("y")
	if len(ran) != 1 || strings.Join(ran[0], " ") != "sudo apt install --only-upgrade docker-ce" {
		t.Fatalf("ran %v", ran)
	}
	if got := d.lastToast(); !strings.Contains(got, "upgrade finished") {
		t.Fatalf("toast %q", got)
	}
	if upd.checks != before+1 {
		t.Fatalf("checks %d → %d: the update check must run again", before, upd.checks)
	}
}

func TestUpgradeCanBeDeclinedAndFailuresAreShown(t *testing.T) {
	var ran [][]string
	d, _ := upgradeDriver(t, &ran, errors.New("exit status 100"))
	d.press("u", "esc")
	if len(ran) != 0 {
		t.Fatalf("ran after esc: %v", ran)
	}
	d.press("u", "y")
	if got := d.lastToast(); len(ran) != 1 || !strings.Contains(got, "did not finish") || !strings.Contains(got, "exit status 100") {
		t.Fatalf("ran %v, toast %q", ran, got)
	}
}

func TestUpgradeRefusals(t *testing.T) {
	var ran [][]string

	d, _ := upgradeDriver(t, &ran, nil, func(o *Options) { o.ReadOnly = true })
	d.press("u")
	if got := d.lastToast(); !strings.Contains(got, "Read-only") || strings.Contains(d.screen(), "Upgrade Docker?") {
		t.Fatalf("read-only: toast %q", got)
	}

	d2, _ := upgradeDriver(t, &ran, nil, func(o *Options) { o.Mock = "homelab" })
	d2.press("u")
	if got := d2.lastToast(); !strings.Contains(got, "--mock: would run sudo apt install") {
		t.Fatalf("mock: toast %q", got)
	}

	// An image row is not an engine.
	d3, _ := upgradeDriver(t, &ran, nil)
	d3.press("down", "down", "u")
	if got := d3.lastToast(); !strings.Contains(got, "engine row") {
		t.Fatalf("image row: toast %q", got)
	}

	// A hint that is not one of the known templates is never run.
	rep := homelabReport(t)
	for i := range rep.Engines {
		rep.Engines[i].Hint = "sudo apt install --only-upgrade docker-ce; rm -rf ~"
	}
	d4, _ := upgradeDriver(t, &ran, nil)
	d4.send(UpdatesMsg{Report: rep})
	d4.press("u")
	if got := d4.lastToast(); !strings.Contains(got, "No command to run") || strings.Contains(d4.screen(), "Upgrade Docker?") {
		t.Fatalf("unsafe hint: toast %q", got)
	}
	if len(ran) != 0 {
		t.Fatalf("something ran: %v", ran)
	}
}
