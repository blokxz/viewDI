package incus

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/blokxz/viewDI/internal/engine"
)

// Log polling: Incus has no follow endpoint for the console ring buffer or
// the runtime log, so both are fetched every logPoll and diffed.
const (
	logPoll     = time.Second
	maxLogFetch = 4 << 20 // bytes read per fetch (untrusted input)
	systemTail  = 3       // runtime-log lines sent with the history
	keepLines   = 2000    // lines remembered to find the overlap
)

// Logs implements engine.Engine with the console log (stdout) and the
// runtime log, lxc.log or qemu.log (system). The first fetch happens before
// Logs returns, so a missing instance or a permission error is reported
// directly.
func (a *Adapter) Logs(ctx context.Context, id string, opts engine.LogOptions) (*engine.Stream[engine.LogLine], error) {
	project, name := a.split(id)
	ictx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	inst, _, err := a.client(ictx, project).GetInstance(name)
	if err != nil {
		return nil, classify(ictx, "logs "+id, err)
	}
	logfile := "lxc.log"
	if inst.Type == string(api.InstanceTypeVM) {
		logfile = "qemu.log"
	}
	p := &logPoller{
		a: a, project: project, name: name, logfile: logfile,
		console: &differ{src: engine.Stdout}, system: &differ{src: engine.System},
	}
	first, err := p.fetch(ictx, true)
	if err != nil {
		return nil, err
	}
	if opts.Tail >= 0 {
		first = tail(first, opts.Tail)
	}
	s := engine.NewStream[engine.LogLine](engine.LogBuffer)
	go func() {
		for _, l := range first {
			if !s.Send(ctx, l) {
				s.Close(ctx.Err())
				return
			}
		}
		if !opts.Follow {
			s.Close(nil)
			return
		}
		for {
			select {
			case <-ctx.Done():
				s.Close(ctx.Err())
				return
			case <-time.After(logPoll):
			}
			fctx, cancel := engine.WithDefaultTimeout(ctx)
			lines, err := p.fetch(fctx, false)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					err = ctx.Err()
				}
				s.Close(err)
				return
			}
			for _, l := range lines {
				if !s.Send(ctx, l) {
					s.Close(ctx.Err())
					return
				}
			}
		}
	}()
	return s, nil
}

// tail keeps the last n console lines and at most systemTail runtime lines.
func tail(lines []engine.LogLine, n int) []engine.LogLine {
	var sys, out []engine.LogLine
	for _, l := range lines {
		if l.Source == engine.System {
			sys = append(sys, l)
		} else {
			out = append(out, l)
		}
	}
	sys = sys[max(0, len(sys)-min(n, systemTail)):]
	out = out[max(0, len(out)-n):]
	return append(sys, out...)
}

func (a *Adapter) split(id string) (project, name string) {
	project, name, ok := strings.Cut(id, "/")
	if !ok {
		return a.defaultProject(), id
	}
	return project, name
}

type logPoller struct {
	a               *Adapter
	project, name   string
	logfile         string
	console, system *differ
}

// fetch reads both logs and returns their new lines: the runtime log first
// on the first fetch (it explains a failed start), then the console. A log
// that cannot be read is skipped unless both fail.
func (p *logPoller) fetch(ctx context.Context, first bool) ([]engine.LogLine, error) {
	base := "/1.0/instances/" + url.PathEscape(p.name)
	sys, serr := p.a.raw(ctx, base+"/logs/"+url.PathEscape(p.logfile), p.project)
	con, cerr := p.a.raw(ctx, base+"/console", p.project)
	switch {
	case serr != nil && cerr != nil:
		return nil, cerr
	case engine.Classify(cerr) == engine.ClassNotFound && !first:
		return nil, cerr // the instance is gone
	}
	now := time.Now().UTC()
	var out []engine.LogLine
	if serr == nil {
		out = append(out, p.system.next(sys, now, first)...)
	}
	if cerr == nil {
		out = append(out, p.console.next(con, now, first)...)
	}
	return out, nil
}

// raw GETs path in project and returns at most maxLogFetch bytes.
func (a *Adapter) raw(ctx context.Context, path, project string) ([]byte, error) {
	if project == "" {
		project = a.defaultProject()
	}
	u := "http://unix.socket" + path + "?project=" + url.QueryEscape(project)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.srv.DoHTTP(req)
	if err != nil {
		return nil, classify(ctx, "logs "+project+"/"+path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLogFetch))
	if err != nil {
		return nil, classify(ctx, "logs "+path, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusNotFound:
		return nil, engine.NotFound("logs", project+"/"+path)
	case http.StatusForbidden, http.StatusUnauthorized:
		return nil, engine.Errorf(engine.ClassPermission, "logs", fmt.Errorf("%s: HTTP %d", path, resp.StatusCode))
	}
	return nil, engine.Errorf(engine.ClassTransient, "logs", fmt.Errorf("%s: HTTP %d", path, resp.StatusCode))
}

// differ turns successive snapshots of a log (a ring buffer that may
// rotate) into new lines. Only complete lines count: a partial last line is
// picked up once its newline arrives.
type differ struct {
	src  engine.LogSource
	prev []string
}

func (d *differ) next(content []byte, now time.Time, first bool) []engine.LogLine {
	var cur []string
	var truncated []bool
	if i := bytes.LastIndexByte(content, '\n'); i >= 0 {
		content = content[:i+1]
	} else {
		content = nil
	}
	_ = engine.ScanLines(bytes.NewReader(content), func(l []byte, tr bool) bool {
		cur = append(cur, string(l))
		truncated = append(truncated, tr)
		return true
	})
	start := overlap(d.prev, cur)
	d.prev = cur[max(0, len(cur)-keepLines):]
	out := make([]engine.LogLine, 0, len(cur)-start)
	for i := start; i < len(cur); i++ {
		l := engine.LogLine{Source: d.src, Text: cur[i], Truncated: truncated[i]}
		if !first {
			l.Time = now // history has no timestamps; new lines get the poll time
		}
		out = append(out, l)
	}
	return out
}

// overlap returns the index in cur after the lines already seen in prev:
// the last line of prev, confirmed by up to 4 lines before it.
func overlap(prev, cur []string) int {
	if len(prev) == 0 {
		return 0
	}
	last := prev[len(prev)-1]
	for j := len(cur) - 1; j >= 0; j-- {
		if cur[j] != last {
			continue
		}
		ok := true
		for k := 1; k <= 4 && j-k >= 0 && len(prev)-1-k >= 0; k++ {
			if cur[j-k] != prev[len(prev)-1-k] {
				ok = false
				break
			}
		}
		if ok {
			return j + 1
		}
	}
	return 0 // rotated or cleared: everything is new
}
