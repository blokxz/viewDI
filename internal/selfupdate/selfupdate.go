// Package selfupdate implements `viewdi --update`: find the latest GitHub
// release, download the right asset for this machine, check it against the
// release's checksums.txt, and put it in place.
//
// How a copy is updated depends on how it was installed:
//
//   - a standalone binary (the tarball, `go install`, a copy in ~/.local/bin)
//     is replaced in place, atomically, after the new one has been run once;
//   - a .deb or .rpm is not touched: the matching package is downloaded and
//     checked, and the package manager is asked to install it (after a
//     confirmation, with sudo asking for its own password on the terminal);
//   - a package from an AUR helper or pacman is left to that tool, whose
//     command is printed.
//
// The checksums come from the same release as the files, so they protect
// against a corrupt or truncated download, not against a compromised release
// (that needs signatures, which the project does not publish yet).
package selfupdate

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Repo is the project's GitHub repository.
const Repo = "blokxz/viewDI"

const (
	maxAPI      = 4 << 20   // release metadata
	maxChecksum = 1 << 20   // checksums.txt
	maxAsset    = 150 << 20 // a package or tarball
	maxBinary   = 200 << 20 // the unpacked binary
)

// Release is the part of a GitHub release the updater needs.
type Release struct {
	Tag    string // "v1.0.1"
	Assets map[string]string
}

// Version is the tag without the leading v.
func (r Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

// Kind says how the running binary was installed.
type Kind int

// Kinds.
const (
	Standalone Kind = iota
	Deb
	RPM
	Pacman
)

func (k Kind) String() string {
	return [...]string{"a standalone binary", "a .deb package", "an .rpm package", "a pacman/AUR package"}[k]
}

// Options configure Run. The zero value of every field but Current and Exe is
// the production behaviour; tests replace the rest.
type Options struct {
	Current string // the running version, e.g. "v1.0.0" or "dev"
	Exe     string // path of the running binary; "" = os.Executable
	Arch    string // "amd64" or "arm64"; "" = runtime.GOARCH
	Yes     bool   // do not ask for confirmation
	Force   bool   // reinstall the same version, or replace a development build

	API       string // GitHub API base; "" = https://api.github.com
	HTTP      *http.Client
	AllowHost func(host string) bool // download hosts; nil = GitHub's
	Query     func(ctx context.Context, name string, args ...string) (string, error)
	Exec      func(ctx context.Context, name string, args ...string) error // runs the package manager on the terminal
	LookPath  func(string) (string, error)
	Verify    func(ctx context.Context, path, want string) error // runs the new binary; nil = run it with --version
	Geteuid   func() int

	In  io.Reader
	Out io.Writer
}

func (o *Options) defaults() {
	if o.API == "" {
		o.API = "https://api.github.com"
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 2 * time.Minute}
	}
	if o.Arch == "" {
		o.Arch = runtime.GOARCH
	}
	if o.AllowHost == nil {
		o.AllowHost = githubHost
	}
	if o.Query == nil {
		o.Query = query
	}
	if o.Exec == nil {
		o.Exec = runOnTerminal
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.Verify == nil {
		o.Verify = verifyBinary
	}
	if o.Geteuid == nil {
		o.Geteuid = os.Geteuid
	}
	if o.In == nil {
		o.In = strings.NewReader("")
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
}

func githubHost(host string) bool {
	return host == "github.com" || host == "api.github.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

// ErrUpToDate is returned (wrapped) when nothing newer exists.
var ErrUpToDate = errors.New("already up to date")

// Run updates viewDI. It reports what it does on o.Out.
func Run(ctx context.Context, o Options) error {
	o.defaults()
	say := func(format string, a ...any) { _, _ = fmt.Fprintf(o.Out, format+"\n", a...) }

	// A tagged release is vMAJOR.MINOR.PATCH. Anything else (dev, a git
	// describe like v1.0.0-3-gabc-dirty, a goreleaser snapshot) is a build
	// someone made: there is no honest "newer than".
	cur := o.Current
	if !semver.IsValid(cur) || semver.Prerelease(cur) != "" || semver.Build(cur) != "" {
		if !o.Force {
			return fmt.Errorf("this is a development build (%q): there is no version to compare; use --force to install the latest release over it", o.Current)
		}
		cur = "v0.0.0"
	}
	if o.Arch != "amd64" && o.Arch != "arm64" {
		return fmt.Errorf("no release is built for %s", o.Arch)
	}
	exe := o.Exe
	if exe == "" {
		p, err := os.Executable()
		if err != nil {
			return fmt.Errorf("find the running binary: %w", err)
		}
		exe = p
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}

	say("viewDI %s at %s", o.Current, exe)
	rel, err := latest(ctx, &o)
	if err != nil {
		return err
	}
	tag := rel.Tag
	switch c := semver.Compare("v"+rel.Version(), cur); {
	case !semver.IsValid(tag):
		return fmt.Errorf("the latest release has an unusable tag %q", tag)
	case c < 0 && !o.Force:
		return fmt.Errorf("%w: %s is newer than the latest release %s", ErrUpToDate, o.Current, tag)
	case c == 0 && !o.Force:
		return fmt.Errorf("%w (%s)", ErrUpToDate, tag)
	}

	kind, pkg := detect(ctx, &o, exe)
	say("latest release: %s (this copy is %s)", tag, kind)

	sums, err := fetch(ctx, &o, rel, "checksums.txt", maxChecksum)
	if err != nil {
		return err
	}

	switch kind {
	case Pacman:
		say("This copy belongs to the package %q. Update it with the tool that installed it:", pkg)
		say("  yay -Syu %s    (or pamac, paru… for the AUR package; or rebuild packaging/aur/viewdi-bin)", pkg)
		return nil
	case Deb, RPM:
		return updatePackage(ctx, &o, rel, sums, kind)
	}
	return updateBinary(ctx, &o, rel, sums, exe)
}

func assetName(rel Release, arch, ext string) string {
	return fmt.Sprintf("viewdi_%s_linux_%s.%s", rel.Version(), arch, ext)
}

func updateBinary(ctx context.Context, o *Options, rel Release, sums []byte, exe string) error {
	say := func(format string, a ...any) { _, _ = fmt.Fprintf(o.Out, format+"\n", a...) }
	dir := filepath.Dir(exe)
	if err := writable(dir); err != nil {
		return fmt.Errorf("cannot write to %s (%v): run the update as the user who owns it, or with sudo: sudo %s --update", dir, err, exe)
	}
	if !confirm(o, fmt.Sprintf("Replace %s with %s?", exe, rel.Tag)) {
		say("Cancelled.")
		return nil
	}
	name := assetName(rel, o.Arch, "tar.gz")
	say("downloading %s…", name)
	archive, err := fetch(ctx, o, rel, name, maxAsset)
	if err != nil {
		return err
	}
	if err := VerifyChecksum(sums, name, archive); err != nil {
		return err
	}
	bin, err := ExtractBinary(archive)
	if err != nil {
		return err
	}
	if err := Replace(ctx, exe, bin, func(p string) error { return o.Verify(ctx, p, rel.Tag) }); err != nil {
		return err
	}
	say("updated %s to %s. Run viewdi again to use it.", exe, rel.Tag)
	return nil
}

func updatePackage(ctx context.Context, o *Options, rel Release, sums []byte, kind Kind) error {
	say := func(format string, a ...any) { _, _ = fmt.Fprintf(o.Out, format+"\n", a...) }
	ext, tool := "deb", "apt"
	if kind == RPM {
		ext, tool = "rpm", "dnf"
	}
	if _, err := o.LookPath(tool); err != nil && kind == RPM {
		if _, err2 := o.LookPath("zypper"); err2 == nil {
			tool = "zypper"
		}
	}
	name := assetName(rel, o.Arch, ext)
	say("downloading %s…", name)
	pkg, err := fetch(ctx, o, rel, name, maxAsset)
	if err != nil {
		return err
	}
	if err := VerifyChecksum(sums, name, pkg); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "viewdi-update-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pkg, 0o600); err != nil {
		return err
	}
	args := []string{"install", path}
	if tool == "zypper" {
		args = []string{"install", "--allow-unsigned-rpm", path}
	}
	cmd, full := installCommand(o, tool, args)
	say("checked %s against checksums.txt. To install it:", name)
	say("  %s", full)
	if !confirm(o, "Run it now (sudo may ask for your password)?") {
		say("Not run. The verified package is deleted when this exits; download it again to install it later.")
		return nil
	}
	if err := o.Exec(ctx, cmd[0], cmd[1:]...); err != nil {
		return fmt.Errorf("%s failed: %w", full, err)
	}
	say("installed %s.", rel.Tag)
	return nil
}

// installCommand prefixes sudo unless already root.
func installCommand(o *Options, tool string, args []string) ([]string, string) {
	cmd := append([]string{tool}, args...)
	if o.Geteuid() != 0 {
		cmd = append([]string{"sudo"}, cmd...)
	}
	return cmd, strings.Join(cmd, " ")
}

func confirm(o *Options, question string) bool {
	if o.Yes {
		return true
	}
	_, _ = fmt.Fprintf(o.Out, "%s [y/N] ", question)
	line, _ := bufio.NewReader(o.In).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".viewdi-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// latest asks GitHub for the newest published (non-draft, non-pre-release) release.
func latest(ctx context.Context, o *Options) (Release, error) {
	u := o.API + "/repos/" + Repo + "/releases/latest"
	body, err := get(ctx, o, u, maxAPI)
	if err != nil {
		return Release{}, fmt.Errorf("look up the latest release: %w", err)
	}
	var doc struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.Tag == "" {
		return Release{}, errors.New("the release feed is not valid")
	}
	rel := Release{Tag: doc.Tag, Assets: map[string]string{}}
	for _, a := range doc.Assets {
		rel.Assets[a.Name] = a.URL
	}
	return rel, nil
}

func fetch(ctx context.Context, o *Options, rel Release, name string, limit int64) ([]byte, error) {
	u, ok := rel.Assets[name]
	if !ok {
		return nil, fmt.Errorf("release %s has no %s (are the packages attached yet?)", rel.Tag, name)
	}
	b, err := get(ctx, o, u, limit)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", name, err)
	}
	return b, nil
}

// get downloads u (https, an allowed host, size-capped; redirects are checked too).
func get(ctx context.Context, o *Options, u string, limit int64) ([]byte, error) {
	if err := checkURL(o, u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "viewDI-updater")
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream")
	client := *o.HTTP
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("too many redirects")
		}
		return checkURL(o, r.URL.String())
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("more than %d bytes", limit)
	}
	return b, nil
}

func checkURL(o *Options, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" && !(o.API != "https://api.github.com" && u.Scheme == "http") { // http only for a test server
		return fmt.Errorf("refusing %q: not https", u.Redacted())
	}
	if !o.AllowHost(u.Hostname()) {
		return fmt.Errorf("refusing to download from %q", u.Hostname())
	}
	return nil
}

// VerifyChecksum checks data against the line for name in a checksums.txt
// ("<sha256>  <name>").
func VerifyChecksum(sums []byte, name string, data []byte) error {
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == name {
			want = strings.ToLower(f[0])
		}
	}
	if want == "" {
		return fmt.Errorf("checksums.txt has no entry for %s", name)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s: the download is corrupt (got %s…, want %s…)", name, got[:12], want[:12])
	}
	return nil
}

// ExtractBinary returns the viewdi executable at the root of a release tarball.
// It reads nothing else and never writes a path from the archive.
func ExtractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("the archive is not a gzip file: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the archive has no viewdi binary")
		}
		if err != nil {
			return nil, fmt.Errorf("read the archive: %w", err)
		}
		if h.Typeflag != tar.TypeReg || h.Name != "viewdi" {
			continue
		}
		if h.Size <= 0 || h.Size > maxBinary {
			return nil, fmt.Errorf("the binary in the archive has an implausible size (%d)", h.Size)
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxBinary+1))
		if err != nil {
			return nil, err
		}
		if !bytes.HasPrefix(b, []byte("\x7fELF")) {
			return nil, errors.New("the file named viewdi in the archive is not an ELF executable")
		}
		return b, nil
	}
}

// Replace puts bin at path atomically: it is written next to it, checked with
// verify, given the old file's permissions, and renamed over it. If anything
// fails before the rename the old binary is untouched.
func Replace(ctx context.Context, path string, bin []byte, verify func(tmp string) error) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".viewdi-new-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := tmp.Write(bin); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.Chmod(name, mode|0o111); err != nil {
		cleanup()
		return err
	}
	if verify != nil {
		if err := verify(name); err != nil {
			cleanup()
			return fmt.Errorf("the new binary does not run, the old one is kept: %w", err)
		}
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// verifyBinary runs path --version and expects the tag in its output.
func verifyBinary(ctx context.Context, path, want string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return err
	}
	if !strings.Contains(string(out), want) {
		return fmt.Errorf("it reports %q, expected %s", strings.TrimSpace(string(out)), want)
	}
	return nil
}

// detect asks the package managers whether they own exe. The queries are
// read-only and only run for a binary in a system directory.
func detect(ctx context.Context, o *Options, exe string) (Kind, string) {
	if !strings.HasPrefix(exe, "/usr/") && !strings.HasPrefix(exe, "/bin/") && !strings.HasPrefix(exe, "/opt/") {
		return Standalone, ""
	}
	if out, err := o.Query(ctx, "dpkg", "-S", exe); err == nil {
		if pkg, _, ok := strings.Cut(strings.TrimSpace(out), ":"); ok {
			return Deb, pkg
		}
	}
	if out, err := o.Query(ctx, "rpm", "-qf", "--qf", "%{NAME}", exe); err == nil && strings.TrimSpace(out) != "" && !strings.Contains(out, "not owned") {
		return RPM, strings.TrimSpace(out)
	}
	if out, err := o.Query(ctx, "pacman", "-Qoq", exe); err == nil && strings.TrimSpace(out) != "" {
		return Pacman, strings.TrimSpace(out)
	}
	return Standalone, ""
}

func query(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

func runOnTerminal(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
