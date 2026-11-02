// Package crash turns a panic in the UI into a readable report (F13).
//
// Bubble Tea restores the terminal when the program panics, but the cause is
// lost with the terminal scrollback. Guard wraps the root model, recovers a
// panic in Init, Update or View, remembers it and re-panics so Bubble Tea still
// restores the terminal; Write then saves the report next to the other
// app-owned state files.
//
// The ring of recent messages is always on, so it records message types only:
// key payloads can hold what the user typed into a form (secrets included) and
// must never reach a file nobody asked for. `--trace` is the opt-in that
// records payloads.
package crash

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Capacity is how many recent messages a report carries.
const Capacity = 200

// Event is one remembered message.
type Event struct {
	T    time.Time
	Type string
}

// Ring keeps the last Capacity messages. It is safe for concurrent use.
type Ring struct {
	mu    sync.Mutex
	buf   [Capacity]Event
	next  int
	count int
	now   func() time.Time
}

// NewRing returns an empty ring; now is time.Now when nil.
func NewRing(now func() time.Time) *Ring {
	if now == nil {
		now = time.Now
	}
	return &Ring{now: now}
}

// Record remembers msg (its type only). It never blocks.
func (r *Ring) Record(msg tea.Msg) {
	if msg == nil {
		return
	}
	e := Event{T: r.now(), Type: fmt.Sprintf("%T", msg)}
	r.mu.Lock()
	r.buf[r.next] = e
	r.next = (r.next + 1) % Capacity
	if r.count < Capacity {
		r.count++
	}
	r.mu.Unlock()
}

// Filter is a tea.WithFilter hook that records and passes every message through.
func (r *Ring) Filter(_ tea.Model, msg tea.Msg) tea.Msg {
	r.Record(msg)
	return msg
}

// Snapshot returns the remembered messages, oldest first.
func (r *Ring) Snapshot() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, 0, r.count)
	start := (r.next - r.count + Capacity) % Capacity
	for i := range r.count {
		out = append(out, r.buf[(start+i)%Capacity])
	}
	return out
}

// Panic is a recovered panic with the stack of the goroutine that raised it.
type Panic struct {
	Where string // "Init", "Update" or "View"
	Value any
	Stack []byte
}

// Holder remembers the first panic caught by a Guard.
type Holder struct {
	mu sync.Mutex
	p  *Panic
}

func (h *Holder) set(p Panic) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.p == nil {
		h.p = &p
	}
}

// Panic returns the captured panic, or nil.
func (h *Holder) Panic() *Panic {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.p
}

// Guard wraps a tea.Model so a panic in it is captured in h before being
// raised again.
type Guard struct {
	Model tea.Model
	H     *Holder
}

func (g Guard) catch(where string) {
	if r := recover(); r != nil {
		g.H.set(Panic{Where: where, Value: r, Stack: debug.Stack()})
		panic(r)
	}
}

// Init implements tea.Model.
func (g Guard) Init() tea.Cmd {
	defer g.catch("Init")
	return g.Model.Init()
}

// Update implements tea.Model.
func (g Guard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	defer g.catch("Update")
	next, cmd := g.Model.Update(msg)
	return Guard{Model: next, H: g.H}, cmd
}

// View implements tea.Model.
func (g Guard) View() tea.View {
	defer g.catch("View")
	return g.Model.View()
}

// Info is the static part of a report.
type Info struct {
	Version string
	Term    string
	Args    []string
}

// Render writes the report. The stack is the only free-form part; messages are
// type names.
func Render(w io.Writer, info Info, p Panic, recent []Event, now time.Time) error {
	var b strings.Builder
	fmt.Fprintf(&b, "viewDI crash report\n\n")
	fmt.Fprintf(&b, "time:    %s\n", now.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "version: %s\n", info.Version)
	fmt.Fprintf(&b, "go:      %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "TERM:    %s\n", info.Term)
	fmt.Fprintf(&b, "args:    %s\n\n", strings.Join(info.Args, " "))
	fmt.Fprintf(&b, "panic in %s: %v\n\n%s\n", p.Where, p.Value, p.Stack)
	fmt.Fprintf(&b, "last %d messages (oldest first, types only):\n", len(recent))
	for _, e := range recent {
		fmt.Fprintf(&b, "  %s  %s\n", e.T.UTC().Format("15:04:05.000"), e.Type)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// Write saves a report as dir/crash-<ts>.log (0600, dir 0700) and returns its path.
func Write(dir string, info Info, p Panic, recent []Event, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("crash report: %w", err)
	}
	path := filepath.Join(dir, "crash-"+now.Format("20060102-150405")+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("crash report: %w", err)
	}
	if err := Render(f, info, p, recent, now); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("crash report: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("crash report: %w", err)
	}
	return path, nil
}
