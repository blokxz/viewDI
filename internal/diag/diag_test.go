package diag

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// env builds a fake system: the socket is root:GID mode MODE, we are uid 1000,
// the session holds sessionGIDs and the group database lists dbGIDs.
func env(mode fs.FileMode, uid, gid uint32, session, db []uint32, exists bool) Env {
	return Env{
		Stat: func(string) (fs.FileMode, uint32, uint32, error) {
			if !exists {
				return 0, 0, 0, errors.New("no such file")
			}
			return mode, uid, gid, nil
		},
		GroupName:   func(g uint32) string { return map[uint32]string{999: "incus-admin", 998: "docker"}[g] },
		UserName:    func(u uint32) string { return map[uint32]string{0: "root", 1000: "me", 2000: "bob"}[u] },
		Current:     func() (string, uint32) { return "me", 1000 },
		SessionGIDs: func() []uint32 { return session },
		DatabaseGID: func() []uint32 { return db },
	}
}

func TestDiagnose(t *testing.T) {
	tests := []struct {
		name string
		env  Env
		want Verdict
		hint string
	}{
		{"socket missing", env(0, 0, 0, nil, nil, false), NoSocket, "not found at"},
		{"not a member", env(0o660, 0, 999, []uint32{1000}, []uint32{1000}, true), NotMember, "usermod -aG incus-admin"},
		{"member but stale session", env(0o660, 0, 999, []uint32{1000}, []uint32{1000, 999}, true), StaleSession, "sg incus-admin -c viewdi"},
		{"in the session already", env(0o660, 0, 999, []uint32{1000, 999}, []uint32{1000, 999}, true), Accessible, "daemon itself refused"},
		{"world writable", env(0o666, 0, 999, []uint32{1000}, nil, true), Accessible, "daemon itself refused"},
		{"owned by me", env(0o600, 1000, 998, []uint32{1000}, nil, true), Accessible, "daemon itself refused"},
		{"root only", env(0o600, 0, 0, []uint32{1000}, nil, true), NoGroupAccess, "no group can write"},
		{"another user's rootless socket", env(0o660, 2000, 4242, []uint32{1000}, nil, true), OtherOwner, "belongs to bob"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Diagnose(tt.env, "/run/incus/unix.socket")
			if r.Verdict != tt.want {
				t.Fatalf("verdict = %v, want %v (%+v)", r.Verdict, tt.want, r)
			}
			if h := Hint("Incus", r); !strings.Contains(h, tt.hint) {
				t.Errorf("hint %q lacks %q", h, tt.hint)
			}
		})
	}
}

func TestRestrictedSocketHintExplainsTheLimit(t *testing.T) {
	e := env(0o660, 0, 999, []uint32{1000, 999}, nil, true)
	h := Hint("Incus", Diagnose(e, "/run/incus/unix.socket.user"))
	if !strings.Contains(h, "restricted socket") {
		t.Fatalf("hint %q", h)
	}
}

func TestSocketPath(t *testing.T) {
	for in, want := range map[string]string{"unix:///var/run/docker.sock": "/var/run/docker.sock", "/run/incus/unix.socket": "/run/incus/unix.socket"} {
		if got, ok := SocketPath(in); !ok || got != want {
			t.Errorf("SocketPath(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"tcp://10.0.0.1:2375", "ssh://me@host", "", "unix://"} {
		if _, ok := SocketPath(in); ok {
			t.Errorf("SocketPath(%q) accepted", in)
		}
	}
}

// The real system must not panic whatever the path is.
func TestSystemEnv(t *testing.T) {
	r := Diagnose(System(), "/nonexistent/socket")
	if r.Verdict != NoSocket {
		t.Fatalf("verdict %v", r.Verdict)
	}
	_ = Diagnose(System(), "/dev/null")
}
