package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/jobs"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/theme"
	updatesview "github.com/blokxz/viewDI/internal/ui/views/updates"
	"github.com/blokxz/viewDI/internal/updates"
)

// recreatePreviewedMsg moves a recreate from its diff preview to the
// confirm modal; recreateConfirmedMsg starts it.
type (
	recreatePreviewedMsg struct{ req recreateRequest }
	recreateConfirmedMsg struct{ req recreateRequest }
)

// recreateRequest is what the user asked to recreate.
type recreateRequest struct {
	image   updates.ImageUpdate
	targets []engine.Instance
	pull    bool
}

// imageAction answers p, f and c in the Updates view.
func (m Model) imageAction(msg updatesview.ImageActionMsg) (tea.Model, tea.Cmd) {
	img := msg.Image
	if img == nil {
		return m, toast.Show(theme.LevelInfo, "p, f and c act on image rows (engines are upgraded with the hint shown below)")
	}
	ref := textsafe.Clean(img.Check.Ref)
	e := img.Check.Engine
	if msg.Action == updatesview.ActRecreate && e == engine.Incus {
		return m, toast.Show(theme.LevelWarn, rebuildText(m.store.Snapshot(), *img))
	}
	if m.opts.ReadOnly {
		return m, toast.Show(theme.LevelWarn, "Read-only mode: pulling, refreshing and recreating are disabled")
	}
	if m.opts.Jobs == nil {
		return m, toast.Show(theme.LevelWarn, capital(msg.Action)+" needs a connected engine")
	}
	if ids, why := m.writable(); !slices.Contains(ids, e) {
		text := e.Label() + " cannot " + msg.Action + " right now"
		for _, w := range why {
			if strings.HasPrefix(w, e.Label()) {
				text = w
			}
		}
		return m, toast.Show(theme.LevelWarn, text)
	}
	chk := img.Check
	switch msg.Action {
	case updatesview.ActPull:
		switch {
		case e != engine.Docker:
			return m, toast.Show(theme.LevelInfo, "Incus images are refreshed, not pulled: press f")
		case img.Pulled:
			return m, toast.Show(theme.LevelInfo, ref+" is already pulled: press c to recreate the containers on it")
		}
		model, cmd := m.submit("pull", []jobs.Request{{Key: engine.MakeKey(e, ""), Name: img.Check.Ref, Pull: &chk}})
		return model, tea.Batch(cmd, toast.Show(theme.LevelInfo, "Pulling "+ref+"… progress in Jobs (5)"))
	case updatesview.ActRefresh:
		if e != engine.Incus {
			return m, toast.Show(theme.LevelInfo, "Docker images are pulled, not refreshed: press p")
		}
		model, cmd := m.submit("refresh", []jobs.Request{{Key: engine.MakeKey(e, ""), Name: img.Check.Ref, Refresh: &chk}})
		return model, tea.Batch(cmd, toast.Show(theme.LevelInfo, "Refreshing "+ref+"… progress in Jobs (5)"))
	case updatesview.ActRecreate:
		return m.previewRecreate(*img)
	}
	return m, nil
}

// previewRecreate opens the diff preview of a recreate: standalone
// containers only, Compose-managed ones get their compose command.
func (m Model) previewRecreate(img updates.ImageUpdate) (tea.Model, tea.Cmd) {
	snap := m.store.Snapshot()
	var targets []engine.Instance
	var hints, skipped []string
	for _, k := range img.Instances {
		in, ok := snap.Lookup(k)
		if !ok {
			continue
		}
		if _, busy := snap.Pending(k); busy {
			skipped = append(skipped, in.Name+" (busy)")
			continue
		}
		if h := engine.ComposeHint(in); h != "" {
			if !slices.Contains(hints, h) {
				hints = append(hints, h)
			}
			continue
		}
		targets = append(targets, in)
	}
	var cmds []tea.Cmd
	if len(hints) > 0 {
		cmds = append(cmds, toast.Show(theme.LevelInfo, "Compose manages "+plural(len(hints), "service", "services")+": "+textsafe.Clean(strings.Join(hints, "; "))))
	}
	if len(skipped) > 0 {
		cmds = append(cmds, toast.Show(theme.LevelWarn, "Skipped "+textsafe.Clean(strings.Join(skipped, ", "))))
	}
	if len(targets) == 0 {
		if len(cmds) == 0 {
			cmds = append(cmds, toast.Show(theme.LevelInfo, "No container to recreate for "+textsafe.Clean(img.Check.Ref)))
		}
		return m, tea.Batch(cmds...)
	}
	req := recreateRequest{image: img, targets: targets, pull: !img.Pulled}
	title := "Recreate " + plural(len(targets), "container", "containers") + " on " + textsafe.Clean(img.Check.Ref)
	dialog := modal.NewConfirm(m.keys, title, recreateDiff(req), "Continue", false, recreatePreviewedMsg{req: req})
	return m, tea.Batch(append(cmds, modal.Open(dialog))...)
}

// confirmRecreate is the second step: the destructive confirm.
func (m Model) confirmRecreate(req recreateRequest) (tea.Model, tea.Cmd) {
	names := make([]string, 0, len(req.targets))
	for _, in := range req.targets {
		names = append(names, "• "+textsafe.Clean(in.Name))
	}
	body := strings.Join(names, "\n") + "\n\n" +
		"Each one is stopped and renamed <name>" + engine.OldSuffix + ", the new one\n" +
		"is created and started, and the old one is removed only once\n" +
		"the new one runs. On failure the old one gets its name back\n" +
		"and is started again."
	title := "Recreate " + plural(len(req.targets), "container", "containers") + "?"
	return m, modal.Open(modal.NewConfirm(m.keys, title, body, "Recreate", true, recreateConfirmedMsg{req: req}))
}

// submitRecreate starts one recreate job per container.
func (m Model) submitRecreate(req recreateRequest) (tea.Model, tea.Cmd) {
	if m.opts.ReadOnly {
		return m, nil
	}
	reqs := make([]jobs.Request, 0, len(req.targets))
	for _, in := range req.targets {
		opts := engine.RecreateOptions{Timeout: m.opts.StopTimeout, Pull: req.pull}
		reqs = append(reqs, jobs.Request{Key: in.Key(), Name: in.Name, Recreate: &opts})
	}
	model, cmd := m.submit("recreate", reqs)
	return model, tea.Batch(cmd, toast.Show(theme.LevelInfo, "Recreating "+plural(len(reqs), "container", "containers")+"… progress in Jobs (5)"))
}

// recreateDiff is the preview body: the image change, then what each
// container keeps.
func recreateDiff(req recreateRequest) string {
	img := req.image
	var b strings.Builder
	if img.Pulled {
		fmt.Fprintf(&b, "image    %s (already pulled: %s)\n", textsafe.Clean(img.Check.Ref), shortDigest(img.Available))
	} else {
		fmt.Fprintf(&b, "image    %s  %s → %s (pulled first)\n", textsafe.Clean(img.Check.Ref), shortDigest(img.Current), shortDigest(img.Available))
	}
	for i, in := range req.targets {
		if i == 6 {
			fmt.Fprintf(&b, "\n… and %d more", len(req.targets)-i)
			break
		}
		b.WriteString("\n" + textsafe.Clean(in.Name) + "  kept:\n")
		for _, kv := range keptSettings(in) {
			fmt.Fprintf(&b, "  %-9s%s\n", kv[0], textsafe.Clean(kv[1]))
		}
	}
	b.WriteString("\nThe new build's defaults (CMD, ENV, labels) apply where\nthe container never set its own.")
	return strings.TrimRight(b.String(), "\n")
}

// keptSettings lists what a recreate carries over, for the preview.
func keptSettings(in engine.Instance) [][2]string {
	none := func(s string) string {
		if s == "" {
			return "none"
		}
		return s
	}
	var ports, mounts, nets []string
	for _, p := range in.Ports {
		s := strconv.Itoa(int(p.ContainerPort)) + "/" + p.Protocol
		if p.HostPort != 0 {
			s = strconv.Itoa(int(p.HostPort)) + "→" + s
		}
		ports = append(ports, s)
	}
	for _, mt := range in.Mounts {
		s := mt.Source + ":" + mt.Destination
		if mt.ReadOnly {
			s += ":ro"
		}
		mounts = append(mounts, s)
	}
	for _, a := range in.Networks {
		nets = append(nets, a.Network)
	}
	restart := ""
	if x, ok := in.Extra.(*engine.DockerExtra); ok {
		restart = x.RestartPolicy
	}
	return [][2]string{
		{"name", in.Name},
		{"env", plural(len(in.Env), "variable", "variables") + " (values not shown)"},
		{"ports", none(strings.Join(ports, ", "))},
		{"mounts", none(strings.Join(mounts, ", "))},
		{"networks", none(strings.Join(nets, ", "))},
		{"restart", none(restart)},
		{"labels", plural(len(in.Labels), "label", "labels")},
	}
}

// rebuildText is the toast for c on an Incus image: the rebuild command
// and its warning, never run by viewDI.
func rebuildText(snap *store.Snapshot, img updates.ImageUpdate) string {
	name := "<name>"
	in := engine.Instance{Name: name}
	if len(img.Instances) > 0 {
		if found, ok := snap.Lookup(img.Instances[0]); ok {
			in = found
		}
	}
	cmd, warning := engine.RebuildHint(in, img.Check.Ref)
	return "viewDI does not rebuild Incus instances (" + warning + "). To do it yourself: " + textsafe.Clean(cmd)
}

// recheckImages asks the checker for an image check after a pull,
// refresh or recreate.
func (m Model) recheckImages() tea.Cmd {
	u := m.opts.Updates
	if u == nil {
		return nil
	}
	return func() tea.Msg { u.CheckImagesNow(); return nil }
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// shortDigest abbreviates a digest or fingerprint.
func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if d == "" {
		return "?"
	}
	return textsafe.Clean(d[:min(12, len(d))])
}
