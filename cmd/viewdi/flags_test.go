package main

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/config"
)

func TestParseFlags(t *testing.T) {
	tests := []struct {
		args    []string
		wantErr string
		check   func(t *testing.T, f flags)
	}{
		{args: nil, check: func(t *testing.T, f flags) {
			if f.Mock != "" || f.Trace != "" || f.ReadOnly {
				t.Errorf("defaults = %+v", f)
			}
		}},
		{args: []string{"--mock"}, check: func(t *testing.T, f flags) {
			if f.Mock != "homelab" {
				t.Errorf("mock = %q", f.Mock)
			}
		}},
		{args: []string{"--mock=chaos", "--read-only", "--debug", "--theme", "nord", "--refresh", "500ms"}, check: func(t *testing.T, f flags) {
			if f.Mock != "chaos" || !f.ReadOnly || !f.Debug || f.Theme != "nord" || f.Refresh != 500*time.Millisecond {
				t.Errorf("got %+v", f)
			}
		}},
		{args: []string{"--trace"}, check: func(t *testing.T, f flags) {
			if f.Trace != traceAuto {
				t.Errorf("trace = %q", f.Trace)
			}
		}},
		{args: []string{"--trace=/tmp/t.log", "--pprof=127.0.0.1:6060"}, check: func(t *testing.T, f flags) {
			if f.Trace != "/tmp/t.log" || f.Pprof != "127.0.0.1:6060" {
				t.Errorf("got %+v", f)
			}
		}},
		{args: []string{"--pprof=localhost:6060"}},
		{args: []string{"--pprof=[::1]:6060"}},
		{args: []string{"--mock", "chaos"}, wantErr: "did you mean --mock=chaos"},
		{args: []string{"--mock=nope"}, wantErr: "unknown scenario"},
		{args: []string{"--mock-notify"}, wantErr: "needs --mock"},
		{args: []string{"--mock", "--mock-notify"}, check: func(t *testing.T, f flags) {
			if f.Mock != "homelab" || !f.MockNotify {
				t.Errorf("got %+v", f)
			}
		}},
		{args: []string{"--refresh=10ms"}, wantErr: "outside"},
		{args: []string{"--pprof=0.0.0.0:6060"}, wantErr: "not a loopback"},
		{args: []string{"--pprof=6060"}, wantErr: "--pprof"},
		{args: []string{"--bogus"}, wantErr: "unknown flag"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			f, err := parseFlags(tt.args, io.Discard)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.check != nil {
				tt.check(t, f)
			}
		})
	}
}

func TestHelp(t *testing.T) {
	var out strings.Builder
	_, err := parseFlags([]string{"--help"}, &out)
	if !errors.Is(err, errHelp) || !strings.Contains(out.String(), "--mock") {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
}

func TestASCIITerminal(t *testing.T) {
	tests := []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"TERM": "xterm-256color", "LANG": "en_US.UTF-8"}, false},
		{map[string]string{"TERM": "linux", "LANG": "en_US.UTF-8"}, true},
		{map[string]string{"LANG": "C"}, true},
		{map[string]string{"LC_ALL": "es_ES.utf8", "LANG": "C"}, false},
		{map[string]string{"LC_CTYPE": "POSIX"}, true},
		{map[string]string{}, false},
	}
	for _, tt := range tests {
		if got := asciiTerminal(func(k string) string { return tt.env[k] }); got != tt.want {
			t.Errorf("%v: %v, want %v", tt.env, got, tt.want)
		}
	}
}

func TestDesktopNotifier(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	on := config.Default()
	on.Updates.NotifyDesktop = true
	off := config.Default()
	disabled := on
	disabled.Updates.Enabled = false
	tests := []struct {
		name string
		f    flags
		cfg  config.Config
		want bool
	}{
		{"default config", flags{}, off, false},
		{"notify_desktop", flags{}, on, true},
		{"checks disabled", flags{}, disabled, false},
		{"mock ignores the config", flags{Mock: "homelab"}, on, false},
		{"mock with --mock-notify", flags{Mock: "homelab", MockNotify: true}, off, true},
	}
	for _, tt := range tests {
		if got := desktopNotifier(tt.f, tt.cfg, logger) != nil; got != tt.want {
			t.Errorf("%s: notifier %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestUpdateFlags(t *testing.T) {
	if f, err := parseFlags([]string{"--update", "--yes", "--force"}, io.Discard); err != nil || !f.Update || !f.Yes || !f.Force {
		t.Fatalf("%+v %v", f, err)
	}
	for _, args := range [][]string{{"--yes"}, {"--force"}} {
		if _, err := parseFlags(args, io.Discard); err == nil || !strings.Contains(err.Error(), "only go with --update") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}

func TestUpdateRefusesADevelopmentBuild(t *testing.T) {
	var out, errb strings.Builder
	if code := run([]string{"--update"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "development build") {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
}
