package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

// Docker Hub endpoints. Only Docker Hub references are sized: another
// registry needs its own authentication, so it answers "unknown".
const (
	hubAuth     = "https://auth.docker.io/token"
	hubRegistry = "https://registry-1.docker.io"
	maxManifest = 2 << 20 // bytes read of one manifest (untrusted input)
)

// errNotHub means the reference is not on Docker Hub.
var errNotHub = errors.New("not a Docker Hub image")

// hubRef splits a reference into the repository and tag of Docker Hub.
// "nginx:1.28" is library/nginx:1.28; "grafana/grafana:11" stays; a first
// component with a dot or a colon is another registry.
func hubRef(ref string) (repo, tag string, err error) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return "", "", errNotHub // digests are not sized
	}
	ref = strings.TrimPrefix(strings.TrimPrefix(ref, "docker.io/"), "index.docker.io/")
	name, tag := ref, "latest"
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		name, tag = ref[:i], ref[i+1:]
	}
	if first, _, ok := strings.Cut(name, "/"); ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return "", "", errNotHub
	}
	if !strings.Contains(name, "/") {
		name = "library/" + name
	}
	if name == "" || tag == "" {
		return "", "", errNotHub
	}
	return name, tag, nil
}

type hubClient struct {
	http     *http.Client
	auth     string // token endpoint
	registry string // registry base URL
	arch     string
}

func newHubClient() *hubClient {
	return &hubClient{
		http: &http.Client{Timeout: 15 * time.Second}, auth: hubAuth, registry: hubRegistry, arch: runtime.GOARCH,
	}
}

type manifest struct {
	MediaType string `json:"mediaType"`
	Config    struct {
		Size int64 `json:"size"`
	} `json:"config"`
	Layers []struct {
		Size int64 `json:"size"`
	} `json:"layers"`
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			OS, Architecture string
		} `json:"platform"`
	} `json:"manifests"`
}

const acceptManifests = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

// size is the compressed download size of repo:tag for the host's
// architecture: the config blob plus every layer.
func (c *hubClient) size(ctx context.Context, repo, tag string) (uint64, error) {
	token, err := c.token(ctx, repo)
	if err != nil {
		return 0, err
	}
	m, err := c.manifest(ctx, token, repo, tag)
	if err != nil {
		return 0, err
	}
	if len(m.Manifests) > 0 { // an index: follow the entry for this platform
		digest := ""
		for _, e := range m.Manifests {
			if e.Platform.OS == "linux" && e.Platform.Architecture == c.arch {
				digest = e.Digest
				break
			}
		}
		if digest == "" {
			return 0, fmt.Errorf("no linux/%s build", c.arch)
		}
		if m, err = c.manifest(ctx, token, repo, digest); err != nil {
			return 0, err
		}
	}
	total := max(m.Config.Size, 0)
	for _, l := range m.Layers {
		total += max(l.Size, 0)
	}
	return uint64(total), nil
}

func (c *hubClient) token(ctx context.Context, repo string) (string, error) {
	q := url.Values{"service": {"registry.docker.io"}, "scope": {"repository:" + repo + ":pull"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.auth+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := c.do(req, &out); err != nil {
		return "", fmt.Errorf("registry token: %w", err)
	}
	return out.Token, nil
}

func (c *hubClient) manifest(ctx context.Context, token, repo, ref string) (manifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.registry+"/v2/"+repo+"/manifests/"+url.PathEscape(ref), nil)
	if err != nil {
		return manifest{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", acceptManifests)
	var m manifest
	if err := c.do(req, &m); err != nil {
		return manifest{}, fmt.Errorf("registry manifest: %w", err)
	}
	return m, nil
}

func (c *hubClient) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxManifest)).Decode(out)
}
