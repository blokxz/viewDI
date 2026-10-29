package fake

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"syscall"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

// LogProfile drives the synthetic log of one instance.
type LogProfile struct {
	Rate     float64 // lines per second
	ErrEvery int     // every ErrEvery-th line is an error on stderr (0 = none)
}

// maxLogBurst bounds the lines one follow tick sends, so a stream that was
// held back (hung daemon) catches up gradually.
const maxLogBurst = 5000

// logState is the synthetic log timeline of one instance: line k was
// written at base + k/rate, until end (zero while running).
type logState struct {
	profile   LogProfile
	base, end time.Time
}

func defaultLogProfile(in engine.Instance) LogProfile {
	n := hashName(in.Name)
	return LogProfile{Rate: 0.2 + float64(n%9)/5, ErrEvery: 11 + int(n%13)}
}

func hashName(name string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return h.Sum32()
}

func (e *Engine) logFor(in *engine.Instance) *logState {
	l, ok := e.logs[in.ID]
	if !ok {
		base := in.Started
		if base.IsZero() {
			base = in.Created
		}
		l = &logState{profile: defaultLogProfile(*in), base: base}
		if in.State != engine.StateRunning {
			l.end = base.Add(10 * time.Minute) // a short past run
		}
		e.logs[in.ID] = l
	}
	return l
}

// SetLogProfile replaces the synthetic log profile of an instance; the
// timeline restarts at the current time.
func (e *Engine) SetLogProfile(name string, p LogProfile) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ok := e.byName(name)
	if !ok {
		return false
	}
	l := e.logFor(in)
	n := l.count(e.clk.Now())
	l.profile = p
	l.rebase(e.clk.Now(), n)
	return true
}

// resume continues a stopped timeline at now.
func (l *logState) resume(now time.Time) {
	n := l.count(now)
	l.end = time.Time{}
	l.rebase(now, n)
}

// rebase moves the timeline so that n lines were written by now (or by the
// end of a stopped timeline).
func (l *logState) rebase(now time.Time, n int64) {
	if !l.end.IsZero() {
		now = l.end
	}
	if l.profile.Rate <= 0 {
		l.base = now
		return
	}
	l.base = now.Add(-time.Duration(float64(max(n-1, 0)) / l.profile.Rate * float64(time.Second)))
}

// count is the number of lines written up to t.
func (l *logState) count(t time.Time) int64 {
	if !l.end.IsZero() && t.After(l.end) {
		t = l.end
	}
	if t.Before(l.base) || l.profile.Rate <= 0 {
		return 0
	}
	return int64(math.Floor(t.Sub(l.base).Seconds()*l.profile.Rate)) + 1
}

// at is the time line k was written, to the millisecond.
func (l *logState) at(k int64) time.Time {
	return l.base.Add(time.Duration(math.Round(float64(k)/l.profile.Rate*1e3)) * time.Millisecond)
}

// line renders line k of an instance's log: an application log for
// Docker, a system console for Incus.
func (l *logState) line(id engine.ID, name, image string, k int64) engine.LogLine {
	out := engine.LogLine{Time: l.at(k).UTC(), Source: engine.Stdout}
	n := hashName(name)
	if l.profile.ErrEvery > 0 && k%int64(l.profile.ErrEvery) == int64(l.profile.ErrEvery-1) {
		out.Source = engine.Stderr
		out.Text = errorLines[(k+int64(n))%int64(len(errorLines))]
		return out
	}
	if id == engine.Incus {
		out.Text = fmt.Sprintf(consoleLines[(k+int64(n))%int64(len(consoleLines))], name, k%60, k)
		return out
	}
	tpl := infoLines[int(n)%len(infoLines)]
	if kind, ok := imageKinds[imageBase(image)]; ok {
		tpl = infoLines[kind]
	}
	paths := []string{"/health", "/api/v1/items", "/api/v1/users/42", "/static/app.js", "/login"}
	codes := []int{200, 200, 200, 304, 201, 404}
	out.Text = fmt.Sprintf(tpl, paths[k%int64(len(paths))], codes[(k/3)%int64(len(codes))], float64(k%97)/10+0.2, k)
	return out
}

// imageBase is "nginx" for "nginx:1.27", "api" for "ghcr.io/me/api:1.4".
func imageBase(image string) string {
	for i := len(image) - 1; i >= 0; i-- {
		if image[i] == ':' {
			image = image[:i]
			break
		}
	}
	for i := len(image) - 1; i >= 0; i-- {
		if image[i] == '/' {
			return image[i+1:]
		}
	}
	return image
}

var imageKinds = map[string]int{"nginx": 0, "api": 1, "node": 1, "postgres": 2, "redis": 3, "grafana": 4}

// infoLines take a path, a status code, a duration in ms and the line number.
// The API line uses colour escapes, like many real applications do.
var infoLines = []string{
	`172.20.0.1 - - "GET %s HTTP/1.1" %d 612 "-" "curl/8.5" rt=%.1fms #%d`,
	"\x1b[32mINFO\x1b[0m request path=%s status=%d duration=%.1fms req=%d",
	`LOG:  statement: SELECT * FROM items WHERE path = '%s' -- status %d, %.1f ms, #%d`,
	`1:M * DB saved on disk (%s) code=%d in %.1fms, ops=%d`,
	`logger=context path=%s status=%d duration=%.1fms id=%d`,
}

// consoleLines take the instance name, a small number and the line number.
var consoleLines = []string{
	"[  OK  ] Started systemd-journald.service - Journal Service. (%s, %d) #%d",
	"\x1b[0;32m[  OK  ]\x1b[0m Finished apt-daily.service - Daily apt download activities (%s, %d) #%d",
	"%s cron[%d]: (root) CMD (run-parts /etc/cron.hourly) #%d",
	"%s systemd[1]: Starting logrotate.service - Rotate log files (%d) #%d",
	"%s sshd[%d]: Accepted publickey for admin from 10.10.0.1 port 51234 ssh2 #%d",
}

var errorLines = []string{
	"ERROR db: connection refused (web-db:5432)",
	"ERROR retrying in 2s",
	"WARN slow request: 2.3s",
	"ERROR upstream timed out (110: Connection timed out)",
}

// Logs implements engine.Engine with the synthetic timeline. Follow ends
// cleanly when the instance stops or is removed, and with an error when the
// daemon fails, like a real log stream.
func (e *Engine) Logs(ctx context.Context, id string, opts engine.LogOptions) (*engine.Stream[engine.LogLine], error) {
	if err := e.enter(ctx, "logs"); err != nil {
		return nil, err
	}
	e.mu.Lock()
	in, ok := e.instances[id]
	if !ok {
		e.mu.Unlock()
		return nil, engine.NotFound("logs", "instance "+id)
	}
	l := e.logFor(in)
	name, image := in.Name, in.Image
	now := e.clk.Now()
	next := l.count(now)
	from := int64(0)
	if opts.Tail >= 0 {
		from = max(0, next-int64(opts.Tail))
	}
	e.mu.Unlock()

	s := engine.NewStream[engine.LogLine](engine.LogBuffer)
	go func() {
		send := func(lo, hi int64) bool {
			for k := lo; k < hi; k++ {
				e.mu.Lock()
				ln := l.line(e.id, name, image, k)
				e.mu.Unlock()
				if !opts.Since.IsZero() && ln.Time.Before(opts.Since) {
					continue
				}
				if !s.Send(ctx, ln) {
					return false
				}
			}
			return true
		}
		if !send(from, next) {
			s.Close(ctx.Err())
			return
		}
		if !opts.Follow {
			s.Close(nil)
			return
		}
		for {
			e.mu.Lock()
			tick := time.Duration(float64(time.Second) / max(l.profile.Rate, 1))
			e.mu.Unlock()
			select {
			case <-ctx.Done():
				s.Close(ctx.Err())
				return
			case <-e.clk.After(max(tick, 100*time.Millisecond)):
			}
			e.mu.Lock()
			cur, alive := e.instances[id]
			fault, ferr, closed := e.fault, e.faultErr, e.closed
			running := alive && cur.State == engine.StateRunning
			hi := l.count(e.clk.Now())
			e.mu.Unlock()
			switch {
			case closed:
				s.Close(io.EOF)
				return
			case fault == faultFail:
				s.Close(streamErr("logs", ferr))
				return
			case fault == faultHang:
				continue
			}
			if hi-next > maxLogBurst {
				hi = next + maxLogBurst
			}
			if !send(next, hi) {
				s.Close(ctx.Err())
				return
			}
			next = hi
			if !running {
				s.Close(nil)
				return
			}
		}
	}()
	return s, nil
}

// streamErr is the error a broken stream reports: EOF when the daemon
// stopped (refused connections), like a real socket.
func streamErr(op string, err error) error {
	switch {
	case err == nil:
		return engine.Wrap(op, errors.New("stream closed"))
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("%s: %w", op, io.EOF)
	}
	return engine.Wrap(op, err)
}
