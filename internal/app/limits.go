package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/form"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// LimitsSource changes the limits of an instance; main backs it with the
// engines (the same value as Options.Images). Calls run inside commands.
type LimitsSource interface {
	UpdateLimits(ctx context.Context, e engine.ID, id string, l engine.Limits) error
}

const formLimits = "limits"

// limitsRequest is what the form and the confirmation carry.
type limitsRequest struct {
	Key    engine.Key
	Name   string
	Engine engine.ID
	ID     string
	Limits engine.Limits
	Text   string // "memory 512 MiB → 1 GiB · 2 CPUs"
}

// limitsConfirmedMsg is the answer to the confirmation.
type limitsConfirmedMsg struct{ req limitsRequest }

// limitsDoneMsg reports the result of the change.
type limitsDoneMsg struct {
	req limitsRequest
	err error
}

// limitsTimeout bounds the change.
const limitsTimeout = engine.DefaultTimeout

// raiseLimit opens the form that raises the memory or CPU limit of in. The
// memory field arrives with a suggestion: twice the limit, or twice what the
// machine uses when it has no limit.
func (m Model) raiseLimit(in engine.Instance) (tea.Model, tea.Cmd) {
	name := textsafe.Clean(in.Name)
	switch {
	case m.opts.ReadOnly:
		return m, toast.Show(theme.LevelWarn, "Read-only mode: changing limits is disabled")
	case in.State != engine.StateRunning:
		return m, toast.Show(theme.LevelInfo, name+" is not running: its limits apply when you change its config")
	}
	if _, ok := m.opts.Images.(LimitsSource); !ok {
		return m, toast.Show(theme.LevelInfo, "Changing limits needs a connected engine")
	}
	if caps := m.store.Snapshot().Engine(in.Engine).Info.Capabilities; caps.Restricted {
		return m, toast.Show(theme.LevelWarn, fmt.Sprintf("%s: %s", in.Engine.Label(), caps.Reason))
	}
	p, _ := m.store.Snapshot().Point(in.Key())
	var intro, memSuggest, cpuSuggest string
	switch {
	case p.MemLimit > 0:
		memSuggest = formatSize(p.MemLimit * 2)
		intro = fmt.Sprintf("%s uses %s of its %s memory limit", name, metrics.Bytes(p.MemUsed), metrics.Bytes(p.MemLimit))
	case p.MemUsed > 0:
		memSuggest = formatSize(roundUp(p.MemUsed*2, 64<<20))
		intro = fmt.Sprintf("%s uses %s and has no memory limit", name, metrics.Bytes(p.MemUsed))
	default:
		intro = name + " has no memory sample yet"
	}
	if p.CPULimit > 0 {
		cpuSuggest = strconv.FormatFloat(p.CPULimit*2, 'f', -1, 64)
		intro += fmt.Sprintf(" · CPU limit %g", p.CPULimit)
	}
	cpuHint := "a number of CPUs"
	if in.Engine == engine.Incus {
		cpuHint = "whole CPUs: Incus rounds up"
	}
	fields := []form.Field{
		{ID: "memory", Label: "Memory", Value: memSuggest, Placeholder: "e.g. 1GiB, empty to keep", Hint: "bytes, or a size such as 512MiB, 2GiB", Validate: validSize},
		{ID: "cpus", Label: "CPUs", Value: cpuSuggest, Placeholder: "e.g. 2, empty to keep", Hint: cpuHint, Validate: validCPUs},
	}
	id := in.ID
	f := form.New(m.keys, formLimits, "Raise a limit · "+name, intro+"\nThe change is live; nothing restarts.", "Review", fields).
		WithContext(limitsRequest{Key: in.Key(), Name: name, Engine: in.Engine, ID: id})
	return m, modal.Open(f)
}

// limitsSubmitted turns the form into a confirmation.
func (m Model) limitsSubmitted(msg form.SubmittedMsg) (tea.Model, tea.Cmd) {
	req, ok := msg.Context.(limitsRequest)
	if !ok {
		return m, nil
	}
	if m.opts.ReadOnly {
		return m, toast.Show(theme.LevelWarn, "Read-only mode: changing limits is disabled")
	}
	var parts []string
	if v := msg.Values["memory"]; v != "" {
		n, err := parseSize(v)
		if err != nil {
			return m, toast.Show(theme.LevelWarn, err.Error())
		}
		req.Limits.MemoryBytes = n
		parts = append(parts, "memory "+formatSize(n))
	}
	if v := msg.Values["cpus"]; v != "" {
		c, err := parseCPUs(v)
		if err != nil {
			return m, toast.Show(theme.LevelWarn, err.Error())
		}
		req.Limits.CPUs = c
		parts = append(parts, fmt.Sprintf("%g CPUs", c))
	}
	if req.Limits.IsZero() {
		return m, toast.Show(theme.LevelInfo, "No limit to change")
	}
	req.Text = strings.Join(parts, " · ")
	dialog := modal.NewConfirm(m.keys, "Change the limits of "+req.Name+"?", req.Text+"\n\nIt applies to the running machine right away.", "Apply", false, limitsConfirmedMsg{req: req})
	return m, modal.Open(dialog)
}

// applyLimits runs the change.
func (m Model) applyLimits(msg limitsConfirmedMsg) (tea.Model, tea.Cmd) {
	if m.opts.ReadOnly {
		return m, toast.Show(theme.LevelWarn, "Read-only mode: changing limits is disabled")
	}
	src, ok := m.opts.Images.(LimitsSource)
	if !ok {
		return m, toast.Show(theme.LevelInfo, "Changing limits needs a connected engine")
	}
	req := msg.req
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.opts.Context, limitsTimeout)
		defer cancel()
		return limitsDoneMsg{req: req, err: src.UpdateLimits(ctx, req.Engine, req.ID, req.Limits)}
	}
}

func (m Model) limitsDone(msg limitsDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, toast.Show(theme.LevelWarn, "Could not change the limits of "+msg.req.Name+": "+textsafe.Clean(msg.err.Error()))
	}
	return m, toast.Show(theme.LevelOK, msg.req.Name+": "+msg.req.Text)
}

// parseSize reads "512", "512MiB", "1.5g", "2 GiB" as bytes; a bare number
// is bytes. Units are binary (k, m, g, t, with an optional i and b).
func parseSize(s string) (uint64, error) {
	t := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	t = strings.TrimSuffix(t, "b")
	t = strings.TrimSuffix(t, "i")
	mult := float64(1)
	if t != "" {
		switch t[len(t)-1] {
		case 'k':
			mult = 1 << 10
		case 'm':
			mult = 1 << 20
		case 'g':
			mult = 1 << 30
		case 't':
			mult = 1 << 40
		}
		if mult != 1 {
			t = t[:len(t)-1]
		}
	}
	v, err := strconv.ParseFloat(t, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0, fmt.Errorf("%q is not a size (try 512MiB or 2GiB)", textsafe.Clean(s))
	}
	n := v * mult
	if n < 4<<20 {
		return 0, errors.New("a memory limit under 4 MiB would not start anything")
	}
	if n > 1<<50 {
		return 0, errors.New("that memory limit is not realistic")
	}
	return uint64(n), nil
}

func validSize(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	_, err := parseSize(s)
	return err
}

func parseCPUs(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || v <= 0 || v > 1024 {
		return 0, fmt.Errorf("%q is not a number of CPUs", textsafe.Clean(s))
	}
	return v, nil
}

func validCPUs(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	_, err := parseCPUs(s)
	return err
}

// formatSize writes bytes as the largest exact binary unit: 1GiB, 1536MiB.
func formatSize(n uint64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return fmt.Sprintf("%dGiB", n>>30)
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%dMiB", n>>20)
	case n >= 1<<20:
		return fmt.Sprintf("%dMiB", roundUp(n, 1<<20)>>20)
	}
	return strconv.FormatUint(n, 10)
}

func roundUp(n, to uint64) uint64 { return (n + to - 1) / to * to }
