// Package textsafe makes untrusted text (container logs, API payloads) safe
// to draw in the terminal: it removes escape sequences and control
// characters that could move the cursor, retitle the window, write to the
// clipboard or reorder text.
package textsafe

import (
	"strings"
	"unicode/utf8"
)

// TabWidth is the number of spaces a tab becomes.
const TabWidth = 4

// Clean returns s without terminal escape sequences (CSI, OSC, DCS, SOS,
// PM, APC and two-byte escapes, in their 7- and 8-bit forms), C0/C1 control
// characters and bidirectional overrides. Tabs become spaces, invalid UTF-8
// becomes U+FFFD, and a carriage return keeps only the text after it (the
// last state of a progress bar).
func Clean(s string) string {
	if plain(s) {
		return s
	}
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		s = s[i+1:]
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune(utf8.RuneError)
			i++
			continue
		case r == 0x1b:
			i = skipEscape(s, i+1)
			continue
		case r == 0x9b: // 8-bit CSI
			i = skipCSI(s, i+size)
			continue
		case r == 0x90 || r == 0x98 || r == 0x9d || r == 0x9e || r == 0x9f: // 8-bit DCS, SOS, OSC, PM, APC
			i = skipString(s, i+size)
			continue
		case r == '\t':
			b.WriteString(strings.Repeat(" ", TabWidth))
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
		case bidi(r):
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// plain reports whether s is printable ASCII only (the common case).
func plain(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// bidi reports bidirectional embedding, override and isolate controls.
func bidi(r rune) bool {
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x200e || r == 0x200f || r == 0x061c
}

// skipEscape returns the index after the escape sequence whose ESC ended at i.
func skipEscape(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[':
		return skipCSI(s, i+1)
	case ']', 'P', 'X', '^', '_':
		return skipString(s, i+1)
	}
	// nF escapes: intermediates 0x20–0x2F, then one final byte.
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
		i++
	}
	return i
}

// skipCSI skips parameter and intermediate bytes up to the final byte.
func skipCSI(s string, i int) int {
	for i < len(s) {
		switch c := s[i]; {
		case c >= 0x40 && c <= 0x7e:
			return i + 1
		case c < 0x20 || c > 0x7e: // not part of a CSI: the sequence is broken
			return i
		}
		i++
	}
	return i
}

// skipString skips a control string up to its terminator: BEL, ESC \ or
// the 8-bit ST (U+009C).
func skipString(s string, i int) int {
	for i < len(s) {
		switch {
		case s[i] == 0x07:
			return i + 1
		case s[i] == 0x1b:
			if i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
			return i + 1
		case strings.HasPrefix(s[i:], "\u009c"):
			return i + len("\u009c")
		}
		i++
	}
	return i
}
