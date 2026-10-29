package fixtures

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxBody caps a recorded response body.
const maxBody = 16 << 20

// keepHeaders are the response headers worth replaying.
var keepHeaders = []string{"Content-Type", "Api-Version", "Ostype"}

// Recorder is a recording reverse proxy: point an adapter at Listen's socket
// and every request it makes is forwarded to the real daemon socket and
// stored, sanitised. Docker's /events stream is recorded line by line;
// Incus events are captured separately (websocket) with AddEvent.
type Recorder struct {
	Engine string // docker | incus
	Target string // real daemon socket path
	San    *Sanitizer

	mu      sync.Mutex
	entries map[string]Entry
	bodies  map[string][]byte
	events  [][]byte
}

// NewRecorder returns a recorder forwarding to the target socket.
func NewRecorder(engineName, target string, san *Sanitizer) *Recorder {
	return &Recorder{Engine: engineName, Target: target, San: san, entries: map[string]Entry{}, bodies: map[string][]byte{}}
}

// Listen serves the proxy on socket until the returned stop is called.
func (r *Recorder) Listen(socket string) (stop func(), err error) {
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "daemon"
			pr.Out.Host = "daemon"
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", r.Target)
			},
		},
		FlushInterval:  -1,
		ModifyResponse: r.capture,
	}
	srv := &http.Server{Handler: proxy, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = srv.Close()
	}, nil
}

func (r *Recorder) capture(resp *http.Response) error {
	req := resp.Request
	path := CanonicalPath(req.URL.Path)
	if resp.StatusCode == http.StatusSwitchingProtocols {
		return nil
	}
	if r.Engine == "docker" && path == "/events" {
		resp.Body = &lineTee{ReadCloser: resp.Body, line: r.AddEvent}
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	e := Entry{Method: req.Method, Path: path, Query: CanonicalQuery(req.URL.RawQuery), Status: resp.StatusCode, Headers: map[string]string{}}
	for _, h := range keepHeaders {
		if v := resp.Header.Get(h); v != "" {
			e.Headers[h] = v
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(body) > 0 {
		e.Body = FileName(e.Method, e.Path, e.Query)
		r.bodies[e.Body] = r.San.JSON(body)
	}
	r.entries[e.Key()] = e
	return nil
}

// AddEvent records one raw event payload (sanitised).
func (r *Recorder) AddEvent(payload []byte) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return
	}
	var compact bytes.Buffer
	clean := r.San.JSON(payload)
	if err := json.Compact(&compact, clean); err != nil {
		compact.Reset()
		compact.Write(bytes.TrimSpace(clean))
	}
	r.mu.Lock()
	r.events = append(r.events, compact.Bytes())
	r.mu.Unlock()
}

// Write stores the recording in dir.
func (r *Recorder) Write(dir string, idx Index) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, body := range r.bodies {
		if err := os.WriteFile(filepath.Join(dir, name), append(body, '\n'), 0o644); err != nil {
			return err
		}
	}
	idx.Engine, idx.Recorded = r.Engine, true
	idx.Entries = idx.Entries[:0]
	for _, e := range r.entries {
		idx.Entries = append(idx.Entries, e)
	}
	if len(r.events) > 0 {
		idx.Events = "events.jsonl"
		if err := os.WriteFile(filepath.Join(dir, idx.Events), append(bytes.Join(r.events, []byte("\n")), '\n'), 0o644); err != nil {
			return err
		}
	}
	if len(idx.Entries) == 0 {
		return errors.New("nothing recorded")
	}
	return idx.Save(dir)
}

// lineTee passes a stream through and reports each complete line.
type lineTee struct {
	io.ReadCloser
	buf  []byte
	line func([]byte)
}

func (t *lineTee) Read(p []byte) (int, error) {
	n, err := t.ReadCloser.Read(p)
	t.buf = append(t.buf, p[:n]...)
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			break
		}
		t.line(append([]byte(nil), t.buf[:i]...))
		t.buf = t.buf[i+1:]
	}
	return n, err
}
