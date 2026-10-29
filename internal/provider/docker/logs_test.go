package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

func frame(kind byte, data string) []byte {
	var h [8]byte
	h[0] = kind
	binary.BigEndian.PutUint32(h[4:], uint32(len(data)))
	return append(h[:], data...)
}

type got struct {
	src  engine.LogSource
	text string
	tr   bool
}

func runDemux(t *testing.T, stream []byte) ([]got, error) {
	t.Helper()
	var out []got
	err := demux(bytes.NewReader(stream), func(src engine.LogSource, l []byte, tr bool) bool {
		out = append(out, got{src, string(l), tr})
		return true
	})
	return out, err
}

func TestDemux(t *testing.T) {
	long := strings.Repeat("x", 40000)
	var s []byte
	s = append(s, frame(frameStdout, "hel")...)
	s = append(s, frame(frameStderr, "err one\nerr ")...)
	s = append(s, frame(frameStdout, "lo\r\nworld\n")...)
	s = append(s, frame(frameStderr, "two\n")...)
	s = append(s, frame(frameStdout, long[:5000])...)
	s = append(s, frame(frameStdout, long[5000:]+"\n")...)
	s = append(s, frame(frameStdin, "ignored")...)
	s = append(s, frame(frameStdout, "no newline at end")...)
	lines, err := runDemux(t, s)
	if err != nil {
		t.Fatal(err)
	}
	want := []got{
		{engine.Stderr, "err one", false},
		{engine.Stdout, "hello", false},
		{engine.Stdout, "world", false},
		{engine.Stderr, "err two", false},
		{engine.Stdout, long[:maxStamped], true},
		{engine.Stdout, "no newline at end", false},
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines: %+v", len(lines), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = {%v %.20q… %v}, want {%v %.20q… %v}", i, lines[i].src, lines[i].text, lines[i].tr, want[i].src, want[i].text, want[i].tr)
		}
	}
}

func TestDemuxDaemonError(t *testing.T) {
	s := append(frame(frameStdout, "ok\n"), frame(frameSystem, "oops: log driver failed\n")...)
	lines, err := runDemux(t, s)
	if len(lines) != 1 || err == nil || !strings.Contains(err.Error(), "log driver failed") {
		t.Fatalf("lines %v err %v", lines, err)
	}
	if _, err := runDemux(t, frame(9, "x")); err == nil {
		t.Fatal("unknown frame type accepted")
	}
}

func TestLogLineTimestamp(t *testing.T) {
	l := logLine(engine.Stdout, []byte("2026-10-29T12:00:01.5Z hello world"), false)
	if l.Text != "hello world" || !l.Time.Equal(time.Date(2026, 10, 29, 12, 0, 1, 5e8, time.UTC)) {
		t.Fatalf("%+v", l)
	}
	if l := logLine(engine.Stdout, []byte("no stamp here"), false); l.Text != "no stamp here" || !l.Time.IsZero() {
		t.Fatalf("%+v", l)
	}
	if l := logLine(engine.Stdout, bytes.Repeat([]byte("y"), engine.MaxLogLine+10), false); len(l.Text) != engine.MaxLogLine || !l.Truncated {
		t.Fatalf("unstamped long line: %d bytes, truncated %v", len(l.Text), l.Truncated)
	}
}

func TestLogsFromFixture(t *testing.T) {
	_, a := serve(t, fixtureSets(t)[0])
	ctx := context.Background()
	s, err := a.Logs(ctx, "9f3ca1b2c3d4e5f60718293a4b5c6d7e8f9012345678901234567890abcdef12", engine.LogOptions{Tail: 10})
	if err != nil {
		t.Fatal(err)
	}
	var lines []engine.LogLine
	for l := range s.C() {
		lines = append(lines, l)
	}
	if s.Err() != nil || len(lines) != 5 {
		t.Fatalf("%d lines, err %v", len(lines), s.Err())
	}
	if lines[0].Text != "\x1b[32mINFO\x1b[0m server listening on :8080" || lines[0].Time.Second() != 0 {
		t.Errorf("first line %+v", lines[0])
	}
	if lines[2].Source != engine.Stderr || !strings.HasPrefix(lines[2].Text, "ERROR") {
		t.Errorf("third line %+v", lines[2])
	}
}

func TestWatchStatsFromFixture(t *testing.T) {
	_, a := serve(t, fixtureSets(t)[0])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := a.WatchStats(ctx, "9f3ca1b2c3d4e5f60718293a4b5c6d7e8f9012345678901234567890abcdef12", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var samples []engine.Sample
	for smp := range s.C() {
		samples = append(samples, smp)
	}
	if len(samples) != 3 || s.Err() != nil {
		t.Fatalf("%d samples, err %v", len(samples), s.Err())
	}
	if len(samples[0].Ifaces) != 1 || samples[0].Ifaces[0] != (engine.IfaceCounters{Name: "eth0", Rx: 7_340_032_000, Tx: 2_400_000_000}) {
		t.Errorf("ifaces %+v", samples[0].Ifaces)
	}
	if samples[2].CPU-samples[0].CPU != 160*time.Millisecond {
		t.Errorf("cpu delta %v", samples[2].CPU-samples[0].CPU)
	}
}

func TestInspectEnvAndImage(t *testing.T) {
	_, a := serve(t, fixtureSets(t)[0])
	in, err := a.Inspect(context.Background(), "9f3ca1b2c3d4e5f60718293a4b5c6d7e8f9012345678901234567890abcdef12")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Env) != 2 || !strings.HasPrefix(in.Env[1], "DB_PASSWORD=") {
		t.Errorf("env %v", in.Env)
	}
	if x := in.Extra.(*engine.DockerExtra); !strings.HasPrefix(x.ImageID, "sha256:") {
		t.Errorf("image ID %q", x.ImageID)
	}
}

func FuzzDemux(f *testing.F) {
	f.Add(append(frame(frameStdout, "a\nb"), frame(frameStderr, "c\n")...))
	f.Add([]byte{1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = demux(bytes.NewReader(data), func(_ engine.LogSource, l []byte, _ bool) bool {
			if len(l) > maxStamped {
				t.Fatalf("line of %d bytes", len(l))
			}
			_ = logLine(engine.Stdout, l, false)
			return true
		})
	})
}
