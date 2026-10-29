package fixtures

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Server serves a fixture set on a Unix socket.
type Server struct {
	Socket string // socket path
	idx    *Index
	dir    string
	byKey  map[string]Entry
	events [][]byte

	srv *http.Server
	ln  net.Listener

	mu       sync.Mutex
	streams  map[chan []byte]struct{}
	misses   []string
	ready    chan struct{} // closed once a stream subscribed
	readyOne sync.Once
}

// Serve starts a server for the fixture set in dir; it stops when the test ends.
func Serve(t testing.TB, dir string) *Server {
	t.Helper()
	idx, err := LoadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Unix socket paths are limited to ~108 bytes; t.TempDir can be longer.
	sockDir, err := os.MkdirTemp("", "viewdi-fx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	s := &Server{
		Socket:  filepath.Join(sockDir, "s.sock"),
		idx:     idx,
		dir:     dir,
		byKey:   map[string]Entry{},
		streams: map[chan []byte]struct{}{},
		ready:   make(chan struct{}),
	}
	for _, e := range idx.Entries {
		s.byKey[e.Key()] = e
	}
	if idx.Events != "" {
		data, err := os.ReadFile(filepath.Join(dir, idx.Events))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(data, []byte("\n")) {
			if len(bytes.TrimSpace(line)) > 0 {
				s.events = append(s.events, line)
			}
		}
	}
	s.ln, err = net.Listen("unix", s.Socket)
	if err != nil {
		t.Fatal(err)
	}
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.srv.Serve(s.ln) }()
	t.Cleanup(s.Close)
	return s
}

// Close stops the server and ends open streams.
func (s *Server) Close() {
	s.mu.Lock()
	for ch := range s.streams {
		close(ch)
		delete(s.streams, ch)
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
	_ = s.srv.Close()
}

// Misses returns the request keys that had no fixture, for debugging.
func (s *Server) Misses() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.misses...)
}

// Emit replays the recorded events to every open stream. It waits (up to
// 5 s) for a stream to be open first.
func (s *Server) Emit() {
	select {
	case <-s.ready:
	case <-time.After(5 * time.Second):
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.streams {
		for _, ev := range s.events {
			ch <- ev
		}
	}
}

func (s *Server) subscribe() chan []byte {
	ch := make(chan []byte, len(s.events)*4+1)
	s.mu.Lock()
	s.streams[ch] = struct{}{}
	s.mu.Unlock()
	s.readyOne.Do(func() { close(s.ready) })
	return ch
}

func (s *Server) unsubscribe(ch chan []byte) {
	s.mu.Lock()
	if _, ok := s.streams[ch]; ok {
		delete(s.streams, ch)
		close(ch)
	}
	s.mu.Unlock()
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := CanonicalPath(r.URL.Path)
	if s.idx.Events != "" && isEventsPath(s.idx.Engine, path) {
		s.serveEvents(w, r)
		return
	}
	key := Entry{Method: r.Method, Path: path, Query: CanonicalQuery(r.URL.RawQuery)}.Key()
	e, ok := s.byKey[key]
	if !ok {
		s.mu.Lock()
		s.misses = append(s.misses, key)
		s.mu.Unlock()
		s.notFound(w)
		return
	}
	for k, v := range e.Headers {
		w.Header().Set(k, v)
	}
	var body []byte
	if e.Body != "" && r.Method != http.MethodHead {
		var err error
		body, err = os.ReadFile(filepath.Join(s.dir, e.Body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(e.Status)
	_, _ = w.Write(body)
}

func isEventsPath(eng, path string) bool {
	if eng == "incus" {
		return path == "/1.0/events"
	}
	return path == "/events"
}

// notFound answers in the engine's own error format.
func (s *Server) notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	if s.idx.Engine == "incus" {
		_, _ = w.Write([]byte(`{"type":"error","status":"","status_code":0,"operation":"","error_code":404,"error":"Not Found","metadata":null}`))
		return
	}
	_, _ = w.Write([]byte(`{"message":"No such object"}`))
}

func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	ch := s.subscribe()
	defer s.unsubscribe(ch)
	if s.idx.Engine == "incus" {
		s.serveWebsocket(w, r, ch)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	if v := s.apiHeader(); v != "" {
		w.Header().Set("Api-Version", v)
	}
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	if fl != nil {
		fl.Flush()
	}
	bw := bufio.NewWriter(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			_, _ = bw.Write(ev)
			_ = bw.WriteByte('\n')
			_ = bw.Flush()
			if fl != nil {
				fl.Flush()
			}
		}
	}
}

func (s *Server) apiHeader() string {
	if s.idx.Engine == "docker" {
		return s.idx.APIVersion
	}
	return ""
}

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func (s *Server) serveWebsocket(w http.ResponseWriter, r *http.Request, ch chan []byte) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	closed := make(chan struct{})
	go func() { // drain control frames; detects the client going away
		defer close(closed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-closed:
			return
		case ev, ok := <-ch:
			if !ok {
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, ev); err != nil {
				return
			}
		}
	}
}
