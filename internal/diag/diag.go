// Package diag explains why a daemon socket cannot be used, so the hint shown
// to the user is the one that fixes their case rather than a generic one.
//
// The usual trap is a stale login session: `usermod -aG` changes the group
// database, but the running shell (and everything started from it) keeps the
// groups it had at login, so viewDI is still refused although `id` of a fresh
// login, or `getent group`, lists the user. Diagnose tells the cases apart:
//
//	no socket        the daemon is not running or the path is wrong
//	not a member     the user is not in the socket's group at all
//	stale session    the user is in the group, this session predates it
//	accessible       the socket permissions allow us, so the refusal comes
//	                 from the daemon (a restricted socket, a project ACL)
//	no group access  the socket is not group-writable at all
package diag

import (
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// Verdict is the diagnosis of one socket.
type Verdict int

// Verdicts.
const (
	NoSocket Verdict = iota
	Accessible
	NotMember
	StaleSession
	NoGroupAccess
	OtherOwner
)

// Result is what Diagnose found.
type Result struct {
	Path    string
	Verdict Verdict
	Mode    fs.FileMode
	Group   string // group name of the socket ("" if unknown)
	Owner   string // owner name
	User    string // current user
}

// Env is the part of the system Diagnose looks at; tests replace it.
type Env struct {
	Stat        func(path string) (mode fs.FileMode, uid, gid uint32, err error)
	GroupName   func(gid uint32) string
	UserName    func(uid uint32) string
	Current     func() (name string, uid uint32)
	SessionGIDs func() []uint32 // groups of this process
	DatabaseGID func() []uint32 // groups the user belongs to now, per the group database
}

// System is the real environment.
func System() Env {
	return Env{
		Stat: func(path string) (fs.FileMode, uint32, uint32, error) {
			fi, err := os.Stat(path)
			if err != nil {
				return 0, 0, 0, err
			}
			st, ok := fi.Sys().(*syscall.Stat_t)
			if !ok {
				return fi.Mode(), 0, 0, nil
			}
			return fi.Mode(), st.Uid, st.Gid, nil
		},
		GroupName: func(gid uint32) string {
			if g, err := user.LookupGroupId(strconv.FormatUint(uint64(gid), 10)); err == nil {
				return g.Name
			}
			return ""
		},
		UserName: func(uid uint32) string {
			if u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err == nil {
				return u.Username
			}
			return ""
		},
		Current: func() (string, uint32) {
			name := ""
			if u, err := user.Current(); err == nil {
				name = u.Username
			}
			return name, uint32(os.Geteuid())
		},
		SessionGIDs: func() []uint32 {
			gids := []uint32{uint32(os.Getegid())}
			if more, err := os.Getgroups(); err == nil {
				for _, g := range more {
					gids = append(gids, uint32(g))
				}
			}
			return gids
		},
		DatabaseGID: func() []uint32 {
			u, err := user.Current()
			if err != nil {
				return nil
			}
			ids, err := u.GroupIds()
			if err != nil {
				return nil
			}
			var out []uint32
			for _, s := range ids {
				if n, err := strconv.ParseUint(s, 10, 32); err == nil {
					out = append(out, uint32(n))
				}
			}
			return out
		},
	}
}

// Diagnose looks at the socket at path.
func Diagnose(env Env, path string) Result {
	r := Result{Path: path}
	r.User, _ = env.Current()
	mode, uid, gid, err := env.Stat(path)
	if err != nil {
		r.Verdict = NoSocket
		return r
	}
	r.Mode = mode.Perm()
	r.Group, r.Owner = env.GroupName(gid), env.UserName(uid)
	_, me := env.Current()
	inSession := slices.Contains(env.SessionGIDs(), gid)
	switch {
	case me == uid:
		r.Verdict = Accessible
		if r.Mode&0o600 != 0o600 {
			r.Verdict = NoGroupAccess
		}
	case inSession && r.Mode&0o060 == 0o060:
		r.Verdict = Accessible
	case r.Mode&0o006 == 0o006:
		r.Verdict = Accessible
	case r.Mode&0o060 != 0o060:
		r.Verdict = NoGroupAccess
	case slices.Contains(env.DatabaseGID(), gid):
		r.Verdict = StaleSession
	default:
		r.Verdict = NotMember
	}
	if r.Verdict == NotMember && uid != 0 && r.Group == "" {
		r.Verdict = OtherOwner
	}
	return r
}

// Hint turns a diagnosis into the text shown to the user. engine is "Docker"
// or "Incus".
func Hint(engine string, r Result) string {
	group := r.Group
	if group == "" {
		group = "the socket's group"
	}
	switch r.Verdict {
	case NoSocket:
		return fmt.Sprintf("%s socket not found at %s. Is the daemon installed and running? (systemctl status %s)", engine, r.Path, strings.ToLower(engine))
	case NotMember:
		return fmt.Sprintf("Permission denied on %s: you are not in the %q group. Run: sudo usermod -aG %s $USER, then log out and back in (or start viewDI with: sg %s -c viewdi).", r.Path, group, group, group)
	case StaleSession:
		return fmt.Sprintf("You are in the %q group, but this session started before that: groups are read at login. Log out and back in, or run: sg %s -c viewdi (running newgrp %s and then viewdi in the same shell also works).", group, group, group)
	case NoGroupAccess:
		return fmt.Sprintf("%s is mode %04o (owner %s, group %s): no group can write to it. Check the daemon's socket settings.", r.Path, r.Mode, nonEmpty(r.Owner, "?"), nonEmpty(r.Group, "?"))
	case OtherOwner:
		return fmt.Sprintf("%s belongs to %s, and you (%s) are not allowed to use it. Use your own daemon socket or ask its owner.", r.Path, nonEmpty(r.Owner, "another user"), r.User)
	case Accessible:
		extra := ""
		if filepath.Base(r.Path) == "unix.socket.user" {
			extra = " This is the restricted socket of the incus group: some calls are refused there; the incus-admin group gives full access."
		}
		return fmt.Sprintf("The permissions of %s allow you (mode %04o, group %s), so the daemon itself refused the request.%s Check its logs (journalctl -u %s).", r.Path, r.Mode, nonEmpty(r.Group, "?"), extra, strings.ToLower(engine))
	}
	return ""
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// SocketPath extracts a filesystem path from an endpoint ("unix:///run/x.sock",
// "/run/x.sock"); ok is false for tcp:// and ssh:// endpoints.
func SocketPath(endpoint string) (string, bool) {
	if rest, ok := strings.CutPrefix(endpoint, "unix://"); ok {
		return rest, rest != ""
	}
	if strings.HasPrefix(endpoint, "/") {
		return endpoint, true
	}
	return "", false
}
