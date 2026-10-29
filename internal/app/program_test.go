package app

import (
	"bytes"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"go.uber.org/goleak"

	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/themes"
)

func TestMain(m *testing.M) {
	time.Local = time.UTC // golden frames print local times
	goleak.VerifyTestMain(m,
		// teatest's SIGINT watcher (teatest.go, NewTestModel) blocks on a signal
		// channel for the life of the process by design; it is not ours.
		goleak.IgnoreTopFunction("github.com/charmbracelet/x/exp/teatest/v2.NewTestModel.func2"),
	)
}

// TestProgramKeyboardOnly drives the real Bubble Tea program: every view is
// reachable with keys only and q exits cleanly (Stage 0 acceptance), with no
// goroutine left behind (checked by TestMain).
func TestProgramKeyboardOnly(t *testing.T) {
	reg, _ := theme.LoadRegistry(themes.FS, "")
	th, _ := reg.Resolve()
	m := New(Options{Themes: reg, Theme: th, Keys: keys.MustDefault()})
	tm := teatest.NewTestModel(t, m,
		teatest.WithInitialTermSize(120, 40),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.Ascii)),
	)
	wait := func(s string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte(s)) },
			teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	}
	// The renderer only repaints changed cells, so wait on text unique to each view.
	wait("waiting for the supervisor")
	for _, step := range []struct{ key, want string }{
		{"2", "No samples yet"},
		{"3", "o/O sort · P project"},
		{"4", "reads snapshots only"},
		{"5", "No jobs yet"},
		{"6", "registry digest checks"},
		{"1", "Shortcuts"},
	} {
		tm.Type(step.key)
		wait(step.want)
	}
	tm.Type("q")
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
	if got := tm.FinalModel(t).(Model).ActiveView(); got != 1 {
		t.Fatalf("final view = %d", got)
	}
}
