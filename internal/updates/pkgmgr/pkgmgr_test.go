package pkgmgr

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/updates"
)

const aptPolicy = `docker-ce:
  Installed: 5:29.0.1-1~debian.12~bookworm
  Candidate: 5:29.1.2-1~debian.12~bookworm
  Version table:
     5:29.1.2-1~debian.12~bookworm 500
        500 https://download.docker.com/linux/debian bookworm/stable amd64 Packages
 *** 5:29.0.1-1~debian.12~bookworm 100
        100 /var/lib/dpkg/status
docker.io:
  Installed: (none)
  Candidate: 20.10.24+dfsg1-1+deb12u1
  Version table:
`

const pacmanSi = `Repository      : extra
Name            : docker
Version         : 1:29.1.2-1
Description     : Pack, ship and run any application as a lightweight container
`

const dnfCheck = `
docker-ce.x86_64                3:29.1.2-1.fc41                 docker-ce-stable
docker-ce-cli.x86_64            1:29.1.2-1.fc41                 docker-ce-stable
`

const zypperXML = `<?xml version='1.0'?>
<stream>
<update-status version="0.6">
<update-list>
<update kind="package" name="incus" edition="6.0.6-1.1" arch="x86_64"><summary>Incus</summary></update>
<update kind="patch" name="openSUSE-2026-1" edition="1" />
</update-list>
</update-status>
</stream>`

func TestParseAptPolicy(t *testing.T) {
	got := parseAptPolicy([]byte(aptPolicy))
	if got["docker-ce"] != [2]string{"5:29.0.1-1~debian.12~bookworm", "5:29.1.2-1~debian.12~bookworm"} {
		t.Fatalf("docker-ce = %q", got["docker-ce"])
	}
	if got["docker.io"][0] != "" || got["docker.io"][1] == "" {
		t.Fatalf("docker.io = %q", got["docker.io"])
	}
}

func TestParsers(t *testing.T) {
	if v := parsePacmanInfo([]byte(pacmanSi)); v != "1:29.1.2-1" {
		t.Errorf("pacman -Si = %q", v)
	}
	if v := parseDnfCheckUpdate([]byte(dnfCheck), "docker-ce"); v != "3:29.1.2-1.fc41" {
		t.Errorf("dnf = %q", v)
	}
	if v := parseDnfCheckUpdate([]byte(dnfCheck), "docker"); v != "" {
		t.Errorf("dnf docker = %q", v)
	}
	if m := parseZypperUpdates([]byte(zypperXML)); m["incus"] != "6.0.6-1.1" || len(m) != 1 {
		t.Errorf("zypper = %v", m)
	}
	m := parseNameVersion([]byte("docker 1:29.0.1-1\nerror: package 'docker-ce' was not found\npackage moby-engine is not installed\n"))
	if m["docker"] != "1:29.0.1-1" || len(m) != 1 {
		t.Errorf("name version = %v", m)
	}
}

// script is a Runner answering from a table keyed by the command line.
type script struct {
	out   map[string]Result
	calls []string
}

func (s *script) Run(_ context.Context, name string, args ...string) (Result, error) {
	cmd := strings.Join(append([]string{name}, args...), " ")
	s.calls = append(s.calls, cmd)
	for k, r := range s.out {
		if strings.HasPrefix(cmd, k) {
			return r, nil
		}
	}
	return Result{}, &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func only(bins ...string) func(string) (string, error) {
	return func(b string) (string, error) {
		for _, x := range bins {
			if x == b {
				return "/usr/bin/" + b, nil
			}
		}
		return "", exec.ErrNotFound
	}
}

func TestLookup(t *testing.T) {
	rpm := "rpm -q --qf"
	cases := []struct {
		name string
		bin  string
		out  map[string]Result
		id   engine.ID
		want updates.Package
		err  error
	}{
		{
			name: "apt", bin: "apt-cache", id: engine.Docker,
			out:  map[string]Result{"apt-cache policy": {Out: []byte(aptPolicy)}},
			want: updates.Package{Manager: "apt", Name: "docker-ce", Installed: "5:29.0.1-1~debian.12~bookworm", Candidate: "5:29.1.2-1~debian.12~bookworm", Hint: "sudo apt install --only-upgrade docker-ce"},
		},
		{
			name: "apt not installed", bin: "apt-cache", id: engine.Incus,
			out: map[string]Result{"apt-cache policy": {Out: []byte("incus:\n  Installed: (none)\n  Candidate: 6.0.4-1\n")}},
			err: updates.ErrNotManaged,
		},
		{
			name: "pacman", bin: "pacman", id: engine.Docker,
			out: map[string]Result{
				"pacman -Q":  {Out: []byte("docker 1:29.0.1-1\n"), Code: 1},
				"pacman -Si": {Out: []byte(pacmanSi)},
			},
			want: updates.Package{Manager: "pacman", Name: "docker", Installed: "1:29.0.1-1", Candidate: "1:29.1.2-1", Hint: "sudo pacman -Syu"},
		},
		{
			name: "pacman local package", bin: "pacman", id: engine.Incus,
			out: map[string]Result{
				"pacman -Q":  {Out: []byte("incus 6.18-1\n")},
				"pacman -Si": {Code: 1},
			},
			want: updates.Package{Manager: "pacman", Name: "incus", Installed: "6.18-1", Candidate: "6.18-1", Hint: "sudo pacman -Syu"},
		},
		{
			name: "dnf update", bin: "dnf", id: engine.Docker,
			out: map[string]Result{
				rpm:                {Out: []byte("package docker-ce is not installed\nmoby-engine 0:28.1.0-1.fc41\n"), Code: 3},
				"dnf check-update": {Out: []byte("moby-engine.x86_64 29.1.2-1.fc41 updates\n"), Code: 100},
			},
			want: updates.Package{Manager: "dnf", Name: "moby-engine", Installed: "28.1.0-1.fc41", Candidate: "29.1.2-1.fc41", Hint: "sudo dnf upgrade moby-engine"},
		},
		{
			name: "dnf none", bin: "dnf", id: engine.Incus,
			out: map[string]Result{
				rpm:                {Out: []byte("incus 0:6.0.5-1.fc41\n")},
				"dnf check-update": {},
			},
			want: updates.Package{Manager: "dnf", Name: "incus", Installed: "6.0.5-1.fc41", Candidate: "6.0.5-1.fc41", Hint: "sudo dnf upgrade incus"},
		},
		{
			name: "dnf failure", bin: "dnf", id: engine.Incus,
			out: map[string]Result{
				rpm:                {Out: []byte("incus 0:6.0.5-1.fc41\n")},
				"dnf check-update": {Code: 1},
			},
			err: errFailed,
		},
		{
			name: "zypper", bin: "zypper", id: engine.Incus,
			out: map[string]Result{
				rpm:      {Out: []byte("incus 0:6.0.5-1.1\n")},
				"zypper": {Out: []byte(zypperXML)},
			},
			want: updates.Package{Manager: "zypper", Name: "incus", Installed: "6.0.5-1.1", Candidate: "6.0.6-1.1", Hint: "sudo zypper update incus"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &script{out: c.out}
			s := Detect(only(c.bin, "rpm"), r)
			if s == nil {
				t.Fatal("no manager detected")
			}
			got, err := s.Lookup(context.Background(), c.id)
			if c.err != nil {
				if !errors.Is(err, c.err) {
					t.Fatalf("err = %v, want %v", err, c.err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %+v, %v\nwant %+v", got, err, c.want)
			}
			for _, call := range r.calls {
				if strings.Contains(call, "sudo") || strings.Contains(call, " -S ") || strings.Contains(call, "-Sy") || strings.Contains(call, "refresh") || strings.Contains(call, "update ") && !strings.Contains(call, "check-update") {
					t.Errorf("command %q is not read-only", call)
				}
			}
		})
	}
}

func TestDetectNone(t *testing.T) {
	if s := Detect(only(), &script{}); s != nil {
		t.Fatalf("detected %s", s.Manager())
	}
}

func TestCleanStripsEscapes(t *testing.T) {
	pol := parseAptPolicy([]byte("incus:\n  Installed: 6.0.5\x1b]52;c;aGk=\x07\n  Candidate: 6.0.6\x1b[31m\n"))
	if p := pol["incus"]; strings.ContainsRune(p[0]+p[1], 0x1b) {
		t.Fatalf("escapes kept: %q", p)
	}
}

func TestCapped(t *testing.T) {
	c := &capped{max: 4}
	if n, _ := c.Write([]byte("abcdef")); n != 6 || c.String() != "abcd" {
		t.Fatalf("n=%d %q", n, c.String())
	}
}

func FuzzParseAptPolicy(f *testing.F) {
	f.Add([]byte(aptPolicy))
	f.Fuzz(func(_ *testing.T, b []byte) { parseAptPolicy(b) })
}

func FuzzParsePacman(f *testing.F) {
	f.Add([]byte(pacmanSi))
	f.Add([]byte("docker 1:29.0.1-1\n"))
	f.Fuzz(func(_ *testing.T, b []byte) {
		parsePacmanInfo(b)
		parseNameVersion(b)
	})
}

func FuzzParseDnf(f *testing.F) {
	f.Add([]byte(dnfCheck), "docker-ce")
	f.Fuzz(func(_ *testing.T, b []byte, name string) { parseDnfCheckUpdate(b, name) })
}

func FuzzParseZypper(f *testing.F) {
	f.Add([]byte(zypperXML))
	f.Fuzz(func(_ *testing.T, b []byte) { parseZypperUpdates(b) })
}
