package pkgmgr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/blokxz/viewDI/internal/updates"
)

// errFailed is a command that exited with an unexpected code.
var errFailed = errors.New("command failed")

func failed(name string, code int) error {
	return fmt.Errorf("%s exited %d: %w", name, code, errFailed)
}

// first returns the first package of pkgs found in installed.
func first(pkgs []string, installed map[string]string) (string, string, bool) {
	for _, p := range pkgs {
		if v, ok := installed[p]; ok && v != "" {
			return p, v, true
		}
	}
	return "", "", false
}

// --- apt ---

func lookupApt(ctx context.Context, r Runner, pkgs []string) (updates.Package, error) {
	res, err := r.Run(ctx, "apt-cache", append([]string{"policy"}, pkgs...)...)
	if err != nil {
		return updates.Package{}, err
	}
	if res.Code != 0 {
		return updates.Package{}, failed("apt-cache policy", res.Code)
	}
	pol := parseAptPolicy(res.Out)
	installed := map[string]string{}
	for name, p := range pol {
		installed[name] = p[0]
	}
	name, v, ok := first(pkgs, installed)
	if !ok {
		return updates.Package{}, updates.ErrNotManaged
	}
	return updates.Package{
		Name: name, Installed: v, Candidate: pol[name][1],
		Hint: "sudo apt install --only-upgrade " + name,
	}, nil
}

// parseAptPolicy reads `apt-cache policy` output: per package, the
// installed and the candidate version. "(none)" is reported as "".
func parseAptPolicy(out []byte) map[string][2]string {
	pol := map[string][2]string{}
	var cur string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 4096), MaxOutput)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			name, ok := strings.CutSuffix(strings.TrimSpace(line), ":")
			cur = ""
			if ok && validName(name) {
				cur = name
				pol[cur] = [2]string{}
			}
			continue
		}
		if cur == "" {
			continue
		}
		f := fields(line)
		if len(f) != 2 {
			continue
		}
		v := clean(f[1])
		if v == "(none)" {
			v = ""
		}
		p := pol[cur]
		switch f[0] {
		case "Installed:":
			p[0] = v
		case "Candidate:":
			p[1] = v
		}
		pol[cur] = p
	}
	return pol
}

// --- pacman ---

func lookupPacman(ctx context.Context, r Runner, pkgs []string) (updates.Package, error) {
	// pacman -Q exits 1 when one of the names is not installed, but still
	// prints the others.
	res, err := r.Run(ctx, "pacman", append([]string{"-Q"}, pkgs...)...)
	if err != nil {
		return updates.Package{}, err
	}
	name, v, ok := first(pkgs, parseNameVersion(res.Out))
	if !ok {
		if res.Code != 0 && res.Code != 1 {
			return updates.Package{}, failed("pacman -Q", res.Code)
		}
		return updates.Package{}, updates.ErrNotManaged
	}
	p := updates.Package{Name: name, Installed: v, Candidate: v, Hint: "sudo pacman -Syu"}
	res, err = r.Run(ctx, "pacman", "-Si", name)
	if err != nil {
		return updates.Package{}, err
	}
	// A package missing from the sync databases (built locally, from the
	// AUR) has no candidate: it stays as installed.
	if res.Code == 0 {
		if c := parsePacmanInfo(res.Out); c != "" {
			p.Candidate = c
		}
	}
	return p, nil
}

// parseNameVersion reads "name version" lines (pacman -Q, rpm -q with a
// query format); other lines ("package x is not installed") are skipped.
func parseNameVersion(out []byte) map[string]string {
	m := map[string]string{}
	for line := range strings.SplitSeq(string(out), "\n") {
		f := fields(line)
		if len(f) == 2 && validName(f[0]) {
			m[f[0]] = clean(f[1])
		}
	}
	return m
}

// parsePacmanInfo returns the Version field of `pacman -Si` output (the
// first one: the package of the first repository).
func parsePacmanInfo(out []byte) string {
	for line := range strings.SplitSeq(string(out), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == "Version" {
			return clean(v)
		}
	}
	return ""
}

// --- rpm (dnf, zypper) ---

func rpmInstalled(ctx context.Context, r Runner, pkgs []string) (string, string, error) {
	args := append([]string{"-q", "--qf", "%{NAME} %{EPOCHNUM}:%{VERSION}-%{RELEASE}\\n"}, pkgs...)
	res, err := r.Run(ctx, "rpm", args...)
	if err != nil {
		return "", "", err
	}
	// rpm -q exits with the number of names not installed.
	name, v, ok := first(pkgs, parseNameVersion(res.Out))
	if !ok {
		return "", "", updates.ErrNotManaged
	}
	return name, strings.TrimPrefix(v, "0:"), nil
}

// --- dnf ---

func lookupDnf(ctx context.Context, r Runner, pkgs []string) (updates.Package, error) {
	name, v, err := rpmInstalled(ctx, r, pkgs)
	if err != nil {
		return updates.Package{}, err
	}
	p := updates.Package{Name: name, Installed: v, Candidate: v, Hint: "sudo dnf upgrade " + name}
	// dnf check-update exits 100 when updates are available, 0 when not.
	res, err := r.Run(ctx, "dnf", "check-update", "-q", name)
	if err != nil {
		return updates.Package{}, err
	}
	switch res.Code {
	case 0:
	case 100:
		if c := parseDnfCheckUpdate(res.Out, name); c != "" {
			p.Candidate = c
		}
	default:
		return updates.Package{}, failed("dnf check-update", res.Code)
	}
	return p, nil
}

// parseDnfCheckUpdate returns the version offered for name in `dnf
// check-update` output ("docker-ce.x86_64  3:29.1.2-1.fc41  repo"), or "".
func parseDnfCheckUpdate(out []byte, name string) string {
	for line := range strings.SplitSeq(string(out), "\n") {
		f := fields(line)
		if len(f) < 2 {
			continue
		}
		n := f[0]
		if i := strings.LastIndexByte(n, '.'); i > 0 {
			n = n[:i]
		}
		if n == name {
			return clean(f[1])
		}
	}
	return ""
}

// --- zypper ---

func lookupZypper(ctx context.Context, r Runner, pkgs []string) (updates.Package, error) {
	name, v, err := rpmInstalled(ctx, r, pkgs)
	if err != nil {
		return updates.Package{}, err
	}
	p := updates.Package{Name: name, Installed: v, Candidate: v, Hint: "sudo zypper update " + name}
	res, err := r.Run(ctx, "zypper", "--non-interactive", "--xmlout", "list-updates")
	if err != nil {
		return updates.Package{}, err
	}
	if res.Code != 0 {
		return updates.Package{}, failed("zypper list-updates", res.Code)
	}
	if c := parseZypperUpdates(res.Out)[name]; c != "" {
		p.Candidate = c
	}
	return p, nil
}

// parseZypperUpdates reads `zypper --xmlout list-updates`: the edition
// offered per package name.
func parseZypperUpdates(out []byte) map[string]string {
	m := map[string]string{}
	d := xml.NewDecoder(bytes.NewReader(out))
	d.Strict = false
	for {
		tok, err := d.Token()
		if err != nil {
			return m
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "update" {
			continue
		}
		var name, edition, kind string
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "name":
				name = a.Value
			case "edition":
				edition = a.Value
			case "kind":
				kind = a.Value
			}
		}
		if (kind == "" || kind == "package") && validName(name) && edition != "" {
			m[name] = clean(edition)
		}
	}
}

// validName reports whether s looks like a package name.
func validName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	return !slices.ContainsFunc([]byte(s), func(c byte) bool {
		return (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && strings.IndexByte("+-._", c) < 0
	})
}
