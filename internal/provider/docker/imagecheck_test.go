package docker

import (
	"testing"

	"github.com/distribution/reference"
)

func TestRepoDigest(t *testing.T) {
	digests := []string{"ghcr.io/me/api@sha256:" + hex('a'), "nginx@sha256:" + hex('b'), "nginx@sha256:" + hex('c')}
	for _, tc := range []struct{ ref, want string }{
		{"nginx:1.27", "sha256:" + hex('b')},
		{"docker.io/library/nginx:1.27", "sha256:" + hex('b')},
		{"ghcr.io/me/api:1.4", "sha256:" + hex('a')},
		{"me/api:1.4", ""},
		{"redis:7", ""},
	} {
		named, err := reference.ParseNormalizedNamed(tc.ref)
		if err != nil {
			t.Fatal(err)
		}
		if got := repoDigest(named, digests); got != tc.want {
			t.Errorf("repoDigest(%s) = %q, want %q", tc.ref, got, tc.want)
		}
	}
	named, _ := reference.ParseNormalizedNamed("nginx:1.27")
	if !hasDigest(named, digests, "sha256:"+hex('c')) || hasDigest(named, digests, "sha256:"+hex('a')) {
		t.Error("hasDigest must match any digest of the same repository only")
	}
}

func hex(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return string(b)
}
