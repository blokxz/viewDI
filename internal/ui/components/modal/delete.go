package modal

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// DeleteConfirmedMsg carries a confirmed delete.
type DeleteConfirmedMsg struct {
	Targets []engine.Instance
	Options engine.DeleteOptions
}

type deleteOption struct {
	label string
	on    bool
	set   func(*engine.DeleteOptions, bool)
}

// Delete is the delete dialog. Focus starts on Cancel (or on the name
// field when typing the name is required), every option starts unticked,
// and the Delete button stays disabled until the typed text matches.
type Delete struct {
	keys    *keys.Map
	targets []engine.Instance
	expect  string // text to type; "" when not required
	typed   string
	opts    []deleteOption
	focus   int // 0..: [input], options…, Cancel, Delete
}

// NeedsTyping reports whether deleting in requires typing its name: it is
// running or keeps data in volumes.
func NeedsTyping(in engine.Instance) bool {
	if in.State == engine.StateRunning || in.State == engine.StatePaused {
		return true
	}
	for _, m := range in.Mounts {
		if m.Type == "volume" || m.Type == "disk" {
			return true
		}
	}
	return false
}

// NewDelete builds the dialog for targets (at least one).
func NewDelete(km *keys.Map, targets []engine.Instance) *Delete {
	d := &Delete{keys: km, targets: targets}
	need, docker, incus := false, false, false
	for _, in := range targets {
		need = need || NeedsTyping(in)
		docker = docker || in.Engine == engine.Docker
		incus = incus || in.Engine == engine.Incus
	}
	if need {
		d.expect = targets[0].Name
		if len(targets) > 1 {
			d.expect = strconv.Itoa(len(targets))
		}
	}
	if docker {
		d.opts = append(d.opts,
			deleteOption{label: "Force: kill running containers", set: func(o *engine.DeleteOptions, v bool) { o.Force = v }},
			deleteOption{label: "Also remove anonymous volumes (named volumes are kept)", set: func(o *engine.DeleteOptions, v bool) { o.Volumes = v }},
		)
	}
	if incus {
		d.opts = append(d.opts, deleteOption{label: "Stop running Incus instances first", set: func(o *engine.DeleteOptions, v bool) { o.Stop = v }})
	}
	d.focus = d.cancelIndex()
	if d.expect != "" {
		d.focus = 0
	}
	return d
}

func (d *Delete) inputIndex() int {
	if d.expect == "" {
		return -1
	}
	return 0
}

func (d *Delete) firstOption() int {
	if d.expect == "" {
		return 0
	}
	return 1
}

func (d *Delete) cancelIndex() int { return d.firstOption() + len(d.opts) }
func (d *Delete) fields() int      { return d.cancelIndex() + 2 }

// Ready reports whether the Delete button is enabled.
func (d *Delete) Ready() bool { return d.expect == "" || d.typed == d.expect }

// Options returns the ticked options.
func (d *Delete) Options() engine.DeleteOptions {
	var o engine.DeleteOptions
	for _, opt := range d.opts {
		opt.set(&o, opt.on)
	}
	return o
}

// Scope implements Modal.
func (d *Delete) Scope() keys.Scope { return keys.Delete }

// Update implements Modal.
func (d *Delete) Update(msg tea.Msg) (Modal, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return d, nil
	}
	switch d.keys.Action(kp, keys.Delete) {
	case "next":
		d.focus = (d.focus + 1) % d.fields()
		return d, nil
	case "prev":
		d.focus = (d.focus + d.fields() - 1) % d.fields()
		return d, nil
	case keys.ActCancel:
		return d, Close
	case keys.ActActivate:
		switch {
		case d.focus == d.cancelIndex():
			return d, Close
		case d.focus == d.cancelIndex()+1, d.focus == d.inputIndex():
			return d, d.confirm()
		}
		return d, nil
	case "toggle":
		if i := d.focus - d.firstOption(); i >= 0 && i < len(d.opts) {
			d.opts[i].on = !d.opts[i].on
			return d, nil
		}
	}
	if d.focus == d.inputIndex() {
		switch {
		case kp.String() == "backspace" && d.typed != "":
			_, n := utf8.DecodeLastRuneInString(d.typed)
			d.typed = d.typed[:len(d.typed)-n]
		case kp.Text != "" && utf8.RuneCountInString(d.typed) < 128 && !strings.ContainsFunc(kp.Text, func(r rune) bool { return r < 0x20 || r == 0x7f }):
			d.typed += kp.Text
		}
	}
	return d, nil
}

func (d *Delete) confirm() tea.Cmd {
	if !d.Ready() {
		return nil
	}
	msg := DeleteConfirmedMsg{Targets: d.targets, Options: d.Options()}
	return tea.Sequence(Close, func() tea.Msg { return msg })
}

// View implements Modal.
func (d *Delete) View(s *theme.Styles, width, _ int) string {
	var b strings.Builder
	if len(d.targets) == 1 {
		in := d.targets[0]
		b.WriteString(s.Text.Render(fmt.Sprintf("%s %s (%s, %s)", in.Engine.Label(), in.Name, in.Kind, in.State)))
	} else {
		b.WriteString(s.Text.Render(fmt.Sprintf("%d instances:", len(d.targets))))
		for i, in := range d.targets {
			if i == 8 {
				b.WriteString("\n" + s.Muted.Render(fmt.Sprintf("  … and %d more", len(d.targets)-i)))
				break
			}
			b.WriteString("\n" + s.Muted.Render("  • ") + s.Text.Render(in.Name) + s.Muted.Render(" ("+in.Engine.Short()+")"))
		}
	}
	b.WriteString("\n" + s.LevelText(theme.LevelWarn).Render("This cannot be undone."))
	if d.expect != "" {
		what := "the name"
		if len(d.targets) > 1 {
			what = "the number of instances"
		}
		field := d.typed
		cursor := " "
		if d.focus == d.inputIndex() {
			cursor = "_"
		}
		box := s.Text.Render(field + cursor)
		if d.focus == d.inputIndex() {
			box = s.Selected.Render(field + cursor)
		}
		b.WriteString("\n\n" + s.Muted.Render("Type "+what+" ") + s.Bold.Render(d.expect) + s.Muted.Render(" to confirm: ") + box)
	}
	if len(d.opts) > 0 {
		b.WriteString("\n")
	}
	for i, opt := range d.opts {
		box := "[ ] "
		if opt.on {
			box = "[x] "
		}
		line := box + opt.label
		if d.focus == d.firstOption()+i {
			line = s.Selected.Render(line)
		} else {
			line = s.Text.Render(line)
		}
		b.WriteString("\n" + line)
	}
	cancel, del := s.Muted.Padding(0, 1), s.ButtonDanger.Faint(true)
	if d.focus == d.cancelIndex() {
		cancel = s.Selected.Padding(0, 1)
	}
	if d.focus == d.cancelIndex()+1 && d.Ready() {
		del = s.ButtonDanger
	}
	label := "Delete"
	if !d.Ready() {
		label = "Delete (type to enable)"
	}
	b.WriteString("\n\n" + lipgloss.JoinHorizontal(lipgloss.Center, cancel.Render("Cancel"), "  ", del.Render(label)))
	b.WriteString("\n\n" + s.Muted.Render("tab/↑↓ move · space tick · enter press · esc cancel"))
	title := "Delete " + d.targets[0].Name + "?"
	if len(d.targets) > 1 {
		title = fmt.Sprintf("Delete %d instances?", len(d.targets))
	}
	return Frame(s, title, b.String(), width)
}
