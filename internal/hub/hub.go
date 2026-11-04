// Package hub is the small HTTP client that lists images on Docker Hub: the
// search and the most downloaded official images. It reads only; it talks to
// hub.docker.com and to nothing else (a redirect to another host is an error),
// caps what it reads, and never sends anything the user did not type. It runs
// only when the user asks (the Docker Hub browser, key S) and is never started
// in --mock or when [updates] enabled = false.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/blokxz/viewDI/internal/textsafe"
)

// Host is the only host the client talks to.
const Host = "hub.docker.com"

// Limits of what the client accepts.
const (
	MaxBody      = 1 << 20 // bytes read from one response
	MaxResults   = 50      // results kept from one response
	MaxQuery     = 100     // runes of a query
	maxRepoName  = 255
	maxDescBytes = 200
	timeout      = 10 * time.Second
)

// Repo is one image on the Hub.
type Repo struct {
	Ref         string // "nginx", "grafana/grafana": what docker pull takes
	Description string // one line, sanitised and capped
	Stars       uint64
	Pulls       uint64
	Official    bool
}

// Client asks Docker Hub. The zero value is not usable: use New.
type Client struct {
	base string
	http *http.Client
}

// New returns a client for hub.docker.com.
func New() *Client { return newAt("https://"+Host, nil) }

// newAt is New against another base URL (tests).
func newAt(base string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: timeout}
	}
	c := &Client{base: strings.TrimRight(base, "/"), http: hc}
	prev := hc.CheckRedirect
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && (req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme) {
			return fmt.Errorf("refusing a redirect to %s", req.URL.Host)
		}
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if prev != nil {
			return prev(req, via)
		}
		return nil
	}
	return c
}

// Search finds images matching query, in the Hub's order of relevance.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Repo, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("type something to search for")
	}
	if len([]rune(q)) > MaxQuery {
		return nil, fmt.Errorf("a search is at most %d characters", MaxQuery)
	}
	v := url.Values{"query": {q}, "page_size": {strconv.Itoa(clamp(limit))}}
	body, err := c.get(ctx, "/v2/search/repositories/?"+v.Encode())
	if err != nil {
		return nil, err
	}
	return parseSearch(body)
}

// Top lists the most downloaded official images, most downloaded first.
func (c *Client) Top(ctx context.Context, limit int) ([]Repo, error) {
	v := url.Values{"page_size": {strconv.Itoa(clamp(limit))}, "ordering": {"pull_count"}}
	body, err := c.get(ctx, "/v2/repositories/library/?"+v.Encode())
	if err != nil {
		return nil, err
	}
	return parseLibrary(body)
}

func clamp(n int) int {
	if n <= 0 || n > MaxResults {
		return MaxResults
	}
	return n
}

// get reads at most MaxBody bytes of the answer to a GET.
func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "viewDI")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker hub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, errors.New("docker hub: too many requests, try again in a minute")
		}
		return nil, fmt.Errorf("docker hub: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return nil, fmt.Errorf("docker hub: %w", err)
	}
	if len(body) > MaxBody {
		return nil, errors.New("docker hub: the answer is too large")
	}
	return body, nil
}

// refName is what a repository name may look like: lower-case path parts of
// letters, digits and . _ -, with at most one "/" (namespace/name).
var refName = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)?$`)

// ValidRef reports whether s is a repository name the client would keep.
func ValidRef(s string) bool { return len(s) <= maxRepoName && refName.MatchString(s) }

type searchAnswer struct {
	Results []struct {
		Name     string `json:"repo_name"`
		Desc     string `json:"short_description"`
		Stars    int64  `json:"star_count"`
		Pulls    int64  `json:"pull_count"`
		Official bool   `json:"is_official"`
	} `json:"results"`
}

func parseSearch(b []byte) ([]Repo, error) {
	var a searchAnswer
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, errors.New("docker hub: unreadable answer")
	}
	var out []Repo
	for _, r := range a.Results {
		if !ValidRef(r.Name) {
			continue
		}
		out = append(out, repo(r.Name, r.Desc, r.Stars, r.Pulls, r.Official))
		if len(out) == MaxResults {
			break
		}
	}
	return out, nil
}

type libraryAnswer struct {
	Results []struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Desc      string `json:"description"`
		Stars     int64  `json:"star_count"`
		Pulls     int64  `json:"pull_count"`
	} `json:"results"`
}

func parseLibrary(b []byte) ([]Repo, error) {
	var a libraryAnswer
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, errors.New("docker hub: unreadable answer")
	}
	var out []Repo
	for _, r := range a.Results {
		if r.Namespace != "library" || !ValidRef(r.Name) || strings.Contains(r.Name, "/") {
			continue
		}
		out = append(out, repo(r.Name, r.Desc, r.Stars, r.Pulls, true))
		if len(out) == MaxResults {
			break
		}
	}
	return out, nil
}

func repo(name, desc string, stars, pulls int64, official bool) Repo {
	return Repo{Ref: name, Description: oneLine(desc), Stars: nonNeg(stars), Pulls: nonNeg(pulls), Official: official}
}

func nonNeg(n int64) uint64 {
	if n < 0 {
		return 0
	}
	return uint64(n)
}

// oneLine makes untrusted text safe for a list: no terminal escapes, no
// control characters, one line, at most maxDescBytes bytes.
func oneLine(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(s)
	s = strings.Join(strings.Fields(textsafe.Clean(s)), " ")
	if len(s) > maxDescBytes {
		s = strings.ToValidUTF8(s[:maxDescBytes], "") + "…"
	}
	return s
}
