package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadMissingFileIsSilentDefault(t *testing.T) {
	cfg, warns := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if cfg.UI.Theme != DefaultTheme || time.Duration(cfg.UI.Refresh) != DefaultRefresh {
		t.Fatalf("not defaults: %+v", cfg)
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		doc       string
		wantWarns []string // substrings, in order
		check     func(t *testing.T, c Config)
	}{
		{
			name: "valid values",
			doc: `
[ui]
theme = "dracula"
refresh = "500ms"
fps = 60
[updates]
enabled = false
engine_interval = "48h"
ignore_images = ["postgres:*"]
`,
			check: func(t *testing.T, c Config) {
				if c.UI.Theme != "dracula" || time.Duration(c.UI.Refresh) != 500*time.Millisecond || c.UI.FPS != 60 {
					t.Errorf("ui = %+v", c.UI)
				}
				if c.Updates.Enabled || time.Duration(c.Updates.EngineInterval) != 48*time.Hour {
					t.Errorf("updates = %+v", c.Updates)
				}
				if !c.Updates.CheckImages {
					t.Errorf("unset key must keep its default")
				}
			},
		},
		{
			name: "engine endpoints",
			doc: `
[docker]
host = "unix:///run/user/1000/docker.sock"
[incus]
socket = "/var/lib/incus/unix.socket.user"
`,
			check: func(t *testing.T, c Config) {
				if c.Docker.Host != "unix:///run/user/1000/docker.sock" || c.Incus.Socket != "/var/lib/incus/unix.socket.user" {
					t.Errorf("endpoints = %+v %+v", c.Docker, c.Incus)
				}
			},
		},
		{
			name:      "invalid engine endpoints are dropped",
			doc:       "[docker]\nhost = \"/var/run/docker.sock\"\n[incus]\nsocket = \"unix.socket\"\n",
			wantWarns: []string{"docker.host", "incus.socket"},
			check: func(t *testing.T, c Config) {
				if c.Docker.Host != "" || c.Incus.Socket != "" {
					t.Errorf("endpoints = %+v %+v", c.Docker, c.Incus)
				}
			},
		},
		{
			name:      "alerts",
			doc:       "[alerts]\nmemory_percent = 80\ncpu_for = \"30s\"\nrestart_count = 1\nrestart_window = \"10s\"\n",
			wantWarns: []string{"alerts.restart_count", "alerts.restart_window"},
			check: func(t *testing.T, c Config) {
				a := c.Alerts
				if a.MemoryPercent != 80 || time.Duration(a.CPUFor) != 30*time.Second || a.RestartCount != 3 || time.Duration(a.RestartWindow) != 10*time.Minute {
					t.Errorf("alerts = %+v", a)
				}
			},
		},
		{
			name:      "stop timeout bounds",
			doc:       "[actions]\nstop_timeout = \"0s\"\n",
			wantWarns: []string{"actions.stop_timeout"},
			check: func(t *testing.T, c Config) {
				if time.Duration(c.Actions.StopTimeout) != 10*time.Second {
					t.Errorf("stop_timeout = %v", time.Duration(c.Actions.StopTimeout))
				}
			},
		},
		{
			name:      "logs buffer bounds",
			doc:       "[logs]\nbuffer_lines = 5\n",
			wantWarns: []string{"logs.buffer_lines"},
			check: func(t *testing.T, c Config) {
				if c.Logs.BufferLines != DefaultLogLines {
					t.Errorf("buffer_lines = %d", c.Logs.BufferLines)
				}
			},
		},
		{
			name:      "invalid TOML falls back entirely",
			doc:       "[ui\ntheme = 1",
			wantWarns: []string{"invalid TOML"},
			check: func(t *testing.T, c Config) {
				if c.UI.Theme != DefaultTheme {
					t.Errorf("theme = %q", c.UI.Theme)
				}
			},
		},
		{
			name:      "unknown keys warn but keep known ones",
			doc:       "[ui]\ntheme = \"nord\"\ncolour = \"red\"\n[extra]\nx = 1\n",
			wantWarns: []string{`"ui.colour"`, `"extra`},
			check: func(t *testing.T, c Config) {
				if c.UI.Theme != "nord" {
					t.Errorf("theme = %q", c.UI.Theme)
				}
			},
		},
		{
			name: "out of range values reset per field",
			doc: `
[ui]
refresh = "10ms"
fps = 0
[updates]
engine_source = "apt"
notify_min_level = "loud"
ignore_images = ["ok:*", "bad["]
`,
			wantWarns: []string{"ui.refresh", "ui.fps", "engine_source", "notify_min_level", "invalid glob"},
			check: func(t *testing.T, c Config) {
				d := Default()
				if c.UI.Refresh != d.UI.Refresh || c.UI.FPS != d.UI.FPS {
					t.Errorf("ui = %+v", c.UI)
				}
				if c.Updates.EngineSource != "auto" || c.Updates.NotifyMinLevel != "minor" {
					t.Errorf("updates = %+v", c.Updates)
				}
				if len(c.Updates.IgnoreImages) != 1 || c.Updates.IgnoreImages[0] != "ok:*" {
					t.Errorf("ignore_images = %v", c.Updates.IgnoreImages)
				}
			},
		},
		{
			name:      "bad duration string",
			doc:       "[ui]\nrefresh = \"soon\"\n",
			wantWarns: []string{"invalid TOML"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, warns := Parse("config.toml", []byte(tt.doc))
			if len(warns) != len(tt.wantWarns) {
				t.Fatalf("warnings = %v, want %d matching %v", warns, len(tt.wantWarns), tt.wantWarns)
			}
			for i, sub := range tt.wantWarns {
				if !strings.Contains(warns[i].String(), sub) {
					t.Errorf("warning %d = %q, want substring %q", i, warns[i], sub)
				}
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func TestReadCappedRejectsLargeAndNonRegular(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big")
	if err := os.WriteFile(big, make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCapped(big, 10); err == nil {
		t.Error("expected size error")
	}
	if _, err := ReadCapped(dir, 10); err == nil {
		t.Error("expected non-regular error")
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte("[ui]\ntheme = \"x\"\nrefresh = \"1s\"\n"))
	f.Add([]byte("[updates]\nignore_images = [\"a*\"]\n"))
	f.Add([]byte("\x00["))
	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, _ := Parse("fuzz", data)
		r := time.Duration(cfg.UI.Refresh)
		if r < MinRefresh || r > MaxRefresh || cfg.UI.FPS < 1 || cfg.UI.FPS > MaxFPS || cfg.UI.Theme == "" {
			t.Fatalf("validated config out of bounds: %+v", cfg.UI)
		}
	})
}
