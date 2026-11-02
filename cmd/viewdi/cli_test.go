package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func testSet() *pflag.FlagSet {
	var f flags
	return newFlagSet(&f, &bytes.Buffer{})
}

// The man page in the repository is generated: `go generate ./cmd/viewdi`
// rewrites it after a flag or the documentation in cli.go changes.
func TestManPageIsCurrent(t *testing.T) {
	want := manPage(testSet())
	got, err := os.ReadFile(filepath.Join("..", "..", "docs", "man", "viewdi.1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatal("docs/man/viewdi.1 is stale: run `go generate ./cmd/viewdi`")
	}
}

func TestManPageCoversEveryFlagAndIsPlainASCII(t *testing.T) {
	page := manPage(testSet())
	for _, s := range flagSpecs(testSet()) {
		if !strings.Contains(page, strings.ReplaceAll(s.Name, "-", `\-`)) {
			t.Errorf("man page lacks --%s", s.Name)
		}
	}
	for i, r := range page {
		if r > 0x7e {
			t.Fatalf("non-ASCII rune %q at byte %d: use a roff escape", r, i)
		}
	}
	for _, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(line, ".") && !strings.HasPrefix(line, `.\"`) {
			req := strings.Fields(line)[0]
			switch req {
			case ".TH", ".SH", ".TP", ".B", ".I", ".BR", ".br", ".PP":
			default:
				t.Errorf("unexpected roff request %q", line)
			}
		}
	}
}

func TestHelpHasExamplesFilesAndEnvironment(t *testing.T) {
	var out strings.Builder
	if _, err := parseFlags([]string{"--help"}, &out); err == nil {
		t.Fatal("--help should return errHelp")
	}
	for _, want := range []string{"Examples:", "viewdi --read-only", "Files", "~/.config/viewDI/config.toml", "Environment:", "DOCKER_HOST", "NO_COLOR", "viewdi completion"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("--help lacks %q", want)
		}
	}
}

func TestCompletionsMentionEveryFlag(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script, err := completion(shell, testSet())
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range flagSpecs(testSet()) {
			if !strings.Contains(script, s.Name) {
				t.Errorf("%s completion lacks --%s", shell, s.Name)
			}
		}
		for _, id := range append(themeIDs(), mockScenarios...) {
			if !strings.Contains(script, id) {
				t.Errorf("%s completion lacks the value %q", shell, id)
			}
		}
	}
	if _, err := completion("powershell", testSet()); err == nil {
		t.Error("an unknown shell was accepted")
	}
}

// Each script must at least parse in its own shell, when that shell exists.
func TestCompletionsParse(t *testing.T) {
	for shell, args := range map[string][]string{"bash": {"-n"}, "zsh": {"-n"}, "fish": {"--no-execute"}} {
		bin, err := exec.LookPath(shell)
		if err != nil {
			t.Logf("%s not installed, skipping", shell)
			continue
		}
		script, _ := completion(shell, testSet())
		file := filepath.Join(t.TempDir(), "script")
		if err := os.WriteFile(file, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(bin, append(args, file)...).CombinedOutput(); err != nil {
			t.Errorf("%s rejects its completion script: %v\n%s", shell, err, out)
		}
	}
}

func TestSubcommands(t *testing.T) {
	tests := []struct {
		args []string
		code int
		out  string
		err  string
	}{
		{[]string{"man"}, 0, ".TH VIEWDI 1", ""},
		{[]string{"man", "extra"}, 2, "", "no arguments"},
		{[]string{"completion", "bash"}, 0, "complete -F _viewdi viewdi", ""},
		{[]string{"completion"}, 2, "", "usage"},
		{[]string{"completion", "tcsh"}, 2, "", "unknown shell"},
	}
	for _, tt := range tests {
		var out, errb bytes.Buffer
		handled, code := runSubcommand(tt.args, &out, &errb)
		if !handled || code != tt.code || !strings.Contains(out.String(), tt.out) || !strings.Contains(errb.String(), tt.err) {
			t.Errorf("%v: handled=%v code=%d out=%.40q err=%q", tt.args, handled, code, out.String(), errb.String())
		}
	}
	if handled, _ := runSubcommand([]string{"--mock"}, &bytes.Buffer{}, &bytes.Buffer{}); handled {
		t.Error("a flag was taken for a subcommand")
	}
}
