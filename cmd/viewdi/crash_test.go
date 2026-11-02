package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/crash"
)

type panicModel struct{}

func (panicModel) Init() tea.Cmd                       { return nil }
func (panicModel) Update(tea.Msg) (tea.Model, tea.Cmd) { panic("forced in Update") }
func (panicModel) View() tea.View                      { return tea.NewView("") }

// F13: a panic in Update leaves a readable crash-*.log and a non-zero exit.
func TestReportCrashWritesReadableLog(t *testing.T) {
	h := &crash.Holder{}
	ring := crash.NewRing(nil)
	ring.Record(tea.FocusMsg{})
	func() {
		defer func() { _ = recover() }()
		crash.Guard{Model: panicModel{}, H: h}.Update(tea.FocusMsg{})
	}()

	dir := t.TempDir()
	var stderr bytes.Buffer
	code := reportCrash(h, ring, config.Paths{StateDir: dir}, &stderr, slog.New(slog.DiscardHandler))
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "crash-*.log"))
	if len(files) != 1 {
		t.Fatalf("crash logs = %v", files)
	}
	if !strings.Contains(stderr.String(), files[0]) {
		t.Fatalf("stderr %q does not name %s", stderr.String(), files[0])
	}
	b, _ := os.ReadFile(files[0])
	if !strings.Contains(string(b), "panic in Update: forced in Update") {
		t.Fatalf("log:\n%s", b)
	}
}
