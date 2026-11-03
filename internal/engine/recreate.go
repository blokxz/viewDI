package engine

import (
	"errors"
	"fmt"
	"time"
)

// RecreateOptions tunes a recreate.
type RecreateOptions struct {
	// Timeout is the grace period of stopping the old container; 0 uses
	// DefaultStopTimeout.
	Timeout time.Duration
	// Pull pulls the image reference before creating the new container.
	Pull bool
	// Source is the image reference an Incus instance is rebuilt from
	// ("images:ubuntu/noble"); Docker ignores it.
	Source string
	// Copy keeps a copy of the Incus instance before it is rebuilt, and puts
	// it back if the rebuild fails.
	Copy bool
}

// Phases a recreate reports, in order.
const (
	PhasePulling     = "pulling"
	PhaseStopping    = "stopping"
	PhaseRenaming    = "renaming"
	PhaseCreating    = "creating"
	PhaseStarting    = "starting"
	PhaseRemoving    = "removing old"
	PhaseRollingBack = "rolling back"

	// Incus rebuild only.
	PhaseCopying    = "copying"
	PhaseRebuilding = "rebuilding"
)

// OldSuffix is appended to the name of the container a recreate replaces
// while the new one starts.
const OldSuffix = "-old"

// ComposeProjectLabel and ComposeServiceLabel mark containers that Docker
// Compose manages; viewDI does not recreate those (Compose would not know).
const (
	ComposeProjectLabel = "com.docker.compose.project"
	ComposeServiceLabel = "com.docker.compose.service"
)

// ComposeHint returns the command that recreates a Compose-managed
// container, or "" when in is not one.
func ComposeHint(in Instance) string {
	project, service := in.Labels[ComposeProjectLabel], in.Labels[ComposeServiceLabel]
	if x, ok := in.Extra.(*DockerExtra); ok && x.ComposeProject != "" {
		project, service = x.ComposeProject, x.ComposeService
	}
	if project == "" || service == "" {
		return ""
	}
	return fmt.Sprintf("docker compose -p %s up -d %s", project, service)
}

// RebuildHint is the command that rebuilds an Incus instance from the
// newest build of its image, and the warning that goes with it.
func RebuildHint(in Instance, alias string) (cmd, warning string) {
	cmd = "incus rebuild " + alias + " " + in.Name
	if in.Project != "" && in.Project != "default" {
		cmd += " --project " + in.Project
	}
	return cmd, "rebuild wipes the root filesystem: everything not on a separate volume is lost"
}

// ErrUnsupported is the base error of operations an engine does not offer.
var ErrUnsupported = errors.New("not supported")

// Unsupported returns a ClassConflict error for an operation the engine
// does not offer, with why (what to do instead).
func Unsupported(op, why string) error {
	return &Error{Class: ClassConflict, Op: op, Err: fmt.Errorf("%w: %s", ErrUnsupported, why)}
}
