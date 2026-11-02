package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
)

const mobyReleases = `[
 {"tag_name":"docker-v29.2.0-rc.1","name":"v29.2.0-rc.1","prerelease":true,"html_url":"https://github.com/moby/moby/releases/tag/docker-v29.2.0-rc.1"},
 {"tag_name":"docker-v29.1.2","name":"v29.1.2","body":"Fixes CVE-2026-12345 in the API.","html_url":"https://github.com/moby/moby/releases/tag/docker-v29.1.2","published_at":"2026-10-20T10:00:00Z"},
 {"tag_name":"api/v1.53.0","name":"API"},
 {"tag_name":"docker-v29.1.1","name":"v29.1.1","html_url":"javascript:alert(1)","published_at":"2026-10-01T10:00:00Z"},
 {"tag_name":"docker-v29.0.1","name":"v29.0.1","draft":true}
]`

const incusReleases = `[
 {"tag_name":"v6.18","name":"Incus 6.18","html_url":"https://github.com/lxc/incus/releases/tag/v6.18"},
 {"tag_name":"v6.0.6","name":"Incus 6.0.6 LTS","body":"Security: CVE-2026-4242","html_url":"https://github.com/lxc/incus/releases/tag/v6.0.6"},
 {"tag_name":"v6.0.5","name":"Incus 6.0.5 LTS"}
]`

func TestParse(t *testing.T) {
	rels, err := Parse(engine.Docker, []byte(mobyReleases))
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 2 || rels[0].Version != "29.1.2" || rels[1].Version != "29.1.1" {
		t.Fatalf("releases = %+v", rels)
	}
	if !rels[0].Security || rels[1].Security {
		t.Errorf("security flags = %v %v", rels[0].Security, rels[1].Security)
	}
	if rels[1].URL != "" {
		t.Errorf("unsafe URL kept: %q", rels[1].URL)
	}
	rels, err = Parse(engine.Incus, []byte(incusReleases))
	if err != nil || len(rels) != 3 || rels[0].Version != "6.18" || !rels[1].Security {
		t.Fatalf("incus = %+v, %v", rels, err)
	}
	if _, err := Parse(engine.Incus, []byte("{")); err == nil {
		t.Fatal("bad JSON parsed")
	}
}

func TestETagAndToken(t *testing.T) {
	var hits, full atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/repos/lxc/incus/releases" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("If-None-Match") == `"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		full.Add(1)
		w.Header().Set("ETag", `"abc"`)
		_, _ = w.Write([]byte(incusReleases))
	}))
	defer srv.Close()
	s := New(srv.Client(), "tok", srv.URL, nil)
	for range 2 {
		rels, err := s.Releases(context.Background(), engine.Incus)
		if err != nil || len(rels) != 3 {
			t.Fatalf("releases = %v, %v", rels, err)
		}
	}
	if hits.Load() != 2 || full.Load() != 1 {
		t.Fatalf("hits=%d full=%d", hits.Load(), full.Load())
	}
	// The ETag survives a restart through the cache.
	s2 := New(srv.Client(), "tok", srv.URL, nil)
	s2.Restore(s.Snapshot())
	if rels, err := s2.Releases(context.Background(), engine.Incus); err != nil || len(rels) != 3 || full.Load() != 1 {
		t.Fatalf("restored: %v %v full=%d", rels, err, full.Load())
	}
}

func TestRateLimitBacksOff(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC))
	var hits atomic.Int32
	reset := clk.Now().Add(10 * time.Minute).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(mobyReleases))
	}))
	defer srv.Close()
	s := New(srv.Client(), "", srv.URL, clk)
	if _, err := s.Releases(context.Background(), engine.Docker); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
	clk.Advance(5 * time.Minute)
	if _, err := s.Releases(context.Background(), engine.Docker); !errors.Is(err, ErrRateLimited) || hits.Load() != 1 {
		t.Fatalf("second call: %v, hits=%d", err, hits.Load())
	}
	clk.Advance(6 * time.Minute)
	if rels, err := s.Releases(context.Background(), engine.Docker); err != nil || len(rels) != 2 {
		t.Fatalf("after reset: %v %v", rels, err)
	}
}

func TestHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := New(srv.Client(), "", srv.URL, nil).Releases(context.Background(), engine.Docker); err == nil {
		t.Fatal("no error on 500")
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(mobyReleases))
	f.Add([]byte(incusReleases))
	f.Fuzz(func(_ *testing.T, b []byte) {
		_, _ = Parse(engine.Docker, b)
		_, _ = Parse(engine.Incus, b)
	})
}

// The conditional-request cache comes back from updates.json.
func FuzzRestore(f *testing.F) {
	for _, s := range []string{"", "{", `{"docker":{"etag":"x"}}`, `{"docker":null}`, "[]", `{"nope":{"etag":"x"}}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		New(nil, "", "", clock.NewFake(time.Unix(0, 0))).Restore(data)
	})
}

func FuzzParseReleases(f *testing.F) {
	f.Add([]byte(mobyReleases))
	f.Add([]byte(`[{"tag_name":null}]`))
	f.Add([]byte("{"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(engine.Docker, data)
		_, _ = Parse(engine.Incus, data)
	})
}
