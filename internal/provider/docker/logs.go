package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"github.com/blokxz/viewDI/internal/engine"
)

// Frame stream types of the multiplexed log format (stdcopy).
const (
	frameStdin  = 0
	frameStdout = 1
	frameStderr = 2
	frameSystem = 3 // a daemon error, sent instead of output
)

// Logs implements engine.Engine. Containers without a TTY multiplex stdout
// and stderr into frames; TTY containers send one raw stream. Lines carry
// the daemon's timestamps.
func (a *Adapter) Logs(ctx context.Context, id string, opts engine.LogOptions) (*engine.Stream[engine.LogLine], error) {
	ictx, cancel := engine.WithDefaultTimeout(ctx)
	res, err := a.cli.ContainerInspect(ictx, id, client.ContainerInspectOptions{})
	cancel()
	if err != nil {
		return nil, classify("logs "+short(id), err)
	}
	tty := res.Container.Config != nil && res.Container.Config.Tty
	lo := client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Timestamps: true, Follow: opts.Follow, Tail: "all"}
	if opts.Tail >= 0 {
		lo.Tail = strconv.Itoa(opts.Tail)
	}
	if !opts.Since.IsZero() {
		lo.Since = opts.Since.UTC().Format(time.RFC3339Nano)
	}
	rc, err := a.cli.ContainerLogs(ctx, id, lo)
	if err != nil {
		return nil, classify("logs "+short(id), err)
	}
	s := engine.NewStream[engine.LogLine](engine.LogBuffer)
	go func() {
		defer rc.Close()
		send := func(src engine.LogSource, line []byte, truncated bool) bool {
			return s.Send(ctx, logLine(src, line, truncated))
		}
		if tty {
			err = engine.ScanLines(rc, func(l []byte, tr bool) bool { return send(engine.Stdout, l, tr) })
		} else {
			err = demux(rc, send)
		}
		switch {
		case ctx.Err() != nil:
			s.Close(ctx.Err())
		case err == nil || errors.Is(err, io.EOF):
			s.Close(nil) // no follow, or the container stopped
		default:
			s.Close(classify("logs "+short(id), err))
		}
	}()
	return s, nil
}

// logLine splits the daemon timestamp off a line.
func logLine(src engine.LogSource, line []byte, truncated bool) engine.LogLine {
	out := engine.LogLine{Source: src, Truncated: truncated}
	if i := bytes.IndexByte(line, ' '); i > 0 && i <= len("2006-01-02T15:04:05.000000000Z07:00") {
		if t, err := time.Parse(time.RFC3339Nano, string(line[:i])); err == nil {
			out.Time = t.UTC()
			line = line[i+1:]
		}
	}
	if len(line) > engine.MaxLogLine {
		line, out.Truncated = line[:engine.MaxLogLine], true
	}
	out.Text = string(line)
	return out
}

// demux reads a multiplexed log stream and calls send for every complete
// line, in stream order. Frames need not be line-aligned: partial lines
// are kept per stream until their newline arrives.
func demux(r io.Reader, send func(engine.LogSource, []byte, bool) bool) error {
	var hdr [8]byte
	buf := make([]byte, 32<<10)
	lines := map[byte]*assembler{frameStdout: {src: engine.Stdout}, frameStderr: {src: engine.Stderr}}
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				for _, k := range []byte{frameStdout, frameStderr} {
					if !lines[k].flush(send) {
						return nil
					}
				}
				return nil
			}
			return err
		}
		kind, size := hdr[0], int64(binary.BigEndian.Uint32(hdr[4:]))
		if kind == frameSystem {
			msg, _ := io.ReadAll(io.LimitReader(r, min(size, engine.MaxLogLine)))
			return fmt.Errorf("daemon: %s", strings.TrimSpace(string(msg)))
		}
		asm := lines[kind]
		for size > 0 {
			n, err := io.ReadFull(r, buf[:min(size, int64(len(buf)))])
			size -= int64(n)
			if asm != nil && !asm.write(buf[:n], send) {
				return nil
			}
			if err != nil {
				return err
			}
		}
		if asm == nil && kind != frameStdin {
			return fmt.Errorf("unknown log frame type %d", kind)
		}
	}
}

// assembler cuts one output stream into lines of at most MaxLogLine bytes
// (plus a timestamp).
type assembler struct {
	src  engine.LogSource
	buf  []byte
	over bool // the current line overflowed: drop until newline
}

const maxStamped = engine.MaxLogLine + 64

func (a *assembler) write(p []byte, send func(engine.LogSource, []byte, bool) bool) bool {
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk = p[:i]
		}
		if room := maxStamped - len(a.buf); len(chunk) > room {
			a.over = true
			chunk = chunk[:max(room, 0)]
		}
		a.buf = append(a.buf, chunk...)
		if i < 0 {
			return true
		}
		line := bytes.TrimRight(a.buf, "\r")
		ok := send(a.src, line, a.over)
		a.buf, a.over = a.buf[:0], false
		if !ok {
			return false
		}
		p = p[i+1:]
	}
	return true
}

func (a *assembler) flush(send func(engine.LogSource, []byte, bool) bool) bool {
	if len(a.buf) == 0 {
		return true
	}
	line := a.buf
	a.buf = nil
	return send(a.src, line, a.over)
}
