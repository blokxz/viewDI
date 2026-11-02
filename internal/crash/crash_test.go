package crash

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type boomMsg struct{}

type model struct{}

func (model) Init() tea.Cmd { return nil }
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(boomMsg); ok {
		panic("forced in Update")
	}
	return m, nil
}
func (model) View() tea.View { return tea.NewView("") }

func TestRingKeepsLastCapacity(t *testing.T) {
	r := NewRing(func() time.Time { return time.Unix(0, 0) })
	for range Capacity + 50 {
		r.Record(tea.FocusMsg{})
	}
	r.Record(boomMsg{})
	s := r.Snapshot()
	if len(s) != Capacity {
		t.Fatalf("len = %d, want %d", len(s), Capacity)
	}
	if !strings.HasSuffix(s[len(s)-1].Type, "boomMsg") {
		t.Fatalf("last = %q", s[len(s)-1].Type)
	}
}

func TestRingNeverRecordsKeyText(t *testing.T) {
	r := NewRing(nil)
	r.Record(tea.KeyPressMsg{Code: 'x', Text: "hunter2"})
	for _, e := range r.Snapshot() {
		if strings.Contains(e.Type, "hunter2") {
			t.Fatal("key text leaked")
		}
	}
}

func TestGuardCapturesPanicInUpdateAndWritesReport(t *testing.T) {
	h := &Holder{}
	r := NewRing(nil)
	g := Guard{Model: model{}, H: h}
	r.Record(tea.FocusMsg{})
	r.Record(boomMsg{})

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic was swallowed; Bubble Tea must still see it")
			}
		}()
		g.Update(boomMsg{})
	}()

	p := h.Panic()
	if p == nil || p.Where != "Update" || p.Value != "forced in Update" || len(p.Stack) == 0 {
		t.Fatalf("panic = %+v", p)
	}
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	path, err := Write(t.TempDir(), Info{Version: "v0", Term: "xterm", Args: []string{"--mock"}}, *p, r.Snapshot(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "crash-20261102-100000.log") {
		t.Fatalf("path = %s", path)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"panic in Update: forced in Update", "version: v0", "TERM:    xterm", "crash.boomMsg", "goroutine"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("report lacks %q:\n%s", want, b)
		}
	}
}

func TestGuardPassesThroughWithoutPanic(t *testing.T) {
	h := &Holder{}
	g := Guard{Model: model{}, H: h}
	next, _ := g.Update(tea.FocusMsg{})
	if _, ok := next.(Guard); !ok {
		t.Fatalf("Update must keep the guard, got %T", next)
	}
	if h.Panic() != nil {
		t.Fatal("unexpected panic")
	}
}
