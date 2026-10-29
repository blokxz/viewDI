// Package fixtures records daemon API responses into testdata and serves them
// back over a Unix socket, so the Docker and Incus adapters are tested end to
// end through the real SDKs without daemons (docs/05-quality-plan.md §1.3).
//
// A fixture set is a directory holding index.json and one body file per
// response:
//
//	testdata/fixtures/docker/v1.52/index.json
//	testdata/fixtures/docker/v1.52/containers_json.json
//	testdata/fixtures/docker/v1.52/events.jsonl
//
// Recordings are sanitised (Sanitizer) before they are written.
package fixtures

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Index describes a fixture set.
type Index struct {
	Engine        string `json:"engine"`         // docker | incus
	APIVersion    string `json:"api_version"`    // e.g. "1.52"
	ServerVersion string `json:"server_version"` // e.g. "29.1.2"
	// Recorded is false for hand-written sets.
	Recorded bool   `json:"recorded"`
	Note     string `json:"note,omitempty"`
	// Events names a file of newline-separated event payloads, replayed on
	// the event stream when Server.Emit is called.
	Events  string  `json:"events,omitempty"`
	Entries []Entry `json:"entries"`
}

// Entry is one recorded request/response pair.
type Entry struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`            // without the Docker /vX.Y prefix
	Query   string            `json:"query,omitempty"` // canonical, see CanonicalQuery
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"` // file name in the set directory
}

// Key is the lookup key of a request.
func (e Entry) Key() string { return e.Method + " " + e.Path + "?" + e.Query }

// LoadIndex reads dir/index.json.
func LoadIndex(dir string) (*Index, error) {
	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("fixtures %s: %w", dir, err)
	}
	return &idx, nil
}

// Save writes the index as dir/index.json, entries sorted.
func (idx *Index) Save(dir string) error {
	sort.Slice(idx.Entries, func(i, j int) bool { return idx.Entries[i].Key() < idx.Entries[j].Key() })
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "index.json"), append(data, '\n'), 0o644)
}

var dockerVersionPrefix = regexp.MustCompile(`^/v\d+\.\d+/`)

// CanonicalPath strips the Docker API version prefix ("/v1.52/info" → "/info").
func CanonicalPath(p string) string {
	if loc := dockerVersionPrefix.FindStringIndex(p); loc != nil {
		return "/" + p[loc[1]:]
	}
	return p
}

// CanonicalQuery sorts query parameters so equivalent requests share a key.
func CanonicalQuery(raw string) string {
	v, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	return v.Encode()
}

// FileName derives a body file name from a request.
func FileName(method, path, query string) string {
	name := strings.Trim(path, "/")
	if query != "" {
		name += "_" + query
	}
	if method != "GET" {
		name = strings.ToLower(method) + "_" + name
	}
	name = regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(name, "_")
	if len(name) > 120 {
		name = name[:120]
	}
	if name == "" {
		name = "root"
	}
	return name + ".json"
}
