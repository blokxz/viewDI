package engine

import (
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func scan(t *testing.T, r io.Reader) (lines []string, truncated []bool) {
	t.Helper()
	err := ScanLines(r, func(l []byte, tr bool) bool {
		lines = append(lines, string(l))
		truncated = append(truncated, tr)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	return lines, truncated
}

func TestScanLines(t *testing.T) {
	long := strings.Repeat("x", MaxLogLine+100)
	huge := strings.Repeat("y", 100<<10) // spans several reader buffers
	in := "one\r\ntwo\n\n" + long + "\n" + huge + "\nlast"
	for _, r := range []io.Reader{strings.NewReader(in), iotest.OneByteReader(strings.NewReader(in))} {
		lines, tr := scan(t, r)
		if len(lines) != 6 {
			t.Fatalf("%d lines: %q", len(lines), lines)
		}
		want := []string{"one", "two", "", long[:MaxLogLine], huge[:MaxLogLine], "last"}
		wantTr := []bool{false, false, false, true, true, false}
		for i := range want {
			if lines[i] != want[i] || tr[i] != wantTr[i] {
				t.Errorf("line %d: %d bytes truncated=%v, want %d bytes truncated=%v", i, len(lines[i]), tr[i], len(want[i]), wantTr[i])
			}
		}
	}
}

func TestScanLinesFullBufferAtEOF(t *testing.T) {
	exact := strings.Repeat("z", 32<<10) // exactly one reader buffer, no newline
	lines, tr := scan(t, strings.NewReader(exact))
	if len(lines) != 1 || len(lines[0]) != MaxLogLine || !tr[0] {
		t.Fatalf("got %d lines", len(lines))
	}
}

func TestScanLinesStops(t *testing.T) {
	n := 0
	_ = ScanLines(strings.NewReader("a\nb\nc\n"), func([]byte, bool) bool { n++; return n < 2 })
	if n != 2 {
		t.Fatalf("callback ran %d times, want 2", n)
	}
}

func TestScanLinesError(t *testing.T) {
	err := ScanLines(iotest.ErrReader(io.ErrClosedPipe), func([]byte, bool) bool { return true })
	if err != io.ErrClosedPipe {
		t.Fatalf("err %v", err)
	}
}
