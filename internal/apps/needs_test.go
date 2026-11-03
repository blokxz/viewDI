package apps

import (
	"strings"
	"testing"

	"github.com/blokxz/viewDI/internal/alerts"
	"github.com/blokxz/viewDI/internal/engine"
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

func TestACopyWithoutItsInstanceNeedsYou(t *testing.T) {
	copyOf := engine.Instance{Engine: engine.Incus, Project: "default", Name: "web" + copyInfix + "20261103-180000"}
	web := engine.Instance{Engine: engine.Incus, Project: "default", Name: "web"}
	other := engine.Instance{Engine: engine.Incus, Project: "lab", Name: "web"} // another project does not count

	if got := Needs(Inputs{Instances: []engine.Instance{copyOf, web}}, nil, false); len(got) != 0 {
		t.Fatalf("a copy next to its instance is fine, got %v", got)
	}
	got := Needs(Inputs{Instances: []engine.Instance{copyOf, other}}, nil, true)
	if len(got) != 1 || got[0].Severity != alerts.Error || got[0].Machine != copyOf.Key() {
		t.Fatalf("items %+v", got)
	}
	if want := "incus rename " + copyOf.Name + " web"; !strings.Contains(got[0].Text, want) {
		t.Errorf("text %q lacks %q", got[0].Text, want)
	}
	if len(got[0].Actions) != 1 || got[0].Actions[0].ID != ActSee {
		t.Errorf("actions %+v", got[0].Actions) // looking is allowed in read-only
	}
}
