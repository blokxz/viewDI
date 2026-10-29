package engine

import (
	"strings"
	"time"
)

// ID names an engine kind.
type ID string

// Engine IDs.
const (
	Docker ID = "docker"
	Incus  ID = "incus"
)

// IDs lists the engines in display order.
var IDs = []ID{Docker, Incus}

// Label is the display name ("Docker").
func (id ID) Label() string {
	switch id {
	case Docker:
		return "Docker"
	case Incus:
		return "Incus"
	}
	return string(id)
}

// Short is the compact column label ("dock").
func (id ID) Short() string {
	if id == Docker {
		return "dock"
	}
	return string(id)
}

// Kind is the instance kind.
type Kind int

// Instance kinds.
const (
	KindContainer Kind = iota
	KindVM
)

func (k Kind) String() string {
	if k == KindVM {
		return "vm"
	}
	return "ctr"
}

// State is the normalised lifecycle state.
type State int

// Instance states, shared by both engines.
const (
	StateUnknown    State = iota
	StateCreated          // created, never started
	StateRunning          // running
	StatePaused           // Docker paused / Incus frozen
	StateRestarting       // Docker restarting
	StateStopped          // exited / stopped
	StateRemoving         // being removed
	StateDead             // Docker dead
	StateError            // Incus error
)

var stateNames = [...]string{"unknown", "created", "running", "paused", "restarting", "stopped", "removing", "dead", "error"}

func (s State) String() string {
	if s < 0 || int(s) >= len(stateNames) {
		return "unknown"
	}
	return stateNames[s]
}

// Health is the container healthcheck status (Docker only).
type Health int

// Health values.
const (
	HealthNone Health = iota // no healthcheck
	HealthStarting
	HealthHealthy
	HealthUnhealthy
)

func (h Health) String() string {
	switch h {
	case HealthStarting:
		return "starting"
	case HealthHealthy:
		return "healthy"
	case HealthUnhealthy:
		return "unhealthy"
	}
	return ""
}

// Key identifies an instance across engines: "<engine>/<id>". It is stable
// across refreshes, so cursors and selections survive resyncs.
type Key string

// MakeKey builds a Key.
func MakeKey(e ID, id string) Key { return Key(string(e) + "/" + id) }

// Engine returns the engine part of a key.
func (k Key) Engine() ID {
	e, _, _ := strings.Cut(string(k), "/")
	return ID(e)
}

// Instance is a container or VM of either engine.
type Instance struct {
	// ID is the engine-local identifier: the full container ID for Docker,
	// "<project>/<name>" for Incus. Stable for the life of the instance.
	ID      string
	Engine  ID
	Name    string
	Project string // Incus project; "" for Docker
	Kind    Kind
	State   State
	Health  Health
	Status  string // the daemon's own status text, e.g. "Up 2 days (healthy)"
	Image   string // image reference or description
	Created time.Time
	Started time.Time // zero when not running or unknown
	PID     int       // host PID of the init process; 0 when not running or unknown
	Labels  map[string]string

	Networks []Attachment
	Ports    []Port
	Mounts   []Mount

	// Extra holds engine-specific data: *DockerExtra or *IncusExtra.
	Extra Extra
}

// Key returns the cross-engine key.
func (i Instance) Key() Key { return MakeKey(i.Engine, i.ID) }

// Uptime returns how long the instance has been running at now, or 0.
func (i Instance) Uptime(now time.Time) time.Duration {
	if i.State != StateRunning || i.Started.IsZero() || now.Before(i.Started) {
		return 0
	}
	return now.Sub(i.Started)
}

// Attachment is an instance's presence on a network.
type Attachment struct {
	Network string
	IPv4    string
	IPv6    string
	MAC     string
}

// Port is a published port.
type Port struct {
	HostIP        string
	HostPort      uint16
	ContainerPort uint16
	Protocol      string // tcp, udp, sctp
}

// Mount is a filesystem attached to an instance.
type Mount struct {
	Type        string // bind, volume, tmpfs, disk
	Source      string
	Destination string
	ReadOnly    bool
}

// Extra is implemented by the engine-specific detail structs.
type Extra interface{ engineExtra() }

// DockerExtra holds Docker-only instance data.
type DockerExtra struct {
	ComposeProject string
	ComposeService string
	Command        string
	RestartPolicy  string
	ExitCode       int
}

// IncusExtra holds Incus-only instance data.
type IncusExtra struct {
	Profiles     []string
	Location     string // cluster member
	Description  string
	Ephemeral    bool
	Architecture string
	BaseImage    string // volatile.base_image fingerprint
}

func (*DockerExtra) engineExtra() {}
func (*IncusExtra) engineExtra()  {}

// Network is a Docker network or an Incus network.
type Network struct {
	ID       string
	Engine   ID
	Name     string
	Project  string
	Driver   string // bridge, macvlan, overlay, ovn…
	Scope    string
	Subnets  []string
	Gateway  string
	Managed  bool // Incus: managed by Incus; Docker: always true
	Internal bool
}

// Volume is a Docker volume or an Incus custom storage volume.
type Volume struct {
	Engine     ID
	Name       string
	Project    string
	Pool       string // Incus storage pool
	Driver     string
	Mountpoint string
	Created    time.Time
	UsedBy     []string
}

// Info describes a daemon.
type Info struct {
	Engine        ID
	Version       string // engine version, e.g. "29.0.1"
	APIVersion    string // negotiated API version
	OS            string
	Arch          string
	Kernel        string
	StorageDriver string
	CgroupVersion string
	Socket        string // connection endpoint
	Rootless      bool   // Docker rootless daemon
	Host          string // daemon host name
	NCPU          int    // host CPUs
	MemTotal      uint64 // host memory, bytes
	Images        int    // local images
	Projects      []string
	Capabilities  Capabilities
}

// Capabilities says what the connection may do.
type Capabilities struct {
	// Restricted is set for limited connections (Incus user socket,
	// untrusted clients): mutating actions are disabled in the UI.
	Restricted bool
	// AllProjects reports whether every project can be listed at once.
	AllProjects bool
	// VMs reports virtual machine support.
	VMs bool
	// Reason explains a restriction, for the UI.
	Reason string
}

// EventKind classifies an instance event.
type EventKind int

// Event kinds.
const (
	EventChanged EventKind = iota // state or config changed: re-inspect
	EventRemoved                  // instance gone
)

// Event is one instance lifecycle event.
type Event struct {
	Engine     ID
	Kind       EventKind
	Action     string // engine action, e.g. "start", "instance-stopped"
	InstanceID string // engine-local ID (Instance.ID)
	Name       string
	Time       time.Time
}

// Key returns the key of the instance the event is about.
func (e Event) Key() Key { return MakeKey(e.Engine, e.InstanceID) }

// IsStart reports whether an event action means the instance (re)started,
// for restart-loop detection.
func (e Event) IsStart() bool {
	switch e.Action {
	case "start", "instance-started", "instance-restarted":
		return true
	}
	return false
}
