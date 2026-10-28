package main

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
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
