package catalog

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/blokxz/viewDI/internal/engine"
)

var (
	idRE       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	envNameRE  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	volNameRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,39}$`)
	memoryRE   = regexp.MustCompile(`^[1-9][0-9]{0,5}(MiB|GiB|MB|GB)$`)
	profileRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	incusImage = regexp.MustCompile(`^images:[a-z0-9][a-z0-9._/-]{0,79}$`)
	// A Docker reference with an explicit tag. "latest" is refused: an entry
	// must name what it runs.
	dockerImage = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,127}(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})$`)
)

// incusConfigKeys are the instance config keys an entry may set. cloud-init
// has its own field; limits have theirs.
var incusConfigKeys = map[string]bool{"security.nesting": true, "boot.autostart": true}

var restartPolicies = map[string]bool{"": true, "no": true, "always": true, "unless-stopped": true, "on-failure": true}

// Validate checks an entry, returning every problem found.
func (e Entry) Validate() error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if !idRE.MatchString(e.ID) {
		bad("id %q: lower-case letters, digits and dashes, 1–40 characters", e.ID)
	}
	if n := strings.TrimSpace(e.Name); n == "" || len(n) > 40 || hasControl(n) {
		bad("name: 1–40 printable characters")
	}
	if c := strings.TrimSpace(e.Category); c == "" || len(c) > 24 || hasControl(c) {
		bad("category: 1–24 printable characters")
	}
	if d := e.Description; len(d) > 200 || hasControl(d) {
		bad("description: at most 200 printable characters")
	}
	if e.Docs != "" {
		if u, err := url.Parse(e.Docs); err != nil || u.Scheme != "https" || u.Host == "" || len(e.Docs) > 200 || hasControl(e.Docs) {
			bad("docs: an https URL of at most 200 characters")
		}
	}
	if err := ValidateNote(e.Note); err != nil {
		bad("note: %v", err)
	}

	switch e.Engine {
	case engine.Docker:
		errs = append(errs, e.validateDocker()...)
	case engine.Incus:
		errs = append(errs, e.validateIncus()...)
	default:
		bad("engine %q: must be docker or incus", e.Engine)
	}
	return errors.Join(errs...)
}

func (e Entry) validateDocker() []error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	switch {
	case !dockerImage.MatchString(e.Image):
		bad("image %q: a Docker reference with an explicit tag", e.Image)
	case strings.HasSuffix(e.Image, ":latest"):
		bad("image %q: pin a version instead of latest", e.Image)
	}
	if len(e.Ports) > 16 {
		bad("ports: at most 16")
	}
	seenPort := map[string]bool{}
	for i, p := range e.Ports {
		if p.Container == 0 {
			bad("ports[%d]: container port 1–65535", i)
		}
		if p.Protocol != "" && p.Protocol != "tcp" && p.Protocol != "udp" {
			bad("ports[%d]: protocol must be tcp or udp", i)
		}
		key := strconv.Itoa(int(p.Container)) + "/" + p.Protocol
		if seenPort[key] {
			bad("ports[%d]: %d listed twice", i, p.Container)
		}
		seenPort[key] = true
	}
	if len(e.Env) > 32 {
		bad("env: at most 32")
	}
	seenEnv := map[string]bool{}
	for i, v := range e.Env {
		switch {
		case !envNameRE.MatchString(v.Name):
			bad("env[%d]: name %q is not a variable name", i, v.Name)
		case seenEnv[v.Name]:
			bad("env[%d]: %s listed twice", i, v.Name)
		}
		seenEnv[v.Name] = true
		if v.Generate != "" && v.Generate != "password" {
			bad("env[%d] %s: generate must be \"password\"", i, v.Name)
		}
		if v.Generate != "" && v.Value != "" {
			bad("env[%d] %s: give a value or generate, not both", i, v.Name)
		}
		if len(v.Value) > 256 || len(v.Description) > 120 || strings.ContainsAny(v.Value+v.Description, "\x00\n\r") {
			bad("env[%d] %s: value up to 256 and description up to 120 characters on one line", i, v.Name)
		}
	}
	if len(e.Volumes) > 8 {
		bad("volumes: at most 8")
	}
	seenVol := map[string]bool{}
	for i, v := range e.Volumes {
		switch {
		case !volNameRE.MatchString(v.Name):
			bad("volumes[%d]: name %q", i, v.Name)
		case seenVol[v.Name]:
			bad("volumes[%d]: %s listed twice", i, v.Name)
		}
		seenVol[v.Name] = true
		if !strings.HasPrefix(v.Path, "/") || len(v.Path) > 200 || strings.ContainsAny(v.Path, "\x00\n\r:") || strings.Contains(v.Path, "..") {
			bad("volumes[%d] %s: an absolute container path", i, v.Name)
		}
	}
	if !restartPolicies[e.Restart] {
		bad("restart %q: no, always, unless-stopped or on-failure", e.Restart)
	}
	if len(e.Kinds) > 0 || e.DefaultKind != "" || e.CPU != 0 || e.Memory != "" || len(e.Profiles) > 0 || len(e.Config) > 0 || e.CloudInit != "" {
		bad("Incus-only fields (kinds, cpu, memory, profiles, config, cloud_init) on a Docker entry")
	}
	return errs
}

func (e Entry) validateIncus() []error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if !incusImage.MatchString(e.Image) {
		bad("image %q: an alias on the images: remote, e.g. images:debian/12", e.Image)
	}
	if len(e.Kinds) == 0 {
		bad("kinds: list container, vm or both")
	}
	for _, k := range e.Kinds {
		if k != "container" && k != "vm" {
			bad("kinds: %q is not container or vm", k)
		}
	}
	if e.DefaultKind != "" && !e.HasKind(e.DefaultKind) {
		bad("default_kind %q is not among the kinds", e.DefaultKind)
	}
	if e.CPU < 0 || e.CPU > 64 {
		bad("cpu: 0–64")
	}
	if e.Memory != "" && !memoryRE.MatchString(e.Memory) {
		bad("memory %q: a number with MiB, GiB, MB or GB", e.Memory)
	}
	if len(e.Profiles) > 8 {
		bad("profiles: at most 8")
	}
	for _, p := range e.Profiles {
		if !profileRE.MatchString(p) {
			bad("profiles: %q", p)
		}
	}
	for k, v := range e.Config {
		if !incusConfigKeys[k] {
			bad("config: key %q is not allowed (use cloud_init, cpu or memory for those)", k)
		}
		if len(v) > 64 || strings.ContainsAny(v, "\x00\n\r") {
			bad("config %s: a short single-line value", k)
		}
	}
	if e.CloudInit != "" {
		switch {
		case !strings.HasSuffix(e.Image, "/cloud"):
			bad("cloud_init needs a cloud image (an alias ending in /cloud)")
		case len(e.CloudInit) > MaxCloudInit:
			bad("cloud_init: at most %d bytes", MaxCloudInit)
		case !strings.HasPrefix(e.CloudInit, "#cloud-config"):
			bad("cloud_init must start with #cloud-config")
		case strings.ContainsRune(e.CloudInit, 0):
			bad("cloud_init: contains a NUL byte")
		}
	}
	if len(e.Ports) > 0 || len(e.Env) > 0 || len(e.Volumes) > 0 || e.Restart != "" {
		bad("Docker-only fields (ports, env, volumes, restart) on an Incus entry")
	}
	return errs
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < ' ' || r == 0x7f {
			return true
		}
	}
	return false
}
