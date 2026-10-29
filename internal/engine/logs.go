package engine

import (
	"bufio"
	"bytes"
	"io"
	"time"
)

// LogSource says which output a log line came from.
type LogSource uint8

// Log sources.
const (
	Stdout LogSource = iota + 1 // container stdout, Incus console
	Stderr                      // container stderr
	System                      // the runtime's own log (Incus lxc.log / qemu.log)
)

func (s LogSource) String() string {
	switch s {
	case Stdout:
		return "out"
	case Stderr:
		return "err"
	case System:
		return "sys"
	}
	return "?"
}

// MaxLogLine caps one log line in bytes (untrusted input); longer lines are
// truncated and flagged.
const MaxLogLine = 8 << 10

// LogLine is one line of instance output. Text is raw daemon output: it may
// hold terminal escape sequences, which consumers must sanitise.
type LogLine struct {
	Time      time.Time // zero when the source has no timestamps
	Source    LogSource
	Text      string
	Truncated bool // the line was longer than MaxLogLine
}

// LogOptions selects the log lines to stream.
type LogOptions struct {
	// Tail is the number of past lines to send first; 0 sends none, a
	// negative value sends the whole history.
	Tail int
	// Since skips lines older than this time (resuming after a reconnect).
	Since time.Time
	// Follow keeps the stream open for new lines.
	Follow bool
}

// LogBuffer is the default capacity of log streams.
const LogBuffer = 256

// ScanLines calls fn for every line of r (without the line ending), cutting
// lines at MaxLogLine bytes. It stops at the first error, or when fn
// returns false, and returns nil at EOF.
func ScanLines(r io.Reader, fn func(line []byte, truncated bool) bool) error {
	br := bufio.NewReaderSize(r, 32<<10)
	var long []byte // a line spanning several reads
	for {
		chunk, err := br.ReadSlice('\n')
		if len(chunk) > 0 {
			done := chunk[len(chunk)-1] == '\n'
			if !done || len(long) > 0 {
				if room := MaxLogLine + 1 - len(long); room > 0 {
					long = append(long, chunk[:min(len(chunk), room)]...)
				}
				chunk = long
			}
			if done || (err != nil && err != bufio.ErrBufferFull) {
				line := bytes.TrimRight(chunk, "\r\n")
				truncated := len(line) > MaxLogLine
				if truncated {
					line = line[:MaxLogLine]
				}
				if !fn(line, truncated) {
					return nil
				}
				long = long[:0]
			}
		}
		switch err {
		case nil, bufio.ErrBufferFull:
		case io.EOF:
			if len(long) > 0 { // a full buffer was the last read
				line := long[:min(len(long), MaxLogLine)]
				fn(line, len(long) > MaxLogLine)
			}
			return nil
		default:
			return err
		}
	}
}
