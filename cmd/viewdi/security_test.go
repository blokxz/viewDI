package main

import (
	"strings"
	"testing"
)

func TestSafeURL(t *testing.T) {
	ok := []string{"https://github.com/moby/moby/releases/tag/v29", "https://example.com/a?b=c#d"}
	for _, u := range ok {
		if got, err := safeURL(u); err != nil || got == "" {
			t.Errorf("safeURL(%q) = %q, %v", u, got, err)
		}
	}
	bad := []string{
		"", "http://example.com", "file:///etc/passwd", "javascript:alert(1)", "-o/tmp/x", "--help",
		"https://", "https:example.com", "https://user:pw@example.com", "ssh://host", "mailto:a@b.c",
		"https://example.com/a b", "https://example.com/\x1b[2J", "https://example.com/\n", "//example.com",
		"https://example.com/" + strings.Repeat("a", maxURL),
	}
	for _, u := range bad {
		if got, err := safeURL(u); err == nil {
			t.Errorf("safeURL(%q) accepted as %q", u, got)
		}
	}
}

func FuzzSafeURL(f *testing.F) {
	for _, s := range []string{"https://a.b/c", "", "-x", "https://%zz", "https://[::1]/x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, err := safeURL(s); err == nil && !strings.HasPrefix(got, "https://") {
			t.Fatalf("accepted %q as %q", s, got)
		}
	})
}
