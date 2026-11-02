package supervisor

import (
	"fmt"

	"github.com/blokxz/viewDI/internal/diag"
	"github.com/blokxz/viewDI/internal/engine"
)

// diagEnv is where permission hints look at the system; tests replace it.
var diagEnv = diag.System

// Hint returns an actionable fix for an unavailable engine. A refused socket
// is inspected first (diag): "join the group" is only the right advice when
// the user is not in it, and it does not help a session that predates the
// usermod.
func Hint(id engine.ID, class engine.Class, endpoint string) string {
	if class == engine.ClassPermission {
		if path, ok := diag.SocketPath(endpoint); ok {
			return diag.Hint(map[engine.ID]string{engine.Docker: "Docker", engine.Incus: "Incus"}[id], diag.Diagnose(diagEnv(), path))
		}
	}
	switch {
	case id == engine.Docker && class == engine.ClassPermission:
		return fmt.Sprintf("Permission denied on %s. Join the docker group: sudo usermod -aG docker $USER && newgrp docker (or use rootless Docker).", endpoint)
	case id == engine.Docker && class == engine.ClassMissing:
		return fmt.Sprintf("Docker socket not found at %s. Is dockerd installed and running? (systemctl status docker)", endpoint)
	case id == engine.Incus && class == engine.ClassPermission:
		return fmt.Sprintf("Permission denied on %s. Join incus-admin: sudo usermod -aG incus-admin $USER && newgrp incus-admin (or the incus group for the restricted socket).", endpoint)
	case id == engine.Incus && class == engine.ClassMissing:
		return fmt.Sprintf("Incus socket not found at %s. Is incusd installed and running? (systemctl status incus)", endpoint)
	}
	return ""
}
