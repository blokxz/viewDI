package views

import (
	"strconv"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// StateCell returns the glyph+label and the level that colors it; muted
// states (created, stopped) are drawn in the muted style.
func StateCell(in engine.Instance) (string, theme.Level, bool) {
	switch in.State {
	case engine.StateRunning:
		switch in.Health {
		case engine.HealthUnhealthy:
			return "▲ unhealthy", theme.LevelWarn, false
		case engine.HealthStarting:
			return "◐ starting", theme.LevelInfo, false
		}
		return "● running", theme.LevelOK, false
	case engine.StatePaused:
		return "⏸ paused", theme.LevelWarn, false
	case engine.StateRestarting:
		return "↻ restarting", theme.LevelWarn, false
	case engine.StateCreated:
		return "○ created", theme.LevelInfo, true
	case engine.StateStopped:
		if x, ok := in.Extra.(*engine.DockerExtra); ok && x.ExitCode != 0 {
			return "■ exited " + strconv.Itoa(x.ExitCode), theme.LevelErr, false
		}
		return "■ stopped", theme.LevelInfo, true
	case engine.StateError, engine.StateDead:
		return "✖ " + in.State.String(), theme.LevelErr, false
	case engine.StateRemoving:
		return "… removing", theme.LevelWarn, false
	}
	return "? unknown", theme.LevelInfo, true
}

// Uptime formats how long an instance has been running ("2d", "6h", "45m").
func Uptime(in engine.Instance, now time.Time) string {
	d := in.Uptime(now)
	switch {
	case d <= 0:
		return "—"
	case d >= 24*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d >= time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d >= time.Minute:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	}
	return strconv.Itoa(int(d/time.Second)) + "s"
}
