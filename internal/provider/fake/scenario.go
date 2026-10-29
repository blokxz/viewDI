package fake

import (
	"context"
	"fmt"
	"slices"
	"syscall"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

// Scenario is a scripted sequence of world changes and failures
// (docs/05-quality-plan.md §1.2), written with a small builder:
//
//	s := fake.NewScenario("demo", fake.Homelab)
//	s.At(2 * time.Second).Stop(engine.Docker, "web-api")
//	s.At(5 * time.Second).Hang(engine.Incus, 10*time.Second)
//	s.At(8 * time.Second).Disconnect(engine.Docker)
type Scenario struct {
	Name  string
	Setup func(*World)
	Steps []Step
	// Loop restarts the step list every Loop once the last step ran
	// (0 = run once).
	Loop time.Duration
}

// Step is one timed action, relative to the scenario start.
type Step struct {
	At   time.Duration
	Desc string
	Do   func(*World)
}

// NewScenario returns an empty scenario whose world is prepared by setup.
func NewScenario(name string, setup func(*World)) *Scenario {
	return &Scenario{Name: name, Setup: setup}
}

// At starts a step at offset d.
func (s *Scenario) At(d time.Duration) StepBuilder { return StepBuilder{s, d} }

// StepBuilder adds the action for one offset.
type StepBuilder struct {
	s  *Scenario
	at time.Duration
}

// Do adds an arbitrary action.
func (b StepBuilder) Do(desc string, f func(*World)) *Scenario {
	b.s.Steps = append(b.s.Steps, Step{At: b.at, Desc: desc, Do: f})
	return b.s
}

// Start starts an instance.
func (b StepBuilder) Start(id engine.ID, name string) *Scenario {
	return b.state(id, name, engine.StateRunning)
}

// Stop stops an instance.
func (b StepBuilder) Stop(id engine.ID, name string) *Scenario {
	return b.state(id, name, engine.StateStopped)
}

// Pause pauses (Docker) or freezes (Incus) an instance.
func (b StepBuilder) Pause(id engine.ID, name string) *Scenario {
	return b.state(id, name, engine.StatePaused)
}

func (b StepBuilder) state(id engine.ID, name string, st engine.State) *Scenario {
	return b.Do(fmt.Sprintf("%s %s → %v", id, name, st), func(w *World) { w.Engine(id).SetState(name, st) })
}

// Add creates an instance.
func (b StepBuilder) Add(id engine.ID, in engine.Instance) *Scenario {
	return b.Do(fmt.Sprintf("%s add %s", id, in.Name), func(w *World) { w.Engine(id).Add(in) })
}

// Remove deletes an instance.
func (b StepBuilder) Remove(id engine.ID, name string) *Scenario {
	return b.Do(fmt.Sprintf("%s remove %s", id, name), func(w *World) { w.Engine(id).Remove(name) })
}

// Hang makes the engine hang for d, then recover (F2/F3).
func (b StepBuilder) Hang(id engine.ID, d time.Duration) *Scenario {
	b.Do(fmt.Sprintf("%s hangs for %s", id, d), func(w *World) { w.Engine(id).Hang() })
	return b.s.At(b.at + d).Recover(id)
}

// Fail makes every call fail with errno (EACCES for F5, ENOENT for a
// removed socket).
func (b StepBuilder) Fail(id engine.ID, errno syscall.Errno) *Scenario {
	return b.Do(fmt.Sprintf("%s fails with %s", id, errno), func(w *World) { w.Engine(id).FailErrno(errno) })
}

// Disconnect stops the daemon (F1).
func (b StepBuilder) Disconnect(id engine.ID) *Scenario {
	return b.Do(fmt.Sprintf("%s disconnects", id), func(w *World) { w.Engine(id).Disconnect() })
}

// Recover clears the engine's fault.
func (b StepBuilder) Recover(id engine.ID) *Scenario {
	return b.Do(fmt.Sprintf("%s recovers", id), func(w *World) { w.Engine(id).Recover() })
}

// Latency sets a per-call delay (F6).
func (b StepBuilder) Latency(id engine.ID, d time.Duration) *Scenario {
	return b.Do(fmt.Sprintf("%s latency %s", id, d), func(w *World) { w.Engine(id).SetLatency(d) })
}

// Build populates a new world on w's clock.
func (s *Scenario) Build(w *World) {
	if s.Setup != nil {
		s.Setup(w)
	}
}

// Run executes the steps on w's clock until they are done (or forever when
// Loop is set) or ctx ends. step, if non-nil, is called before each action.
func (s *Scenario) Run(ctx context.Context, w *World, step func(Step)) {
	steps := slices.Clone(s.Steps)
	slices.SortStableFunc(steps, func(a, b Step) int { return int(a.At - b.At) })
	if len(steps) == 0 {
		return
	}
	for {
		var elapsed time.Duration
		for _, st := range steps {
			if wait := st.At - elapsed; wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-w.Clock.After(wait):
				}
				elapsed = st.At
			}
			if step != nil {
				step(st)
			}
			st.Do(w)
		}
		if s.Loop <= 0 {
			return
		}
		if wait := s.Loop - elapsed; wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-w.Clock.After(wait):
			}
		}
	}
}

// Scenarios returns the bundled scenarios by --mock name.
func Scenarios() map[string]*Scenario {
	return map[string]*Scenario{
		"homelab":  homelabScenario(),
		"large":    NewScenario("large", func(w *World) { Large(w, 500) }),
		"chaos":    chaosScenario(),
		"logflood": NewScenario("logflood", Homelab), // log streams arrive in Stage 3
		"offline":  NewScenario("offline", Homelab),  // no network for update checks (Stage 6)
	}
}

// homelabScenario is the default demo: a little life in the engines.
func homelabScenario() *Scenario {
	s := NewScenario("homelab", Homelab)
	s.Loop = 60 * time.Second
	s.At(10*time.Second).Stop(engine.Docker, "nginx-proxy")
	s.At(14*time.Second).Start(engine.Docker, "nginx-proxy")
	s.At(25*time.Second).Start(engine.Incus, "test-alpine")
	s.At(40*time.Second).Stop(engine.Incus, "test-alpine")
	return s
}

// chaosScenario rotates through the failure catalogue (F1, F2, F5, F6).
func chaosScenario() *Scenario {
	s := NewScenario("chaos", Homelab)
	s.Loop = 120 * time.Second
	s.At(5 * time.Second).Disconnect(engine.Incus)
	s.At(20 * time.Second).Recover(engine.Incus)
	s.At(30*time.Second).Hang(engine.Docker, 12*time.Second)
	s.At(50*time.Second).Fail(engine.Incus, syscall.EACCES)
	s.At(70 * time.Second).Recover(engine.Incus)
	s.At(75*time.Second).Latency(engine.Docker, 3*time.Second)
	s.At(90*time.Second).Latency(engine.Docker, 0)
	s.At(95*time.Second).Stop(engine.Docker, "web-api")
	s.At(100*time.Second).Start(engine.Docker, "web-api")
	return s
}
