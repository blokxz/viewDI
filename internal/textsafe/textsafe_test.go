package textsafe

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClean(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain text", "plain text"},
		{"\x1b[31mred\x1b[0m", "red"},
		{"\x1b[38;2;255;0;0mtruecolor\x1b[m", "truecolor"},
		{"\x1b]0;evil title\x07after", "after"},
		{"\x1b]52;c;ZXZpbA==\x1b\\clip", "clip"},
		{"\x1bP1$qm\x1b\\dcs", "dcs"},
		{"\x1b(Bcharset", "charset"},
		{"\x1bccleared", "cleared"},
		{"\u009b2Jcsi8", "csi8"},
		{"\u009d0;t\u009cosc8", "osc8"},
		{"a\tb", "a    b"},
		{"bell\x07 nul\x00 del\x7f", "bell nul del"},
		{"10%\r50%\r100%", "100%"},
		{"bidi \u202eevil\u202c", "bidi evil"},
		{"bad \xff utf8", "bad � utf8"},
		{"héllo wörld ✓", "héllo wörld ✓"},
		{"\x1b[", ""},
		{"\x1b]unterminated", ""},
		{"trailing esc\x1b", "trailing esc"},
	}
	for _, c := range cases {
		if got := Clean(c.in); got != c.want {
			t.Errorf("Clean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func FuzzClean(f *testing.F) {
	for _, s := range []string{"\x1b[31mx", "\x1b]0;t\x07", "a\tb\r", "\u009b1m", "\xff\xfe", "\u202e"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := Clean(s)
		if !utf8.ValidString(got) {
			t.Fatalf("invalid UTF-8 in %q", got)
		}
		for _, r := range got {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || bidi(r) {
				t.Fatalf("control %U left in %q (from %q)", r, got, s)
			}
		}
		if strings.ContainsRune(got, 0x1b) {
			t.Fatalf("escape left in %q", got)
		}
		if Clean(got) != got {
			t.Fatalf("not idempotent: %q", got)
		}
	})
}

func BenchmarkClean(b *testing.B) {
	lines := []string{
		"2026-10-29T10:00:00Z GET /health 200 0.4ms",
		"\x1b[32mINFO\x1b[0m server started on :8080 \t(pid 1)",
	}
	for b.Loop() {
		for _, l := range lines {
			_ = Clean(l)
		}
	}
}
