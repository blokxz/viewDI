package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const searchJSON = `{"count":3,"results":[
 {"repo_name":"nginx","short_description":"Official build of Nginx.","star_count":21416,"pull_count":13544827346,"is_official":true},
 {"repo_name":"nginx/nginx-ingress","short_description":"NGINX \u001b[31mIngress\u001b[0m\nController","star_count":124,"pull_count":1088362532,"is_official":false},
 {"repo_name":"--evil;rm -rf","short_description":"x","star_count":1,"pull_count":1,"is_official":false},
 {"repo_name":"UPPER/case","short_description":"x","star_count":1,"pull_count":1,"is_official":false},
 {"repo_name":"a/b/c","short_description":"too many parts","star_count":1,"pull_count":1,"is_official":false},
 {"repo_name":"neg/count","short_description":"","star_count":-5,"pull_count":-1,"is_official":false}]}`

const libraryJSON = `{"count":3,"results":[
 {"name":"nginx","namespace":"library","description":"Official build of Nginx.","star_count":21416,"pull_count":13548180858},
 {"name":"alpine","namespace":"library","description":"A minimal image","star_count":11579,"pull_count":12446992049},
 {"name":"sneaky","namespace":"someone","description":"not official","star_count":1,"pull_count":1}]}`

func server(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return newAt(srv.URL, nil)
}

func TestSearchKeepsOnlyValidRepositories(t *testing.T) {
	var gotPath, gotQuery string
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query().Get("query")
		_, _ = w.Write([]byte(searchJSON))
	})
	got, err := c.Search(context.Background(), "  nginx ", 10)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v2/search/repositories/" || gotQuery != "nginx" {
		t.Errorf("asked %s ?query=%q", gotPath, gotQuery)
	}
	var refs []string
	for _, r := range got {
		refs = append(refs, r.Ref)
	}
	if want := "nginx,nginx/nginx-ingress,neg/count"; strings.Join(refs, ",") != want {
		t.Errorf("refs %v, want %s", refs, want)
	}
	if !got[0].Official || got[0].Pulls != 13544827346 || got[0].Stars != 21416 {
		t.Errorf("first %+v", got[0])
	}
	if d := got[1].Description; d != "NGINX Ingress Controller" {
		t.Errorf("description %q: escapes and newlines must go", d)
	}
	if got[2].Stars != 0 || got[2].Pulls != 0 {
		t.Errorf("negative counts must become 0: %+v", got[2])
	}
}

func TestTopListsOfficialImagesByPulls(t *testing.T) {
	var gotPath, gotOrder string
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotOrder = r.URL.Path, r.URL.Query().Get("ordering")
		_, _ = w.Write([]byte(libraryJSON))
	})
	got, err := c.Top(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v2/repositories/library/" || gotOrder != "pull_count" {
		t.Errorf("asked %s ordering=%q", gotPath, gotOrder)
	}
	if len(got) != 2 || got[0].Ref != "nginx" || got[1].Ref != "alpine" || !got[0].Official {
		t.Errorf("got %+v (another namespace must be dropped, order kept)", got)
	}
}

func TestSearchRefusesWhatItWouldNotSend(t *testing.T) {
	called := false
	c := server(t, func(http.ResponseWriter, *http.Request) { called = true })
	for _, q := range []string{"", "   ", strings.Repeat("a", MaxQuery+1)} {
		if _, err := c.Search(context.Background(), q, 10); err == nil {
			t.Errorf("query of %d bytes was accepted", len(q))
		}
	}
	if called {
		t.Error("a refused query reached the network")
	}
}

func TestQueryIsEscaped(t *testing.T) {
	var raw string
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"results":[]}`))
	})
	if _, err := c.Search(context.Background(), "a&b=c d#e", 5); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "b=c") || strings.Contains(raw, " ") || strings.Contains(raw, "#") {
		t.Errorf("raw query %q lets the text become parameters", raw)
	}
}

func TestErrorsAreShortAndKnown(t *testing.T) {
	for code, want := range map[int]string{429: "too many requests", 500: "500", 404: "404"} {
		c := server(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte("<html>secret internal page</html>"))
		})
		_, err := c.Top(context.Background(), 5)
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "secret") {
			t.Errorf("status %d: error %v", code, err)
		}
	}
}

func TestAnswersAreCapped(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[],"pad":"` + strings.Repeat("x", MaxBody) + `"}`))
	})
	if _, err := c.Top(context.Background(), 5); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("a %d byte answer: %v", MaxBody, err)
	}
	c = server(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) })
	if _, err := c.Top(context.Background(), 5); err == nil {
		t.Error("garbage was accepted")
	}
}

func TestResultsAreCapped(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"results":[`)
	for i := range MaxResults + 20 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"repo_name":"img` + strings.Repeat("a", i%5) + `","short_description":"x","star_count":1,"pull_count":1}`)
	}
	b.WriteString(`]}`)
	c := server(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(b.String())) })
	got, err := c.Search(context.Background(), "img", 10)
	if err != nil || len(got) != MaxResults {
		t.Errorf("%d results, %v", len(got), err)
	}
}

func TestARedirectToAnotherHostIsRefused(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the redirect was followed")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	t.Cleanup(other.Close)
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	})
	if _, err := c.Top(context.Background(), 5); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Errorf("error %v", err)
	}
}

func TestTheDefaultClientOnlyTalksToDockerHub(t *testing.T) {
	c := New()
	if c.base != "https://hub.docker.com" || Host != "hub.docker.com" {
		t.Errorf("base %q", c.base)
	}
}

func TestValidRef(t *testing.T) {
	for ref, want := range map[string]bool{
		"nginx": true, "grafana/grafana": true, "a.b_c-d/e": true, "": false, "-x": false, "a//b": false,
		"A": false, "a b": false, "a:tag": false, "a/b/c": false, strings.Repeat("a", 300): false,
	} {
		if got := ValidRef(ref); got != want {
			t.Errorf("ValidRef(%q) = %v", ref, got)
		}
	}
}

func FuzzParseSearch(f *testing.F) {
	f.Add([]byte(searchJSON))
	f.Add([]byte(`{"results":[{"repo_name":"a","pull_count":-1}]}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		got, err := parseSearch(b)
		if err != nil {
			return
		}
		if len(got) > MaxResults {
			t.Fatalf("%d results", len(got))
		}
		for _, r := range got {
			if !ValidRef(r.Ref) || len(r.Description) > maxDescBytes+len("…") || strings.ContainsAny(r.Description, "\x1b\n\r") {
				t.Fatalf("unsafe result %+v", r)
			}
		}
	})
}

func FuzzParseLibrary(f *testing.F) {
	f.Add([]byte(libraryJSON))
	f.Fuzz(func(t *testing.T, b []byte) {
		got, err := parseLibrary(b)
		if err != nil {
			return
		}
		for _, r := range got {
			if !ValidRef(r.Ref) || strings.Contains(r.Ref, "/") || !r.Official {
				t.Fatalf("unsafe result %+v", r)
			}
		}
	})
}
