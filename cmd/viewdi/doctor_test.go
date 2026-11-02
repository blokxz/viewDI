package main

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
)

func testDoctorEnv(dockerErr, incusErr error) doctorEnv {
	return doctorEnv{
		getenv:   func(k string) string { return map[string]string{"TERM": "xterm-256color", "LANG": "en_US.UTF-8"}[k] },
		lookPath: func(string) (string, error) { return "/usr/bin/x", nil },
		exists:   func(string) bool { return true },
		paths:    config.PathsUnder("/nonexistent"),
		dockerAt: func(context.Context, string) (engine.Info, error) {
			return engine.Info{Engine: engine.Docker, Version: "29.1.0"}, dockerErr
		},
		incusAt: func(context.Context, string) (engine.Info, error) {
			return engine.Info{Engine: engine.Incus, Version: "6.7"}, incusErr
		},
	}
}

func run1(t *testing.T, env doctorEnv) (string, int, []finding) {
	t.Helper()
	fs := doctor(context.Background(), env, flags{}, config.Default())
	var out strings.Builder
	code := renderDoctor(&out, fs)
	return out.String(), code, fs
}

func TestDoctorAllGood(t *testing.T) {
	out, code, _ := run1(t, testDoctorEnv(nil, nil))
	if code != 0 || !strings.Contains(out, "Everything viewDI needs is in place") || strings.Contains(out, "FAIL") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestDoctorOneEngineDownStillWorks(t *testing.T) {
	out, code, _ := run1(t, testDoctorEnv(nil, &engine.Error{Class: engine.ClassMissing, Op: "ping", Err: syscall.ENOENT}))
	if code != 0 || !strings.Contains(out, "works with the engine that is ok") || !strings.Contains(out, "FAIL") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestDoctorNoEngineFails(t *testing.T) {
	denied := &engine.Error{Class: engine.ClassPermission, Op: "ping", Err: syscall.EACCES}
	out, code, fs := run1(t, testDoctorEnv(denied, denied))
	if code != 1 || !strings.Contains(out, "cannot reach Docker or Incus") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	for _, f := range fs {
		if (f.name == "Docker" || f.name == "Incus") && f.fix == "" {
			t.Errorf("%s failure comes without a fix", f.name)
		}
	}
}

func TestDoctorReportsWeakSpots(t *testing.T) {
	env := testDoctorEnv(nil, nil)
	env.exists = func(string) bool { return false }
	env.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	env.getenv = func(k string) string {
		return map[string]string{"TERM": "dumb", "DOCKER_HOST": "tcp://10.0.0.1:2375"}[k]
	}
	out, code, _ := run1(t, env)
	for _, want := range []string{"cgroup v2", "Terminal", "xdg-open", "notify-send", "has no TLS", "none of apt, pacman, dnf, zypper"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if code != 0 {
		t.Errorf("weak spots alone must not fail, code=%d", code)
	}
}
