package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Refresh-rate bounds for the metrics UI tick (0.5–4 Hz, docs/02-architecture.md §4.5).
const (
	MinRefresh     = 250 * time.Millisecond
	MaxRefresh     = 2 * time.Second
	DefaultRefresh = time.Second
	DefaultFPS     = 30
	MaxFPS         = 120
	DefaultTheme   = "everforest-dark"
)

// maxConfigSize caps config.toml; anything larger is not a hand-written file.
const maxConfigSize = 1 << 20

// Duration is a time.Duration that decodes from TOML strings such as "24h".
type Duration time.Duration

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) { return []byte(time.Duration(d).String()), nil }

// Config is the content of config.toml. Zero values never reach callers: Load
// always returns a fully defaulted and validated Config.
type Config struct {
	UI      UIConfig      `toml:"ui"`
	Updates UpdatesConfig `toml:"updates"`
}

// UIConfig holds presentation settings.
type UIConfig struct {
	Theme   string   `toml:"theme"`   // theme ID, e.g. "dracula"
	Refresh Duration `toml:"refresh"` // metrics UI tick
	FPS     int      `toml:"fps"`     // renderer frame cap
}

// UpdatesConfig mirrors the [updates] section of docs/01-research.md §3.4.C.
// It is validated now so users can copy the documented section verbatim; the
// checker itself arrives in Stage 6.
type UpdatesConfig struct {
	Enabled        bool     `toml:"enabled"`
	CheckOnStartup bool     `toml:"check_on_startup"`
	EngineInterval Duration `toml:"engine_interval"`
	ImageInterval  Duration `toml:"image_interval"`
	EngineSource   string   `toml:"engine_source"`
	IncusChannel   string   `toml:"incus_channel"`
	CheckImages    bool     `toml:"check_images"`
	IgnoreImages   []string `toml:"ignore_images"`
	NotifyDesktop  bool     `toml:"notify_desktop"`
	NotifyMinLevel string   `toml:"notify_min_level"`
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		UI: UIConfig{
			Theme:   DefaultTheme,
			Refresh: Duration(DefaultRefresh),
			FPS:     DefaultFPS,
		},
		Updates: UpdatesConfig{
			Enabled:        true,
			CheckOnStartup: true,
			EngineInterval: Duration(24 * time.Hour),
			ImageInterval:  Duration(6 * time.Hour),
			EngineSource:   "auto",
			IncusChannel:   "auto",
			CheckImages:    true,
			NotifyDesktop:  false,
			NotifyMinLevel: "minor",
		},
	}
}

// Load reads config.toml at path. A missing file yields defaults silently; any
// other problem yields defaults (fully or per field) plus warnings.
func Load(path string) (Config, []Warning) {
	cfg := Default()
	data, err := ReadCapped(path, maxConfigSize)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, []Warning{{Source: path, Msg: fmt.Sprintf("cannot read, using defaults: %v", err)}}
	}
	return Parse(path, data)
}

// Parse decodes config.toml content. source is used to label warnings.
func Parse(source string, data []byte) (Config, []Warning) {
	cfg := Default()
	var warns []Warning

	// Lenient decode first: unknown keys must not discard the known ones.
	if err := toml.NewDecoder(bytes.NewReader(data)).Decode(&cfg); err != nil {
		return Default(), []Warning{{Source: source, Msg: fmt.Sprintf("invalid TOML, using defaults: %v", err)}}
	}

	// Strict decode into a throwaway value only to report unknown keys.
	var strict Config
	err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&strict)
	var sme *toml.StrictMissingError
	if errors.As(err, &sme) {
		for _, e := range sme.Errors {
			warns = append(warns, Warning{Source: source, Msg: fmt.Sprintf("unknown key %q ignored", strings.Join(e.Key(), "."))})
		}
	}

	warns = append(warns, cfg.validate(source)...)
	return cfg, warns
}

// validate resets invalid fields to their defaults and reports each one.
func (c *Config) validate(source string) []Warning {
	def := Default()
	var warns []Warning
	bad := func(field string, value any, why string) {
		warns = append(warns, Warning{Source: source, Msg: fmt.Sprintf("%s = %v: %s, using default", field, value, why)})
	}

	if strings.TrimSpace(c.UI.Theme) == "" {
		c.UI.Theme = def.UI.Theme
	}
	if r := time.Duration(c.UI.Refresh); r < MinRefresh || r > MaxRefresh {
		bad("ui.refresh", r, fmt.Sprintf("must be between %s and %s", MinRefresh, MaxRefresh))
		c.UI.Refresh = def.UI.Refresh
	}
	if c.UI.FPS < 1 || c.UI.FPS > MaxFPS {
		bad("ui.fps", c.UI.FPS, fmt.Sprintf("must be between 1 and %d", MaxFPS))
		c.UI.FPS = def.UI.FPS
	}

	u := &c.Updates
	if time.Duration(u.EngineInterval) < time.Hour {
		bad("updates.engine_interval", time.Duration(u.EngineInterval), "must be at least 1h")
		u.EngineInterval = def.Updates.EngineInterval
	}
	if time.Duration(u.ImageInterval) < 15*time.Minute {
		bad("updates.image_interval", time.Duration(u.ImageInterval), "must be at least 15m")
		u.ImageInterval = def.Updates.ImageInterval
	}
	if !oneOf(u.EngineSource, "auto", "package", "upstream") {
		bad("updates.engine_source", u.EngineSource, "expected auto, package or upstream")
		u.EngineSource = def.Updates.EngineSource
	}
	if !oneOf(u.IncusChannel, "auto", "lts", "stable") {
		bad("updates.incus_channel", u.IncusChannel, "expected auto, lts or stable")
		u.IncusChannel = def.Updates.IncusChannel
	}
	if !oneOf(u.NotifyMinLevel, "patch", "minor", "major", "security") {
		bad("updates.notify_min_level", u.NotifyMinLevel, "expected patch, minor, major or security")
		u.NotifyMinLevel = def.Updates.NotifyMinLevel
	}
	kept := u.IgnoreImages[:0]
	for _, pat := range u.IgnoreImages {
		if _, err := path.Match(pat, ""); err != nil {
			bad("updates.ignore_images", fmt.Sprintf("%q", pat), "invalid glob")
			continue
		}
		kept = append(kept, pat)
	}
	u.IgnoreImages = kept
	return warns
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// ReadCapped reads a regular file of at most limit bytes. Shared by the loaders
// of user-supplied files (config, themes, keys).
func ReadCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if st.Size() > limit {
		return nil, fmt.Errorf("file too large (%d bytes, limit %d)", st.Size(), limit)
	}
	buf := bytes.NewBuffer(make([]byte, 0, st.Size()))
	if _, err := buf.ReadFrom(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
