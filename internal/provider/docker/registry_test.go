package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHubRef(t *testing.T) {
	for ref, want := range map[string]string{
		"nginx:1.28":                "library/nginx 1.28",
		"nginx":                     "library/nginx latest",
		"grafana/grafana:11.5.2":    "grafana/grafana 11.5.2",
		"docker.io/library/redis:7": "library/redis 7",
	} {
		repo, tag, err := hubRef(ref)
		if err != nil || repo+" "+tag != want {
			t.Errorf("hubRef(%q) = %q %q, %v; want %s", ref, repo, tag, err, want)
		}
	}
	for _, ref := range []string{"ghcr.io/x/y:1", "localhost:5000/app:1", "registry.example.com/a/b", "nginx@sha256:abc"} {
		if _, _, err := hubRef(ref); err == nil {
			t.Errorf("hubRef(%q) accepted another registry", ref)
		}
	}
}

func TestHubSizeFollowsTheIndexToTheHostPlatform(t *testing.T) {
	var asked []string
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "repository%3Alibrary%2Fnginx%3Apull") {
			t.Errorf("scope %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"token":"t0k"}`))
	})
	mux.HandleFunc("/v2/library/nginx/manifests/", func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer t0k" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/1.28") {
			_, _ = w.Write([]byte(`{"manifests":[
				{"digest":"sha256:arm","platform":{"os":"linux","architecture":"arm64"}},
				{"digest":"sha256:amd","platform":{"os":"linux","architecture":"amd64"}}]}`))
			return
		}
		if !strings.HasSuffix(r.URL.Path, "sha256:amd") {
			t.Errorf("followed %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"config":{"size":1000},"layers":[{"size":20000},{"size":300000}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &hubClient{http: srv.Client(), auth: srv.URL + "/token", registry: srv.URL, arch: "amd64"}
	n, err := c.size(context.Background(), "library/nginx", "1.28")
	if err != nil || n != 321000 {
		t.Fatalf("size = %d, %v (asked %v)", n, err, asked)
	}
	c.arch = "riscv64"
	if _, err := c.size(context.Background(), "library/nginx", "1.28"); err == nil {
		t.Fatal("a missing platform must be an error")
	}
}

func TestHubSizeSurfacesRegistryErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "slow down", http.StatusTooManyRequests) }))
	defer srv.Close()
	c := &hubClient{http: srv.Client(), auth: srv.URL, registry: srv.URL, arch: "amd64"}
	if _, err := c.size(context.Background(), "library/x", "1"); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v", err)
	}
}
