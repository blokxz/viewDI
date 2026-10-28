// Package theme implements viewDI's theme engine: TOML palettes mapped to
// semantic tokens, a registry of built-in and user themes, and a style cache
// rebuilt once per theme change.
//
// Theme file format (themes/*.toml or ~/.config/viewDI/themes/*.toml):
//
//	name = "My Theme"          # display name (defaults to the file name)
//	dark = true                # whether the background is dark
//
//	[palette]                  # optional named colors
//	green = "#a7c080"
//
//	[tokens]                   # semantic tokens: "#rrggbb", "#rgb" or a palette name
//	accent = "green"
//	bg = "#2d353b"
//
// Required tokens: bg, fg, accent, ok, warn, err. Every other token falls back
// to a related one (see fallbacks), so small user themes stay valid.
package theme

import (
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Token is a semantic color role. Components reference tokens, never colors.
type Token int

// Semantic tokens (docs/02-architecture.md §6).
const (
	Bg Token = iota
	BgAlt
	Fg
	FgMuted
	Border
	BorderFocus
	SelectionBg
	SelectionFg
	Accent
	AccentAlt
	OK
	Warn
	Err
	Info
	Update
	EngineDocker
	EngineIncus
	TreeGuide
	TreeCursorBg
	TreeGroup
	ChartCPU
	ChartMem
	ChartNetRx
	ChartNetTx
	ChartDisk
	numTokens
)

var tokenNames = [numTokens]string{
	"bg", "bg_alt", "fg", "fg_muted", "border", "border_focus", "selection_bg", "selection_fg",
	"accent", "accent_alt", "ok", "warn", "err", "info", "update",
	"engine_docker", "engine_incus", "tree_guide", "tree_cursor_bg", "tree_group",
	"chart_cpu", "chart_mem", "chart_net_rx", "chart_net_tx", "chart_disk",
}

func (t Token) String() string {
	if t < 0 || t >= numTokens {
		return "token(" + strconv.Itoa(int(t)) + ")"
	}
	return tokenNames[t]
}

// Tokens returns every token in declaration order.
func Tokens() []Token {
	out := make([]Token, numTokens)
	for i := range out {
		out[i] = Token(i)
	}
	return out
}

var required = []Token{Bg, Fg, Accent, OK, Warn, Err}

// fallbacks resolve optional tokens. Order matters: a token's fallback is
// always resolved before it (checked by TestFallbackOrder).
var fallbacks = []struct{ tok, from Token }{
	{BgAlt, Bg},
	{FgMuted, Fg},
	{Border, FgMuted},
	{BorderFocus, Accent},
	{SelectionBg, Accent},
	{SelectionFg, Bg},
	{AccentAlt, Accent},
	{Info, Accent},
	{Update, Info},
	{EngineDocker, Info},
	{EngineIncus, AccentAlt},
	{TreeGuide, Border},
	{TreeCursorBg, SelectionBg},
	{TreeGroup, Accent},
	{ChartCPU, OK},
	{ChartMem, Info},
	{ChartNetRx, AccentAlt},
	{ChartNetTx, Update},
	{ChartDisk, Warn},
}

// Theme is a fully resolved palette: every token has a color.
type Theme struct {
	ID     string // file stem, e.g. "everforest-dark"
	Name   string // display name
	Dark   bool
	Source string // "built-in" or the user file path
	colors [numTokens]color.Color
}

// Color returns the color of a token.
func (t *Theme) Color(tok Token) color.Color { return t.colors[tok] }

// Hex returns a token's color as "#rrggbb".
func (t *Theme) Hex(tok Token) string { return toHex(t.colors[tok]) }

type file struct {
	Name    string            `toml:"name"`
	Dark    *bool             `toml:"dark"`
	Palette map[string]string `toml:"palette"`
	Tokens  map[string]string `toml:"tokens"`
}

// Parse decodes and validates a theme file. id is the file stem.
func Parse(id string, data []byte) (*Theme, error) {
	var f file
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&f); err != nil {
		var sme *toml.StrictMissingError
		if errors.As(err, &sme) {
			keys := make([]string, 0, len(sme.Errors))
			for _, e := range sme.Errors {
				keys = append(keys, strings.Join(e.Key(), "."))
			}
			return nil, fmt.Errorf("theme %s: unknown keys: %s", id, strings.Join(keys, ", "))
		}
		return nil, fmt.Errorf("theme %s: %w", id, err)
	}
	t := &Theme{ID: id, Name: strings.TrimSpace(f.Name), Dark: true}
	if t.Name == "" {
		t.Name = id
	}
	if f.Dark != nil {
		t.Dark = *f.Dark
	}

	palette := make(map[string]color.Color, len(f.Palette))
	for name, v := range f.Palette {
		c, err := parseHex(v)
		if err != nil {
			return nil, fmt.Errorf("theme %s: palette.%s: %w", id, name, err)
		}
		palette[name] = c
	}

	byName := make(map[string]Token, numTokens)
	for _, tok := range Tokens() {
		byName[tok.String()] = tok
	}
	var errs []error
	for name, v := range f.Tokens {
		tok, ok := byName[name]
		if !ok {
			errs = append(errs, fmt.Errorf("unknown token %q", name))
			continue
		}
		c, err := resolve(v, palette)
		if err != nil {
			errs = append(errs, fmt.Errorf("tokens.%s: %w", name, err))
			continue
		}
		t.colors[tok] = c
	}
	for _, tok := range required {
		if t.colors[tok] == nil {
			errs = append(errs, fmt.Errorf("missing required token %q", tok))
		}
	}
	if len(errs) > 0 {
		slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
		return nil, fmt.Errorf("theme %s: %w", id, errors.Join(errs...))
	}
	for _, fb := range fallbacks {
		if t.colors[fb.tok] == nil {
			t.colors[fb.tok] = t.colors[fb.from]
		}
	}
	return t, nil
}

func resolve(v string, palette map[string]color.Color) (color.Color, error) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "#") {
		return parseHex(v)
	}
	if c, ok := palette[v]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("%q is neither a hex color nor a palette name", v)
}

// parseHex accepts "#rgb" and "#rrggbb".
func parseHex(s string) (color.Color, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "#") || (len(s) != 4 && len(s) != 7) {
		return nil, fmt.Errorf("invalid color %q (want #rgb or #rrggbb)", s)
	}
	hex := s[1:]
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid color %q", s)
	}
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}, nil
}

func toHex(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}
