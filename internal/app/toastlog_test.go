package app

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// A warning or error toast leaves a line in the log; the rest do not.
func TestWarningAndErrorToastsAreLogged(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	d := newDriver(t, func(o *Options) { o.Logger = log })
	d.send(toast.ShowMsg{Level: theme.LevelInfo, Text: "info text"})
	d.send(toast.ShowMsg{Level: theme.LevelOK, Text: "ok text"})
	d.send(toast.ShowMsg{Level: theme.LevelWarn, Text: "warn text"})
	d.send(toast.ShowMsg{Level: theme.LevelErr, Text: "Start web: boom\x1b[31m"})
	out := buf.String()
	for _, want := range []string{"level=WARN", `text="warn text"`, "level=ERROR", `Start web: boom\x1b[31m`} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "info text") || strings.Contains(out, "ok text") || strings.Contains(out, "\x1b") {
		t.Errorf("log has what it should not:\n%q", out)
	}
}
