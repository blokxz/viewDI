package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/jobs"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/form"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/updates"
)

const formRebuild = "rebuild"

// rebuildRequest is what the rebuild form carries: the Incus image and the
// instances to rebuild on its newest build.
type rebuildRequest struct {
	image   updates.ImageUpdate
	targets []engine.Instance
	expect  string // what the user must type
}

// rebuildExpect is the text that confirms a rebuild: the instance's name,
// or how many instances there are.
func rebuildExpect(targets []engine.Instance) string {
	if len(targets) == 1 {
		return targets[0].Name
	}
	return strconv.Itoa(len(targets))
}

// previewRebuild opens the confirmation of an Incus rebuild. A rebuild
// replaces the root filesystem, so the user types the instance's name; a
// snapshot is taken first and restored if a step fails.
func (m Model) previewRebuild(img updates.ImageUpdate) (tea.Model, tea.Cmd) {
	snap := m.store.Snapshot()
	var targets []engine.Instance
	var skipped []string
	for _, k := range img.Instances {
		in, ok := snap.Lookup(k)
		if !ok {
			continue
		}
		if _, busy := snap.Pending(k); busy {
			skipped = append(skipped, in.Name+" (busy)")
			continue
		}
		targets = append(targets, in)
	}
	var cmds []tea.Cmd
	if len(skipped) > 0 {
		cmds = append(cmds, toast.Show(theme.LevelWarn, "Skipped "+textsafe.Clean(strings.Join(skipped, ", "))))
	}
	if len(targets) == 0 {
		if len(cmds) == 0 {
			cmds = append(cmds, toast.Show(theme.LevelInfo, "No instance to rebuild for "+textsafe.Clean(img.Check.Ref)))
		}
		return m, tea.Batch(cmds...)
	}
	req := rebuildRequest{image: img, targets: targets, expect: rebuildExpect(targets)}
	var b strings.Builder
	fmt.Fprintf(&b, "Replaces the root disk of %s with the newest build of %s.\n\n", rebuildNames(targets), textsafe.Clean(img.Check.Ref))
	b.WriteString("Kept:  config, devices, attached volumes\n")
	b.WriteString("Lost:  everything else on the root disk\n\n")
	b.WriteString("First a snapshot " + incusSnapshotPrefix + "<date> is taken; if any step\n")
	b.WriteString("fails it is restored. Running instances are stopped for the\n")
	b.WriteString("rebuild and started again. No snapshot, no rebuild.")
	label := "Type " + req.expect + " to confirm"
	if len(targets) > 1 {
		label = "Type " + req.expect + " (the number of instances) to confirm"
	}
	expect := req.expect
	fields := []form.Field{{
		ID: "confirm", Label: label, Required: true, Placeholder: req.expect,
		Validate: func(v string) error {
			if v != expect {
				return errors.New("type " + expect + " to confirm")
			}
			return nil
		},
	}}
	title := "Rebuild " + rebuildNames(targets)
	f := form.New(m.keys, formRebuild, title, b.String(), "Rebuild", fields).WithContext(req)
	return m, tea.Batch(append(cmds, modal.Open(f))...)
}

// incusSnapshotPrefix mirrors the adapter's prefix for the dialog text.
const incusSnapshotPrefix = "viewdi-pre-rebuild-"

func rebuildNames(targets []engine.Instance) string {
	names := make([]string, 0, len(targets))
	for i, in := range targets {
		if i == 4 {
			names = append(names, fmt.Sprintf("and %d more", len(targets)-i))
			break
		}
		names = append(names, textsafe.Clean(in.Name))
	}
	return strings.Join(names, ", ")
}

// rebuildSubmitted starts one rebuild job per instance once the name has
// been typed.
func (m Model) rebuildSubmitted(msg form.SubmittedMsg) (tea.Model, tea.Cmd) {
	req, ok := msg.Context.(rebuildRequest)
	if !ok || msg.Values["confirm"] != req.expect {
		return m, nil
	}
	if m.opts.ReadOnly {
		return m, toast.Show(theme.LevelWarn, "Read-only mode: rebuilding is disabled")
	}
	reqs := make([]jobs.Request, 0, len(req.targets))
	for _, in := range req.targets {
		opts := engine.RecreateOptions{Timeout: m.opts.StopTimeout, Source: req.image.Check.Ref}
		reqs = append(reqs, jobs.Request{Key: in.Key(), Name: in.Name, Recreate: &opts})
	}
	model, cmd := m.submit("rebuild", reqs)
	return model, tea.Batch(cmd, toast.Show(theme.LevelInfo, "Rebuilding "+plural(len(reqs), "instance", "instances")+"… progress in Activity (4)"))
}
