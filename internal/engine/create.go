package engine

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Image is a local image.
type Image struct {
	Engine      ID
	ID          string   // Docker "sha256:…", Incus fingerprint
	Refs        []string // Docker repo:tags, Incus aliases
	Description string
	Size        uint64
	Created     time.Time
	VM          bool // Incus virtual-machine image
}

// RemoteImage is an image found by SearchImages.
type RemoteImage struct {
	Ref         string // what CreateSpec.Image accepts ("nginx", "images:debian/12")
	Description string
	Official    bool // Docker official image
	VM          bool // Incus: a virtual-machine variant exists
}

// Progress reports how far a long operation got.
type Progress struct {
	Phase          string // "pulling", "downloading", "uploading", "creating"…
	Current, Total int64  // bytes or steps; Total 0 when unknown
}

// ProgressFunc receives progress updates. It must not block.
type ProgressFunc func(Progress)

// CreateSpec describes a new instance. Docker uses the common fields plus
// Ports, Env, Volumes, Network, RestartPolicy and Labels; Incus the common
// fields plus Project, Kind, Profiles, CPU, Memory, Network and Pool.
type CreateSpec struct {
	Name  string // "" lets Docker pick one; required by Incus
	Image string // Docker reference; Incus local alias or "<remote>:<alias>"
	Pull  bool   // Docker: pull the image first
	Start bool   // start the instance once created

	Ports         []Port
	Env           []string
	Volumes       []Mount
	Network       string
	RestartPolicy string
	Labels        map[string]string

	Project  string
	Kind     Kind
	Profiles []string
	CPU      int    // limits.cpu; 0 = no limit
	Memory   string // limits.memory, e.g. "2GiB"; "" = no limit
	Pool     string // root disk storage pool; "" = the profile's
}

// ImportKind says what a file is.
type ImportKind int

// Import kinds.
const (
	ImportDockerImage  ImportKind = iota // docker save / OCI tarball → docker load
	ImportDockerRootfs                   // filesystem tarball → docker import (needs a reference)
	ImportIncusBackup                    // incus export tarball → new instance
	ImportIncusImage                     // unified image tarball → local image (alias optional)
)

// Engine returns the engine the kind belongs to.
func (k ImportKind) Engine() ID {
	if k == ImportIncusBackup || k == ImportIncusImage {
		return Incus
	}
	return Docker
}

func (k ImportKind) String() string {
	return [...]string{"Docker image tarball", "Docker root filesystem tarball", "Incus instance backup", "Incus image"}[k]
}

// ImportSpec describes an import.
type ImportSpec struct {
	Kind    ImportKind
	Path    string // local file
	Name    string // rootfs: image reference; backup: instance name ("" = the backup's); image: alias
	Pool    string // backup: storage pool ("" = default)
	Project string // Incus project
}

// Validation of user input. The UI checks fields with these before
// submitting; adapters may rely on specs being valid.
var (
	dockerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
	incusName  = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{0,62}$`)
	envName    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
	memSize    = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?(B|kB|MB|GB|TB|KiB|MiB|GiB|TiB|%)$`)
	imageRef   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:/@+-]*$`)
)

// ValidateName checks an instance name for engine e ("" is allowed for
// Docker, which then picks one).
func ValidateName(e ID, name string) error {
	switch {
	case name == "" && e == Docker:
		return nil
	case name == "":
		return errors.New("a name is required")
	case e == Incus && (!incusName.MatchString(name) || strings.HasSuffix(name, "-")):
		return errors.New("letters, digits and dashes; starts with a letter; at most 63")
	case e == Docker && !dockerName.MatchString(name):
		return errors.New("letters, digits, '_', '.', '-'; starts with a letter or digit")
	}
	return nil
}

// ValidateImage checks an image reference.
func ValidateImage(ref string) error {
	if ref == "" {
		return errors.New("an image is required")
	}
	if !imageRef.MatchString(ref) || len(ref) > 255 {
		return errors.New("not an image reference")
	}
	return nil
}

// ValidateMemory checks an Incus memory limit ("" = none).
func ValidateMemory(s string) error {
	if s != "" && !memSize.MatchString(s) {
		return errors.New(`a size such as 512MiB, 2GiB or 50%`)
	}
	return nil
}

// fields splits a list typed by the user on commas and whitespace.
func fields(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
}

// ParsePorts parses "8080:80, 127.0.0.1:5432:5432, 53:53/udp, 9000".
func ParsePorts(s string) ([]Port, error) {
	var out []Port
	for _, f := range fields(s) {
		spec, proto, _ := strings.Cut(f, "/")
		if proto == "" {
			proto = "tcp"
		}
		if proto != "tcp" && proto != "udp" && proto != "sctp" {
			return nil, fmt.Errorf("%q: protocol must be tcp, udp or sctp", f)
		}
		parts := strings.Split(spec, ":")
		var p Port
		p.Protocol = proto
		var host, ctr string
		switch len(parts) {
		case 1:
			ctr = parts[0]
		case 2:
			host, ctr = parts[0], parts[1]
		case 3:
			p.HostIP, host, ctr = parts[0], parts[1], parts[2]
		default:
			return nil, fmt.Errorf("%q: use [ip:]host:container[/proto]", f)
		}
		cp, err := strconv.ParseUint(ctr, 10, 16)
		if err != nil || cp == 0 {
			return nil, fmt.Errorf("%q: bad container port", f)
		}
		p.ContainerPort = uint16(cp)
		if host != "" {
			hp, err := strconv.ParseUint(host, 10, 16)
			if err != nil || hp == 0 {
				return nil, fmt.Errorf("%q: bad host port", f)
			}
			p.HostPort = uint16(hp)
		}
		out = append(out, p)
	}
	return out, nil
}

// ParseEnv parses "KEY=value, OTHER=x".
func ParseEnv(s string) ([]string, error) {
	var out []string
	for _, f := range fields(s) {
		k, _, ok := strings.Cut(f, "=")
		if !ok || !envName.MatchString(k) {
			return nil, fmt.Errorf("%q: use NAME=value", f)
		}
		out = append(out, f)
	}
	return out, nil
}

// ParseVolumes parses "data:/var/lib/data, /srv/conf:/etc/app:ro": a source
// starting with / is a bind mount, anything else a named volume.
func ParseVolumes(s string) ([]Mount, error) {
	var out []Mount
	for _, f := range fields(s) {
		parts := strings.Split(f, ":")
		if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || !strings.HasPrefix(parts[1], "/") {
			return nil, fmt.Errorf("%q: use source:/path[:ro]", f)
		}
		m := Mount{Type: "volume", Source: parts[0], Destination: parts[1]}
		if strings.HasPrefix(m.Source, "/") {
			m.Type = "bind"
		}
		if len(parts) == 3 {
			switch parts[2] {
			case "ro":
				m.ReadOnly = true
			case "rw":
			default:
				return nil, fmt.Errorf("%q: mode must be ro or rw", f)
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// ParseLabels parses "tier=web, owner=me".
func ParseLabels(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range fields(s) {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("%q: use key=value", f)
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// ParseList parses "default, vm-large".
func ParseList(s string) []string { return fields(s) }

// RestartPolicies are Docker's restart policies.
var RestartPolicies = []string{"no", "on-failure", "unless-stopped", "always"}
