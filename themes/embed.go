// Package themes embeds the built-in theme palettes (*.toml) into the binary.
// The format is documented in internal/ui/theme.
package themes

import "embed"

// FS holds every built-in theme file at its base name (e.g. "nord.toml").
//
//go:embed *.toml
var FS embed.FS
