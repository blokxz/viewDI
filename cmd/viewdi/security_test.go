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

func TestSafeAddress(t *testing.T) {
	ok := []string{
		"http://10.10.0.50", "https://192.168.1.1", "http://172.16.0.3:8080", "http://localhost:3000",
		"http://127.0.0.1:9000/", "http://169.254.1.1", "https://100.64.0.9", "http://10.0.0.1/admin",
	}
	for _, u := range ok {
		if got, err := safeAddress(u); err != nil || got == "" {
			t.Errorf("safeAddress(%q) = %q, %v", u, got, err)
		}
	}
	bad := []string{
		"", "ftp://10.0.0.1", "file:///etc/passwd", "javascript:alert(1)", "-o/tmp/x",
		"http://8.8.8.8", "https://example.com", "http://10.0.0.1.evil.com", "http://user:pw@10.0.0.1",
		"http://[::1]:80", "http://10.0.0.1:99999", "http://10.0.0.1:0", "http://10.0.0.1/a b",
		"http://10.0.0.1/\x1b[2J", "http://10.0.0.1?x=1", "http://10.0.0.1#frag", "//10.0.0.1", "http://0.0.0.0",
		"http://192.168.1.1@evil.com", "http://100.128.0.1", "http://172.32.0.1",
	}
	for _, u := range bad {
		if got, err := safeAddress(u); err == nil {
			t.Errorf("safeAddress(%q) accepted as %q", u, got)
		}
	}
}

func FuzzSafeAddress(f *testing.F) {
	for _, s := range []string{"http://10.0.0.1", "", "http://[fe80::1%25eth0]/", "http://localhost:0", "https://100.64.0.1:8443"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, err := safeAddress(s)
		if err != nil {
			return
		}
		if !strings.HasPrefix(got, "http://") && !strings.HasPrefix(got, "https://") {
			t.Fatalf("accepted %q as %q", s, got)
		}
	})
}
