package app

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/jobs"
	"github.com/blokxz/viewDI/internal/store"
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
	b.WriteString("Running instances are stopped for the rebuild and started again.\n")
	b.WriteString("Incus refuses an instance that has snapshots: those are reported\n")
	b.WriteString("before anything changes.")
	if old := olderCopies(snap, targets); len(old) > 0 {
		b.WriteString("\n\nCopies from earlier rebuilds still exist (incus delete <name>):\n")
		for i, n := range old {
			if i == 4 {
				fmt.Fprintf(&b, "  … and %d more\n", len(old)-i)
				break
			}
			b.WriteString("  " + textsafe.Clean(n) + "\n")
		}
	}
	label := "Type " + req.expect + " to confirm"
	if len(targets) > 1 {
		label = "Type " + req.expect + " (the number of instances) to confirm"
	}
	expect := req.expect
	fields := []form.Field{
		{
			ID: "confirm", Label: label, Required: true, Placeholder: req.expect,
			Validate: func(v string) error {
				if v != expect {
					return errors.New("type " + expect + " to confirm")
				}
				return nil
			},
		},
		{
			ID: "copy", Label: "Keep a copy", Kind: form.Check, On: true,
			Hint: "copies the stopped instance first (same disk space again) and puts it back if the rebuild fails",
		},
	}
	title := "Rebuild " + rebuildNames(targets)
	f := form.New(m.keys, formRebuild, title, b.String(), "Rebuild", fields).WithContext(req)
	return m, tea.Batch(append(cmds, modal.Open(f))...)
}

// olderCopies lists the copies earlier rebuilds of targets left behind: the
// instances of the same engine and project named after a target, the infix
// and a date.
func olderCopies(snap *store.Snapshot, targets []engine.Instance) []string {
	var out []string
	for _, t := range targets {
		prefix := t.Name + incusCopyInfix
		for _, in := range snap.Instances {
			if in.Engine == t.Engine && in.Project == t.Project && strings.HasPrefix(in.Name, prefix) {
				out = append(out, in.Name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// incusCopyInfix mirrors the adapter's infix in the names of the copies.
const incusCopyInfix = "-pre-rebuild-"

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
		opts := engine.RecreateOptions{Timeout: m.opts.StopTimeout, Source: req.image.Check.Ref, Copy: msg.Values.Bool("copy")}
		reqs = append(reqs, jobs.Request{Key: in.Key(), Name: in.Name, Recreate: &opts})
	}
	model, cmd := m.submit("rebuild", reqs)
	return model, tea.Batch(cmd, toast.Show(theme.LevelInfo, "Rebuilding "+plural(len(reqs), "instance", "instances")+"… progress in Activity (4)"))
}
