package activity

import (
	"github.com/blokxz/viewDI/internal/engine"
	upd "github.com/blokxz/viewDI/internal/updates"
)

// CancelMsg asks the app to cancel a job.
type CancelMsg struct{ ID uint64 }

// SnoozeMsg asks the app to snooze (or unsnooze) an update.
type SnoozeMsg struct{ Key, Text string }

// IgnoreMsg asks the app to ignore (or stop ignoring) one version.
type IgnoreMsg struct{ Key, Version, Text string }

// OpenNotesMsg asks the app to open release notes in the browser.
type OpenNotesMsg struct{ URL string }

// OpenMachineMsg asks the app to open the detail of a machine.
type OpenMachineMsg struct{ Key engine.Key }

// Image actions (ImageActionMsg.Action).
const (
	ActPull     = "pull"
	ActRefresh  = "refresh"
	ActRecreate = "recreate"
)

// ImageActionMsg asks the app to pull, refresh or recreate from an image
// update. Image is nil when the line is not an image (the app explains).
type ImageActionMsg struct {
	Action string
	Image  *upd.ImageUpdate
}

// UpgradeMsg asks the app to upgrade the engine of an update line with the
// package manager (after a confirmation). Engine is nil on an image line
// (the app explains).
type UpgradeMsg struct{ Engine *upd.EngineStatus }
