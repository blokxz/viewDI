// Package library embeds the built-in container library (*.toml). The format
// is documented in internal/catalog.
package library

import "embed"

// FS holds every built-in library file at its base name.
//
//go:embed *.toml
var FS embed.FS
