package main

import (
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/pflag"

	"github.com/blokxz/viewDI/themes"
)

// The command-line documentation lives here once; --help, the man page
// (docs/man/viewdi.1) and the completions are all rendered from it and from
// the flag set.

var cliExamples = []struct{ cmd, desc string }{
	{"viewdi", "manage the real Docker and Incus daemons"},
	{"viewdi --read-only", "look but do not touch: every mutating action is disabled"},
	{"viewdi --mock", "try it without any daemon, on made-up containers"},
	{"viewdi --mock=chaos", "watch the UI survive failing daemons"},
	{"viewdi --docker-host ssh://me@server", "a remote Docker daemon over SSH"},
	{"viewdi --theme nord --refresh 2s", "pick a theme and a slower metrics refresh"},
	{"viewdi doctor", "check sockets, permissions and optional tools, and say how to fix what is missing"},
	{"viewdi --debug", "also write the debug level to ~/.local/state/viewDI/viewdi.log (warnings and errors are always written there)"},
	{"viewdi completion fish > ~/.config/fish/completions/viewdi.fish", "install the completions"},
}

var cliFiles = []struct{ path, desc string }{
	{"~/.config/viewDI/config.toml", "settings, edited by hand and never rewritten by viewDI"},
	{"~/.config/viewDI/themes/*.toml", "your own themes"},
	{"~/.config/viewDI/keys.toml", "key overrides"},
	{"~/.local/state/viewDI/state.json", "last view, theme, sort, folded tree nodes"},
	{"~/.local/state/viewDI/viewdi.log", "warnings, errors and failed actions; --debug adds the debug level"},
	{"~/.local/state/viewDI/crash-*.log", "crash reports"},
	{"~/.local/share/viewDI/labs.json", "your labs"},
	{"~/.cache/viewDI/updates.json", "update check cache"},
}

var cliEnv = []struct{ name, desc string }{
	{"DOCKER_HOST", "Docker daemon URI (default: the rootless socket, then /var/run/docker.sock)"},
	{"DOCKER_TLS_VERIFY", "set when a tcp:// Docker host uses TLS; without it viewDI warns"},
	{"INCUS_SOCKET", "path of the Incus unix socket"},
	{"INCUS_DIR", "directory holding unix.socket (default /var/lib/incus, then /run/incus)"},
	{"GITHUB_TOKEN", "raises the GitHub API rate limit of the engine update checks"},
	{"NO_COLOR", "use the colourless monochrome theme and no terminal colour queries"},
	{"XDG_CONFIG_HOME, XDG_STATE_HOME, XDG_DATA_HOME, XDG_CACHE_HOME", "move the files above"},
}

const cliSummary = "viewDI manages and monitors Docker and Incus side by side from one terminal UI: lifecycle, one card per app on a Home screen, a machines list and tree, an activity feed, logs, metrics, image and engine updates."

// usageText is the --help output.
func usageText(set *pflag.FlagSet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "viewDI — manage and monitor Docker and Incus from one TUI\n\n%s\n\nUsage:\n  viewdi [flags]\n  viewdi doctor\n  viewdi completion bash|zsh|fish\n  viewdi man\n\nFlags:\n%s", cliSummary, set.FlagUsages())
	b.WriteString("\nExamples:\n")
	for _, e := range cliExamples {
		fmt.Fprintf(&b, "  %-66s %s\n", e.cmd, e.desc)
	}
	b.WriteString("\nFiles (XDG base directories are honoured):\n")
	for _, f := range cliFiles {
		fmt.Fprintf(&b, "  %-36s %s\n", f.path, f.desc)
	}
	b.WriteString("\nEnvironment:\n")
	for _, e := range cliEnv {
		fmt.Fprintf(&b, "  %s\n      %s\n", e.name, e.desc)
	}
	b.WriteString("\nPress ? inside viewDI for the key map.\n")
	return b.String()
}

// flagSpec is what the man page and the completions need to know of a flag.
type flagSpec struct {
	Name, Usage string
	Value       bool     // takes a value
	Optional    bool     // the value may be omitted (--mock, --trace)
	Choices     []string // fixed values, when known
}

func flagSpecs(set *pflag.FlagSet) []flagSpec {
	var out []flagSpec
	set.VisitAll(func(f *pflag.Flag) {
		s := flagSpec{Name: f.Name, Usage: f.Usage, Value: f.Value.Type() != "bool", Optional: f.NoOptDefVal != "" && f.Value.Type() != "bool"}
		switch f.Name {
		case "mock":
			s.Choices = mockScenarios
		case "theme":
			s.Choices = themeIDs()
		}
		out = append(out, s)
	})
	return out
}

// themeIDs lists the built-in theme IDs.
func themeIDs() []string {
	files, _ := fs.Glob(themes.FS, "*.toml")
	ids := make([]string, 0, len(files))
	for _, f := range files {
		ids = append(ids, strings.TrimSuffix(filepath.Base(f), ".toml"))
	}
	slices.Sort(ids)
	return ids
}

// roffEscape makes text safe for a roff request or paragraph.
func roffEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\e`)
	s = strings.ReplaceAll(s, "-", `\-`)
	s = strings.NewReplacer("–", `\(en`, "—", `\(em`).Replace(s)
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "'") {
		s = `\&` + s
	}
	return s
}

// manPage renders viewdi(1). It carries no date or version, so it only changes
// when the flags or the documentation above change; a test compares it with
// docs/man/viewdi.1.
func manPage(set *pflag.FlagSet) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w(".\\\" Generated by `viewdi man` (go generate ./cmd/viewdi). Do not edit.\n")
	w(".TH VIEWDI 1 \"\" \"viewDI\" \"User Commands\"\n")
	w(".SH NAME\nviewdi \\- manage and monitor Docker and Incus from one terminal UI\n")
	w(".SH SYNOPSIS\n.B viewdi\n[\\fIoptions\\fR]\n.br\n.B viewdi doctor\n[\\fIoptions\\fR]\n.br\n.B viewdi completion\n\\fBbash\\fR|\\fBzsh\\fR|\\fBfish\\fR\n.br\n.B viewdi man\n")
	w(".SH DESCRIPTION\n%s\n.PP\nviewDI only reads from the update sources and never installs anything. It talks to the daemons through their sockets, so it needs the same group membership as the docker and incus command line tools (\\fBdocker\\fR, and \\fBincus\\-admin\\fR or \\fBincus\\fR). Press \\fB?\\fR inside viewDI for the key map.\n", roffEscape(cliSummary))
	w(".SH OPTIONS\n")
	for _, s := range flagSpecs(set) {
		head := `\fB\-\-` + strings.ReplaceAll(s.Name, "-", `\-`) + `\fR`
		switch {
		case s.Optional:
			head += `[=\fIvalue\fR]`
		case s.Value:
			head += ` \fIvalue\fR`
		}
		w(".TP\n%s\n%s\n", head, roffEscape(s.Usage))
	}
	w(".SH COMMANDS\n.TP\n\\fBdoctor\\fR\nCheck the Docker and Incus sockets, whether this session may use them, the terminal and the optional tools, and print how to fix each problem. Exits 1 when neither engine can be reached. It accepts \\fB\\-\\-docker\\-host\\fR and \\fB\\-\\-incus\\-socket\\fR.\n.TP\n\\fBcompletion\\fR \\fIshell\\fR\nPrint the completion script for bash, zsh or fish.\n.TP\n\\fBman\\fR\nPrint this manual page.\n")
	w(".SH EXAMPLES\n")
	for _, e := range cliExamples {
		w(".TP\n.B %s\n%s\n", roffEscape(e.cmd), roffEscape(e.desc))
	}
	w(".SH FILES\n")
	for _, f := range cliFiles {
		w(".TP\n.I %s\n%s\n", roffEscape(f.path), roffEscape(f.desc))
	}
	w(".SH ENVIRONMENT\n")
	for _, e := range cliEnv {
		w(".TP\n.B %s\n%s\n", roffEscape(e.name), roffEscape(e.desc))
	}
	w(".SH EXIT STATUS\n.TP\n.B 0\nNormal exit.\n.TP\n.B 1\nAn error, or a crash (the path of the crash report is printed).\n.TP\n.B 2\nInvalid command line.\n.TP\n.B 130\nInterrupted.\n")
	w(".SH SEE ALSO\n.BR docker (1),\n.BR incus (1)\n")
	return b.String()
}

func choices(s flagSpec) string { return strings.Join(s.Choices, " ") }

// completion renders the completion script for shell ("bash", "zsh", "fish").
func completion(shell string, set *pflag.FlagSet) (string, error) {
	specs := flagSpecs(set)
	switch shell {
	case "bash":
		return bashCompletion(specs), nil
	case "zsh":
		return zshCompletion(specs), nil
	case "fish":
		return fishCompletion(specs), nil
	}
	return "", fmt.Errorf("unknown shell %q (want bash, zsh or fish)", shell)
}

func bashCompletion(specs []flagSpec) string {
	var b strings.Builder
	b.WriteString("# bash completion for viewdi (generated by `viewdi completion bash`)\n_viewdi() {\n  local cur prev\n  cur=\"${COMP_WORDS[COMP_CWORD]}\"\n  prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n")
	b.WriteString("  if [ \"$COMP_CWORD\" -eq 1 ] && [[ \"$cur\" != -* ]]; then\n    COMPREPLY=($(compgen -W \"doctor completion man\" -- \"$cur\"))\n    return\n  fi\n")
	b.WriteString("  if [ \"${COMP_WORDS[1]}\" = completion ]; then\n    COMPREPLY=($(compgen -W \"bash zsh fish\" -- \"$cur\"))\n    return\n  fi\n")
	b.WriteString("  case \"$prev\" in\n")
	for _, s := range specs {
		if s.Value && !s.Optional && len(s.Choices) > 0 {
			fmt.Fprintf(&b, "    --%s) COMPREPLY=($(compgen -W \"%s\" -- \"$cur\")); return;;\n", s.Name, choices(s))
		}
	}
	b.WriteString("  esac\n  case \"$cur\" in\n")
	for _, s := range specs {
		if s.Optional && len(s.Choices) > 0 {
			fmt.Fprintf(&b, "    --%s=*) COMPREPLY=($(compgen -W \"%s\" -- \"${cur#--%s=}\")); return;;\n", s.Name, choices(s), s.Name)
		}
	}
	var names []string
	for _, s := range specs {
		names = append(names, "--"+s.Name)
	}
	fmt.Fprintf(&b, "  esac\n  COMPREPLY=($(compgen -W \"%s\" -- \"$cur\"))\n}\ncomplete -F _viewdi viewdi\n", strings.Join(names, " "))
	return b.String()
}

func zshQuote(s string) string {
	s = strings.NewReplacer("'", `'\''`, "[", `\[`, "]", `\]`, ":", `\:`).Replace(s)
	return s
}

func zshCompletion(specs []flagSpec) string {
	var b strings.Builder
	b.WriteString("#compdef viewdi\n# zsh completion for viewdi (generated by `viewdi completion zsh`)\n_viewdi() {\n  local -a args\n  args=(\n")
	for _, s := range specs {
		switch {
		case s.Optional && len(s.Choices) > 0:
			fmt.Fprintf(&b, "    '--%s=-[%s]::scenario:(%s)'\n", s.Name, zshQuote(s.Usage), choices(s))
		case s.Optional:
			fmt.Fprintf(&b, "    '--%s=-[%s]::file:_files'\n", s.Name, zshQuote(s.Usage))
		case s.Value && len(s.Choices) > 0:
			fmt.Fprintf(&b, "    '--%s[%s]:%s:(%s)'\n", s.Name, zshQuote(s.Usage), s.Name, choices(s))
		case s.Value:
			fmt.Fprintf(&b, "    '--%s[%s]:%s: '\n", s.Name, zshQuote(s.Usage), s.Name)
		default:
			fmt.Fprintf(&b, "    '--%s[%s]'\n", s.Name, zshQuote(s.Usage))
		}
	}
	b.WriteString("    '1:command:(doctor completion man)'\n    '2:shell:(bash zsh fish)'\n  )\n  _arguments -s $args\n}\n_viewdi \"$@\"\n")
	return b.String()
}

func fishQuote(s string) string { return strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(s) }

func fishCompletion(specs []flagSpec) string {
	var b strings.Builder
	b.WriteString("# fish completion for viewdi (generated by `viewdi completion fish`)\ncomplete -c viewdi -f\n")
	b.WriteString("complete -c viewdi -n '__fish_use_subcommand' -a 'doctor' -d 'check sockets, permissions and tools'\n")
	b.WriteString("complete -c viewdi -n '__fish_use_subcommand' -a 'completion' -d 'print the completion script'\n")
	b.WriteString("complete -c viewdi -n '__fish_use_subcommand' -a 'man' -d 'print the manual page'\n")
	b.WriteString("complete -c viewdi -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'\n")
	for _, s := range specs {
		fmt.Fprintf(&b, "complete -c viewdi -l %s", s.Name)
		if s.Value && !s.Optional {
			b.WriteString(" -r")
		}
		if len(s.Choices) > 0 {
			fmt.Fprintf(&b, " -a '%s'", choices(s))
		}
		fmt.Fprintf(&b, " -d '%s'\n", fishQuote(s.Usage))
	}
	return b.String()
}

// runSubcommand handles `viewdi completion <shell>` and `viewdi man`. It
// reports whether args named a subcommand.
func runSubcommand(args []string, stdout, stderr io.Writer) (handled bool, code int) {
	if len(args) == 0 {
		return false, 0
	}
	var f flags
	set := newFlagSet(&f, io.Discard)
	switch args[0] {
	case "man":
		if len(args) != 1 {
			_, _ = fmt.Fprintln(stderr, "viewdi: man takes no arguments")
			return true, 2
		}
		_, _ = io.WriteString(stdout, manPage(set))
		return true, 0
	case "completion":
		if len(args) != 2 {
			_, _ = fmt.Fprintln(stderr, "viewdi: usage: viewdi completion bash|zsh|fish")
			return true, 2
		}
		script, err := completion(args[1], set)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "viewdi:", err)
			return true, 2
		}
		_, _ = io.WriteString(stdout, script)
		return true, 0
	}
	return false, 0
}
