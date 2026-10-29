// Package cgroup reads container resource counters straight from the cgroup
// v2 hierarchy (docs/01-research.md §3.2): one pass over a few small files
// per container instead of one stats request each.
//
// Paths are resolved from the container's init PID (/proc/<pid>/cgroup)
// and, when that is not readable, from the known layouts of Docker (systemd
// and cgroupfs drivers, rootless) and Incus (lxc.payload.*). Incus VMs are
// never read here: their cgroup only covers the host-side QEMU process.
package cgroup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

// Default mount points.
const (
	DefaultRoot = "/sys/fs/cgroup"
	DefaultProc = "/proc"
)

// maxFile caps the files read (they are a few hundred bytes in practice).
const maxFile = 64 << 10

// ErrNoCgroup means no cgroup could be found for an instance.
var ErrNoCgroup = errors.New("cgroup not found")

// Reader reads cgroup v2 counters.
type Reader struct {
	Root string // cgroup2 mount, normally /sys/fs/cgroup
	Proc string // procfs mount, normally /proc
	UID  int    // our UID, for the rootless Docker layout
}

// New returns a reader for the system mounts.
func New() *Reader { return &Reader{Root: DefaultRoot, Proc: DefaultProc, UID: os.Getuid()} }

// Available reports whether Root is a cgroup v2 (unified) hierarchy.
func (r *Reader) Available() bool {
	_, err := os.Stat(filepath.Join(r.Root, "cgroup.controllers"))
	return err == nil
}

// Resolve returns the cgroup directory of an instance. It prefers the init
// PID's own cgroup and falls back to the known layouts.
func (r *Reader) Resolve(in engine.Instance) (string, error) {
	if in.Kind == engine.KindVM {
		return "", fmt.Errorf("%s: VM metrics come from the engine API: %w", in.Name, ErrNoCgroup)
	}
	if in.PID > 0 {
		if p, err := r.fromPID(in.PID); err == nil {
			if r.valid(p) {
				return p, nil
			}
		}
	}
	for _, rel := range r.layouts(in) {
		p := filepath.Join(r.Root, rel)
		if r.valid(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s: %w", in.Name, ErrNoCgroup)
}

func (r *Reader) valid(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "cpu.stat"))
	return err == nil
}

// fromPID reads the unified ("0::") entry of /proc/<pid>/cgroup. For Incus
// the container's cgroup is the lxc.payload.* ancestor of the init
// process's (systemd inside the container moves init into init.scope).
func (r *Reader) fromPID(pid int) (string, error) {
	data, err := readCapped(filepath.Join(r.Proc, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		rel, ok := strings.CutPrefix(line, "0::")
		if !ok {
			continue
		}
		rel = filepath.Clean("/" + rel)
		if i := strings.Index(rel, "/lxc.payload."); i >= 0 {
			if j := strings.IndexByte(rel[i+1:], '/'); j >= 0 {
				rel = rel[:i+1+j]
			}
		}
		return filepath.Join(r.Root, rel), nil
	}
	return "", fmt.Errorf("pid %d: no cgroup v2 entry", pid)
}

// layouts lists the well-known cgroup paths of an instance, relative to Root.
func (r *Reader) layouts(in engine.Instance) []string {
	switch in.Engine {
	case engine.Docker:
		uid := strconv.Itoa(r.UID)
		return []string{
			"system.slice/docker-" + in.ID + ".scope", // systemd driver
			"docker/" + in.ID,                         // cgroupfs driver
			"user.slice/user-" + uid + ".slice/user@" + uid + ".service/app.slice/docker.service/docker-" + in.ID + ".scope", // rootless
		}
	case engine.Incus:
		if in.Project == "" || in.Project == "default" {
			return []string{"lxc.payload." + in.Name}
		}
		return []string{"lxc.payload." + in.Project + "_" + in.Name}
	}
	return nil
}

// Read reads the counters of the cgroup at dir. pid (0 = unknown) is used
// for network counters, which live in the container's network namespace
// rather than in the cgroup.
func (r *Reader) Read(dir string, pid int, now time.Time) (engine.Sample, error) {
	s := engine.Sample{Time: now}
	cpu, err := readKeyed(filepath.Join(dir, "cpu.stat"))
	if err != nil {
		return s, err // the cgroup is gone (container stopped) or unreadable
	}
	if v, ok := cpu["usage_usec"]; ok {
		s.CPU = time.Duration(v) * time.Microsecond
		s.Has |= engine.HasCPU
	}
	if q, err := readCapped(filepath.Join(dir, "cpu.max")); err == nil {
		s.CPULimit = parseCPUMax(string(q))
	}
	if cur, err := readUint(filepath.Join(dir, "memory.current")); err == nil {
		s.MemUsed = cur
		if st, err := readKeyed(filepath.Join(dir, "memory.stat")); err == nil && st["inactive_file"] < cur {
			s.MemUsed = cur - st["inactive_file"]
		}
		s.Has |= engine.HasMem
		if lim, err := readUint(filepath.Join(dir, "memory.max")); err == nil {
			s.MemLimit = lim
		}
	}
	if io, err := readCapped(filepath.Join(dir, "io.stat")); err == nil {
		s.BlkRead, s.BlkWrite = parseIOStat(io)
		s.Has |= engine.HasBlk
	}
	if n, err := readUint(filepath.Join(dir, "pids.current")); err == nil {
		s.PIDs = n
		s.Has |= engine.HasPIDs
	}
	if pid > 0 {
		if data, err := readCapped(filepath.Join(r.Proc, strconv.Itoa(pid), "net", "dev")); err == nil {
			s.NetRx, s.NetTx = parseNetDev(data)
			s.Has |= engine.HasNet
		}
	}
	return s, nil
}

func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var buf bytes.Buffer
	_, err = buf.ReadFrom(&limited{f, maxFile})
	return buf.Bytes(), err
}

type limited struct {
	f *os.File
	n int64
}

func (l *limited) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, errors.New("file too large")
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.f.Read(p)
	l.n -= int64(n)
	return n, err
}

func readUint(path string) (uint64, error) {
	data, err := readCapped(path)
	if err != nil {
		return 0, err
	}
	v := strings.TrimSpace(string(data))
	if v == "max" {
		return 0, nil
	}
	return strconv.ParseUint(v, 10, 64)
}

// readKeyed parses "key value" lines (cpu.stat, memory.stat).
func readKeyed(path string) (map[string]uint64, error) {
	data, err := readCapped(path)
	if err != nil {
		return nil, err
	}
	out := map[string]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			continue
		}
		if n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64); err == nil {
			out[k] = n
		}
	}
	return out, nil
}

// parseCPUMax parses "quota period" ("max 100000" = unlimited) into cores.
func parseCPUMax(s string) float64 {
	q, p, ok := strings.Cut(strings.TrimSpace(s), " ")
	if !ok || q == "max" {
		return 0
	}
	quota, err1 := strconv.ParseFloat(q, 64)
	period, err2 := strconv.ParseFloat(p, 64)
	if err1 != nil || err2 != nil || period <= 0 {
		return 0
	}
	return quota / period
}

// parseIOStat sums rbytes/wbytes over every device of io.stat.
func parseIOStat(data []byte) (read, write uint64) {
	for _, line := range strings.Split(string(data), "\n") {
		for _, f := range strings.Fields(line) {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				continue
			}
			switch k {
			case "rbytes":
				read += n
			case "wbytes":
				write += n
			}
		}
	}
	return read, write
}

// parseNetDev sums received and transmitted bytes of every interface but lo.
func parseNetDev(data []byte) (rx, tx uint64) {
	for _, line := range strings.Split(string(data), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "lo" {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, err1 := strconv.ParseUint(f[0], 10, 64)
		t, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		rx += r
		tx += t
	}
	return rx, tx
}
