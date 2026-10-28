// Package trace records every tea.Msg the program processes (--trace) as JSON
// lines, and turns a trace back into input messages for deterministic replay
// in tests (docs/05-quality-plan.md §2).
//
// Recording never blocks the UI: entries go through a bounded channel to a
// writer goroutine, and entries that do not fit are counted as dropped.
package trace

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
)

const bufferSize = 4096

// Entry is one trace line. Input messages (keys, resizes) carry their payload
// so they can be replayed; other messages record only their type.
type Entry struct {
	T    time.Time `json:"t"`
	Type string    `json:"type"`
	Key  *Key      `json:"key,omitempty"`
	Size *Size     `json:"size,omitempty"`

	Dropped uint64 `json:"dropped,omitempty"` // trailer: entries lost to backpressure
}

// Key is the replayable part of a key press.
type Key struct {
	Code rune       `json:"code"`
	Mod  tea.KeyMod `json:"mod,omitempty"`
	Text string     `json:"text,omitempty"`
}

// Size is a terminal size.
type Size struct {
	W int `json:"w"`
	H int `json:"h"`
}

// Writer records messages asynchronously.
type Writer struct {
	out     io.WriteCloser
	ch      chan Entry
	done    chan error
	dropped atomic.Uint64
	now     func() time.Time
}

// Open creates the trace file at path (0600, parent directories 0700).
func Open(path string) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("open trace: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open trace: %w", err)
	}
	return NewWriter(f, time.Now), nil
}

// NewWriter starts a writer goroutine owned by the returned Writer; Close stops it.
func NewWriter(out io.WriteCloser, now func() time.Time) *Writer {
	w := &Writer{out: out, ch: make(chan Entry, bufferSize), done: make(chan error, 1), now: now}
	go w.loop()
	return w
}

func (w *Writer) loop() {
	bw := bufio.NewWriter(w.out)
	enc := json.NewEncoder(bw)
	var firstErr error
	for e := range w.ch {
		if err := enc.Encode(e); err != nil && firstErr == nil {
			firstErr = err
		}
		if len(w.ch) == 0 {
			if err := bw.Flush(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if d := w.dropped.Load(); d > 0 {
		if err := enc.Encode(Entry{T: w.now(), Type: "trace.dropped", Dropped: d}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := bw.Flush(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := w.out.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	w.done <- firstErr
}

// Record enqueues msg without blocking.
func (w *Writer) Record(msg tea.Msg) {
	if msg == nil {
		return
	}
	e := Entry{T: w.now(), Type: fmt.Sprintf("%T", msg)}
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		e.Key = &Key{Code: m.Code, Mod: m.Mod, Text: m.Text}
	case tea.WindowSizeMsg:
		e.Size = &Size{W: m.Width, H: m.Height}
	}
	select {
	case w.ch <- e:
	default:
		w.dropped.Add(1)
	}
}

// Filter is a tea.WithFilter hook that records and passes every message through.
func (w *Writer) Filter(_ tea.Model, msg tea.Msg) tea.Msg {
	w.Record(msg)
	return msg
}

// Dropped returns how many entries were lost so far.
func (w *Writer) Dropped() uint64 { return w.dropped.Load() }

// Close flushes pending entries, writes the dropped-count trailer and closes
// the file. Record must not be called after Close.
func (w *Writer) Close() error {
	close(w.ch)
	return <-w.done
}

// Read parses a trace.
func Read(r io.Reader) ([]Entry, error) {
	var out []Entry
	dec := json.NewDecoder(r)
	for dec.More() {
		var e Entry
		if err := dec.Decode(&e); err != nil {
			return out, fmt.Errorf("read trace: %w", err)
		}
		out = append(out, e)
	}
	return out, nil
}

// Replay returns the input messages of a trace (key presses and resizes), in
// order, ready to feed into a model or a teatest program.
func Replay(entries []Entry) []tea.Msg {
	var out []tea.Msg
	for _, e := range entries {
		switch {
		case e.Key != nil:
			out = append(out, tea.KeyPressMsg{Code: e.Key.Code, Mod: e.Key.Mod, Text: e.Key.Text})
		case e.Size != nil:
			out = append(out, tea.WindowSizeMsg{Width: e.Size.W, Height: e.Size.H})
		}
	}
	return out
}
