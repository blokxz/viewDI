package docker

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blokxz/viewDI/internal/engine"
)

// UpdateLimits posts the limits to the container's update endpoint: the
// memory limit with the same swap limit, the CPUs as nano-CPUs.
func TestUpdateLimitsRequest(t *testing.T) {
	var path string
	var body map[string]any
	sock := filepath.Join(t.TempDir(), "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Warnings":[]}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	a, err := New("unix://" + sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if err := a.UpdateLimits(context.Background(), "abc123", engine.Limits{}); err != nil || path != "" {
		t.Fatalf("zero limits: err %v, request %q", err, path)
	}
	if err := a.UpdateLimits(context.Background(), "abc123", engine.Limits{MemoryBytes: 512 << 20, CPUs: 1.5}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "/containers/abc123/update") {
		t.Errorf("path %q", path)
	}
	if body["Memory"] != float64(512<<20) || body["MemorySwap"] != float64(512<<20) || body["NanoCpus"] != float64(1.5e9) {
		t.Errorf("body %v", body)
	}
}
