package apps

import (
	"testing"

	"github.com/blokxz/viewDI/internal/alerts"
)

func TestMemoryAlertOffersToRaiseTheLimitUnlessReadOnly(t *testing.T) {
	al := alerts.Alert{ID: "memory:docker/a", Rule: alerts.RuleMemory, Severity: alerts.Warn, Key: "docker/a", Subject: "a", Text: "using 95% of its memory"}
	in := Inputs{Alerts: []alerts.Alert{al}}
	ids := func(readOnly bool) []string {
		var out []string
		for _, it := range Needs(in, nil, readOnly) {
			for _, a := range it.Actions {
				out = append(out, a.ID)
			}
		}
		return out
	}
	if got := ids(false); len(got) != 2 || got[0] != ActLogs || got[1] != ActLimits {
		t.Fatalf("actions %v", got)
	}
	if got := ids(true); len(got) != 1 || got[0] != ActLogs {
		t.Fatalf("read-only actions %v", got)
	}
}
