package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarball(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(data)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

var elf = []byte("\x7fELF fake viewdi binary")

// server serves a GitHub-like release feed with the given assets.
type server struct {
	*httptest.Server
	assets map[string][]byte
	tag    string
}

func newServer(t *testing.T, tag string, assets map[string][]byte) *server {
	t.Helper()
	s := &server{assets: assets, tag: tag}
	mux := http.NewServeMux()
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	mux.HandleFunc("/repos/"+Repo+"/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		var as []string
		for name := range s.assets {
			as = append(as, fmt.Sprintf(`{"name":%q,"browser_download_url":%q}`, name, s.URL+"/dl/"+name))
		}
		_, _ = fmt.Fprintf(w, `{"tag_name":%q,"assets":[%s]}`, s.tag, strings.Join(as, ","))
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		b, ok := s.assets[strings.TrimPrefix(r.URL.Path, "/dl/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	})
	return s
}

func release(t *testing.T, tag string, binary []byte) *server {
	t.Helper()
	ver := strings.TrimPrefix(tag, "v")
	tgz := tarball(t, map[string][]byte{"viewdi": binary, "LICENSE": []byte("x")})
	deb := []byte("deb bytes")
	sums := fmt.Sprintf("%s  viewdi_%s_linux_amd64.tar.gz\n%s  viewdi_%s_linux_amd64.deb\n", sha(tgz), ver, sha(deb), ver)
	return newServer(t, tag, map[string][]byte{
		"viewdi_" + ver + "_linux_amd64.tar.gz": tgz,
		"viewdi_" + ver + "_linux_amd64.deb":    deb,
		"checksums.txt":                         []byte(sums),
	})
}

func opts(srv *server, exe, current string, out *bytes.Buffer) Options {
	return Options{
		Current: current, Exe: exe, Arch: "amd64", Yes: true, API: srv.URL, HTTP: srv.Client(), Out: out,
		AllowHost: func(string) bool { return true },
		Query:     func(context.Context, string, ...string) (string, error) { return "", errors.New("not owned") },
		Verify:    func(context.Context, string, string) error { return nil },
		Geteuid:   func() int { return 1000 },
	}
}

func installedBinary(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "viewdi")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestUpdatesAStandaloneBinary(t *testing.T) {
	srv := release(t, "v1.2.0", elf)
	exe := installedBinary(t)
	var out bytes.Buffer
	if err := Run(context.Background(), opts(srv, exe, "v1.0.0", &out)); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	got, _ := os.ReadFile(exe)
	if !bytes.Equal(got, elf) {
		t.Fatalf("binary not replaced: %q", got)
	}
	if fi, _ := os.Stat(exe); fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("not executable: %v", fi.Mode())
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
	if !strings.Contains(out.String(), "updated") || !strings.Contains(out.String(), "v1.2.0") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestNothingToDoWhenCurrentOrNewer(t *testing.T) {
	srv := release(t, "v1.0.0", elf)
	for _, cur := range []string{"v1.0.0", "v1.3.0"} {
		var out bytes.Buffer
		err := Run(context.Background(), opts(srv, installedBinary(t), cur, &out))
		if !errors.Is(err, ErrUpToDate) {
			t.Errorf("current %s: err = %v", cur, err)
		}
	}
}

func TestDevelopmentBuildsNeedForce(t *testing.T) {
	srv := release(t, "v1.0.0", elf)
	for _, cur := range []string{"dev", "v1.0.0-3-gabc123-dirty", "v0.7.0-SNAPSHOT-abc"} {
		var out bytes.Buffer
		err := Run(context.Background(), opts(srv, installedBinary(t), cur, &out))
		if err == nil || !strings.Contains(err.Error(), "development build") {
			t.Errorf("%s: err = %v", cur, err)
		}
	}
	exe := installedBinary(t)
	o := opts(srv, exe, "dev", &bytes.Buffer{})
	o.Force = true
	if err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); !bytes.Equal(got, elf) {
		t.Fatal("--force did not install the release")
	}
}

func TestCorruptDownloadKeepsTheOldBinary(t *testing.T) {
	srv := release(t, "v1.2.0", elf)
	srv.assets["viewdi_1.2.0_linux_amd64.tar.gz"] = append(srv.assets["viewdi_1.2.0_linux_amd64.tar.gz"], 'x') // no longer matches checksums.txt
	exe := installedBinary(t)
	err := Run(context.Background(), opts(srv, exe, "v1.0.0", &bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Fatalf("the binary changed: %q", got)
	}
}

func TestABinaryThatDoesNotRunIsNotInstalled(t *testing.T) {
	srv := release(t, "v1.2.0", elf)
	exe := installedBinary(t)
	o := opts(srv, exe, "v1.0.0", &bytes.Buffer{})
	o.Verify = func(context.Context, string, string) error { return errors.New("exec format error") }
	err := Run(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "old one is kept") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Fatalf("the binary changed: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestAsksBeforeReplacing(t *testing.T) {
	srv := release(t, "v1.2.0", elf)
	exe := installedBinary(t)
	for answer, replaced := range map[string]bool{"n\n": false, "\n": false, "y\n": true, "YES\n": true} {
		if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		o := opts(srv, exe, "v1.0.0", &bytes.Buffer{})
		o.Yes, o.In = false, strings.NewReader(answer)
		if err := Run(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(exe)
		if bytes.Equal(got, elf) != replaced {
			t.Errorf("answer %q: replaced=%v, want %v", answer, !replaced, replaced)
		}
	}
}

func TestPackagedInstallsGoThroughThePackageManager(t *testing.T) {
	srv := release(t, "v1.2.0", elf)
	exe := "/usr/bin/viewdi"
	var ran []string
	o := opts(srv, exe, "v1.0.0", &bytes.Buffer{})
	o.Exe = exe
	o.Query = func(_ context.Context, name string, args ...string) (string, error) {
		if name == "dpkg" {
			return "viewdi: /usr/bin/viewdi\n", nil
		}
		return "", errors.New("no")
	}
	o.LookPath = func(s string) (string, error) { return "/usr/bin/" + s, nil }
	o.Exec = func(_ context.Context, name string, args ...string) error {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil
	}
	var out bytes.Buffer
	o.Out = &out
	if err := Run(context.Background(), o); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(ran) != 1 || !strings.HasPrefix(ran[0], "sudo apt install ") || !strings.HasSuffix(ran[0], "viewdi_1.2.0_linux_amd64.deb") {
		t.Fatalf("ran %q", ran)
	}
	// The binary on disk is never written by the updater itself.
	if _, err := os.Stat("/usr/bin/.viewdi-new"); err == nil {
		t.Fatal("touched the packaged binary")
	}

	// As root there is no sudo; declining runs nothing.
	ran = nil
	o.Geteuid = func() int { return 0 }
	if err := Run(context.Background(), o); err != nil || !strings.HasPrefix(ran[0], "apt install ") {
		t.Fatalf("root: err=%v ran=%q", err, ran)
	}
	ran = nil
	o.Yes, o.In = false, strings.NewReader("n\n")
	if err := Run(context.Background(), o); err != nil || len(ran) != 0 {
		t.Fatalf("declined: err=%v ran=%q", err, ran)
	}
}

func TestPacmanPackagesAreLeftToTheirTool(t *testing.T) {
	srv := release(t, "v1.2.0", elf)
	o := opts(srv, "/usr/bin/viewdi", "v1.0.0", &bytes.Buffer{})
	o.Query = func(_ context.Context, name string, _ ...string) (string, error) {
		if name == "pacman" {
			return "viewdi-bin\n", nil
		}
		return "", errors.New("no")
	}
	var out bytes.Buffer
	o.Out = &out
	o.Exec = func(context.Context, string, ...string) error {
		t.Fatal("ran a command for a pacman package")
		return nil
	}
	if err := Run(context.Background(), o); err != nil || !strings.Contains(out.String(), "yay -Syu viewdi-bin") {
		t.Fatalf("err=%v\n%s", err, out.String())
	}
}

func TestMissingAssetsAreExplained(t *testing.T) {
	srv := newServer(t, "v1.2.0", map[string][]byte{"checksums.txt": []byte("")})
	err := Run(context.Background(), opts(srv, installedBinary(t), "v1.0.0", &bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "no viewdi_1.2.0_linux_amd64.tar.gz") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadsStayOnGitHub(t *testing.T) {
	o := Options{API: "https://api.github.com"}
	o.defaults()
	for _, bad := range []string{"http://github.com/x", "https://evil.example/x", "https://github.com.evil.example/x", "file:///etc/passwd", "https://raw.githubusercontent.com.evil.io/x"} {
		if err := checkURL(&o, bad); err == nil {
			t.Errorf("%s was allowed", bad)
		}
	}
	for _, good := range []string{"https://github.com/blokxz/viewDI/releases/download/v1/x", "https://objects.githubusercontent.com/x", "https://api.github.com/repos/x"} {
		if err := checkURL(&o, good); err != nil {
			t.Errorf("%s refused: %v", good, err)
		}
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("hello")
	sums := []byte(sha(data) + "  a.tar.gz\n" + strings.Repeat("0", 64) + "  b.tar.gz\n")
	if err := VerifyChecksum(sums, "a.tar.gz", data); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChecksum(sums, "b.tar.gz", data); err == nil {
		t.Fatal("wrong checksum accepted")
	}
	if err := VerifyChecksum(sums, "c.tar.gz", data); err == nil {
		t.Fatal("missing entry accepted")
	}
}

func TestExtractBinaryIsStrict(t *testing.T) {
	if b, err := ExtractBinary(tarball(t, map[string][]byte{"viewdi": elf})); err != nil || !bytes.Equal(b, elf) {
		t.Fatalf("good archive: %v", err)
	}
	for name, files := range map[string]map[string][]byte{
		"no binary":       {"README.md": []byte("x")},
		"nested path":     {"dir/viewdi": elf},
		"traversal":       {"../viewdi": elf},
		"not an ELF file": {"viewdi": []byte("#!/bin/sh\nrm -rf ~")},
	} {
		if _, err := ExtractBinary(tarball(t, files)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ExtractBinary([]byte("not gzip")); err == nil {
		t.Error("garbage accepted")
	}
}

func FuzzExtractBinary(f *testing.F) {
	f.Add([]byte("not gzip"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = ExtractBinary(b) })
}
