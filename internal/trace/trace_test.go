package trace

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

func fixedNow() time.Time { return time.Unix(1700000000, 0).UTC() }

func TestRecordAndReplay(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(nopCloser{&buf}, fixedNow)
	in := []tea.Msg{
		tea.WindowSizeMsg{Width: 120, Height: 40},
		tea.KeyPressMsg{Code: 'T', Text: "T"},
		tea.FocusMsg{},
		tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl},
	}
	for _, m := range in {
		w.Filter(nil, m)
	}
	w.Record(nil)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 || entries[2].Type != "tea.FocusMsg" {
		t.Fatalf("entries = %+v", entries)
	}
	out := Replay(entries)
	want := []tea.Msg{in[0], in[1], in[3]}
	if len(out) != len(want) {
		t.Fatalf("replay = %v", out)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Errorf("replay[%d] = %#v, want %#v", i, out[i], want[i])
		}
	}
	if out[2].(tea.KeyPressMsg).String() != "ctrl+c" {
		t.Errorf("replayed key = %q", out[2].(tea.KeyPressMsg).String())
	}
}

// blockingWriter blocks every write until released, to force backpressure.
type blockingWriter struct{ release chan struct{} }

func (b blockingWriter) Write(p []byte) (int, error) { <-b.release; return len(p), nil }
func (blockingWriter) Close() error                  { return nil }

func TestRecordNeverBlocks(t *testing.T) {
	bw := blockingWriter{release: make(chan struct{})}
	w := NewWriter(bw, fixedNow)
	start := time.Now()
	for range bufferSize * 3 {
		w.Record(tea.FocusMsg{})
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Record blocked for %s", d)
	}
	if w.Dropped() == 0 {
		t.Fatal("expected dropped entries under backpressure")
	}
	close(bw.release)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCreatesPrivateFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state", "trace.log")
	w, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	w.Record(tea.BlurMsg{})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 || fi.Size() == 0 {
		t.Fatalf("stat = %v, %v", fi, err)
	}
}
