// Package secret decides which configuration values the UI masks by
// default: environment variables and config keys whose names look like
// they hold credentials.
package secret

import (
	"regexp"
	"strings"
)

// Mask replaces a hidden value.
const Mask = "••••••"

var secretName = regexp.MustCompile(`(?i)(passw|pwd|secret|token|api_?key|private|credential|(^|[_.-])key$|auth)`)

// IsSecret reports whether a variable or config key name looks secret.
func IsSecret(name string) bool {
	if strings.Contains(strings.ToLower(name), "author") {
		return false
	}
	return secretName.MatchString(name)
}

// Env masks the value of a "KEY=value" entry whose key looks secret. It
// reports whether it masked anything.
func Env(kv string) (string, bool) {
	k, v, ok := strings.Cut(kv, "=")
	if !ok || v == "" || !IsSecret(k) {
		return kv, false
	}
	return k + "=" + Mask, true
}
