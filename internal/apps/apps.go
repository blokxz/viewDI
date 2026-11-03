// Package apps turns the machines of both engines into the apps the Home
// shows: one card per compose project, lab or library entry, and one for
// everything else, each with a health in one word and its usage. It reads
// store data only (no daemon, no network) and is pure, so the Home can
// derive it on every data message.
package apps

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/blokxz/viewDI/internal/alerts"
	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
)

// Kind says what makes an app.
type Kind int

// App kinds, in the order they take a machine: a lab the user drew, a
// compose project, a library entry, and everything else.
const (
	Lab Kind = iota
	Compose
	Library
	Other
)

func (k Kind) String() string { return [...]string{"lab", "compose", "library", "other"}[k] }

// Health is an app's state in one word. The values are ordered by how much
// they need the user: a larger value is worse, except Off, which is the
// calm state of an app that is switched off.
type Health int

// Health values.
const (
	Off       Health = iota // every machine is stopped on purpose
	OK                      // everything that should run, runs
	Starting                // something is starting or restarting
	Attention               // running, but an alert or a failed health check says otherwise
	Down                    // a machine that was running stopped without being asked to
)

func (h Health) String() string { return [...]string{"off", "ok", "starting", "attention", "down"}[h] }

// Label keys of the machines created from a library entry (internal/ui/views/library
// holds the same constants for the view; apps must not import ui).
const (
	labelDocker  = "io.viewdi.library"
	labelIncus   = "viewdi.library"
	labelCompose = "com.docker.compose.project"
)

// App is one card of the Home.
type App struct {
	ID       string // stable across refreshes: "lab:3", "compose:web", "library:docker/postgres-2", "other"
	Name     string
	Kind     Kind
	Machines []engine.Key // sorted by name
	Health   Health
	// Reason is the most important thing wrong, in a few words, for
	// Attention and Down ("web-db memory 92%"); "" otherwise.
	Reason string
	// Counts of machines by what they are doing.
	Up, Starting, Down, Off int
	// Downs are the machines that stopped on their own.
	Downs    []engine.Key
	CPU      float64 // percent of one core, summed over running machines
	MemUsed  uint64
	MemLimit uint64 // sum of the limits of machines that have one
}

// Inputs is what Derive reads.
type Inputs struct {
	Instances []engine.Instance
	Alerts    []alerts.Alert
	Labs      config.Labs
	Points    map[engine.Key]metrics.Point
	// Down holds the machines a Tracker saw stop on their own.
	Down map[engine.Key]bool
}

// Derive groups the machines into apps and rates each one. The order of the
// result is the Home's: apps that need the user first (worst health), then
// the busiest, then by name.
func Derive(in Inputs) []App {
	byID := map[string]*App{}
	var order []string
	get := func(id, name string, kind Kind) *App {
		a, ok := byID[id]
		if !ok {
			a = &App{ID: id, Name: name, Kind: kind}
			byID[id] = a
			order = append(order, id)
		}
		return a
	}
	labOf := labIndex(in.Labs)
	for _, m := range in.Instances {
		id, name, kind := classify(m, labOf)
		a := get(id, name, kind)
		a.Machines = append(a.Machines, m.Key())
	}
	alertOf := map[engine.Key]alerts.Alert{}
	for _, al := range in.Alerts {
		if al.Key == "" || al.Severity < alerts.Warn {
			continue
		}
		if cur, ok := alertOf[al.Key]; !ok || al.Severity > cur.Severity {
			alertOf[al.Key] = al
		}
	}
	byKey := map[engine.Key]engine.Instance{}
	for _, m := range in.Instances {
		byKey[m.Key()] = m
	}
	out := make([]App, 0, len(order))
	for _, id := range order {
		a := byID[id]
		slices.SortFunc(a.Machines, func(x, y engine.Key) int { return cmp.Compare(byKey[x].Name, byKey[y].Name) })
		rate(a, byKey, alertOf, in)
		out = append(out, *a)
	}
	SortApps(out)
	return out
}

// SortApps puts apps that need attention first, then the busiest, then by name.
func SortApps(as []App) {
	slices.SortStableFunc(as, func(a, b App) int {
		if c := cmp.Compare(rank(b.Health), rank(a.Health)); c != 0 {
			return c
		}
		if c := cmp.Compare(b.CPU, a.CPU); c != 0 {
			return c
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
}

// rank orders the health values for the Home: down, attention, starting, ok, off.
func rank(h Health) int { return int(h) }

// labIndex maps a machine's identity to the first lab that lists it.
func labIndex(labs config.Labs) map[string]config.Lab {
	idx := map[string]config.Lab{}
	for _, l := range labs.Labs {
		for _, m := range l.Members {
			if _, taken := idx[m.String()]; !taken {
				idx[m.String()] = l
			}
		}
	}
	return idx
}

func classify(m engine.Instance, labOf map[string]config.Lab) (id, name string, kind Kind) {
	member := config.LabMember{Engine: string(m.Engine), Project: m.Project, Name: m.Name}
	if l, ok := labOf[member.String()]; ok {
		return "lab:" + strconv.Itoa(l.ID), l.Name, Lab
	}
	if x, ok := m.Extra.(*engine.DockerExtra); ok && x.ComposeProject != "" {
		return "compose:" + x.ComposeProject, x.ComposeProject, Compose
	}
	if m.Labels[labelCompose] != "" {
		return "compose:" + m.Labels[labelCompose], m.Labels[labelCompose], Compose
	}
	if m.Labels[labelDocker] != "" || m.Labels[labelIncus] != "" {
		return "library:" + string(m.Key()), m.Name, Library
	}
	return "other", "other machines", Other
}

// rate counts the machines of an app, sums their usage and decides its health.
func rate(a *App, byKey map[engine.Key]engine.Instance, alertOf map[engine.Key]alerts.Alert, in Inputs) {
	worst := Off
	bump := func(h Health, reason string) {
		if h > worst {
			worst = h
			a.Reason = ""
		}
		if h == worst && a.Reason == "" && (h == Attention || h == Down) {
			a.Reason = reason
		}
	}
	for _, k := range a.Machines {
		m := byKey[k]
		switch m.State {
		case engine.StateRunning:
			a.Up++
			bump(OK, "")
			if m.Health == engine.HealthUnhealthy {
				bump(Attention, m.Name+" is unhealthy")
			}
			if al, ok := alertOf[k]; ok {
				bump(Attention, m.Name+" "+shortAlert(al))
			}
			if p, ok := in.Points[k]; ok {
				a.CPU += p.CPU
				a.MemUsed += p.MemUsed
				a.MemLimit += p.MemLimit
			}
		case engine.StateRestarting, engine.StateCreated:
			if m.State == engine.StateRestarting || in.Down[k] {
				a.Starting++
				bump(Starting, "")
				continue
			}
			a.Off++
		case engine.StatePaused:
			a.Up++
			bump(Attention, m.Name+" is paused")
		default: // stopped, dead, error, removing, unknown
			if down(m, in) {
				a.Down++
				a.Downs = append(a.Downs, k)
				bump(Down, m.Name+" stopped on its own")
			} else {
				a.Off++
			}
		}
	}
	if a.Starting > 0 {
		bump(Starting, "")
	}
	if worst == Off && a.Up > 0 {
		worst = OK
	}
	a.Health = worst
}

// down reports whether a stopped machine stopped without being asked to: a
// Tracker saw it stop, or a Docker container exited with a non-zero code.
func down(m engine.Instance, in Inputs) bool {
	if in.Down[m.Key()] {
		return true
	}
	if x, ok := m.Extra.(*engine.DockerExtra); ok && m.State == engine.StateStopped && x.ExitCode != 0 {
		return true
	}
	return m.State == engine.StateDead || m.State == engine.StateError
}

// shortAlert is the alert text without the subject, which the Reason adds.
func shortAlert(a alerts.Alert) string {
	t := strings.TrimSpace(a.Text)
	if i := strings.Index(t, " "); i > 0 && strings.EqualFold(t[:i], a.Subject) {
		t = strings.TrimSpace(t[i:])
	}
	return t
}
