// Package notifier sends opt-in desktop notifications about new updates
// (docs/01-research.md §3.4, docs/04-development-plan.md Stage 6b task 10):
// org.freedesktop.Notifications.Notify on the session bus, the notify-send
// command when there is no bus, and one debug-log line when neither works.
//
// It only shows a message: it never runs anything the user did not ask for.
package notifier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/blokxz/viewDI/internal/textsafe"
)

// Timeout bounds one notification, bus or command.
const Timeout = 5 * time.Second

// Message caps: a desktop notification is a glance, not a report.
const (
	maxSummary = 120
	maxBody    = 1000
)

// Notifier shows one notification; Desktop implements it.
type Notifier interface {
	Notify(ctx context.Context, summary, body string, urgent bool) error
}

// Desktop delivers through D-Bus, then notify-send.
type Desktop struct {
	// Bus sends over D-Bus; nil uses the session bus.
	Bus func(ctx context.Context, summary, body string, urgent bool) error
	// LookPath and Run find and run notify-send; nil uses os/exec.
	LookPath func(string) (string, error)
	Run      func(ctx context.Context, name string, args ...string) error
	Logger   *slog.Logger

	once sync.Once // the "no way to notify" log line
}

// ErrNoNotifier means neither the bus nor notify-send could deliver.
var ErrNoNotifier = errors.New("no desktop notification service (D-Bus) and no notify-send")

// Notify implements Notifier. Its text is sanitised and capped. When
// neither way works it logs one debug line, once, and returns
// ErrNoNotifier.
func (d *Desktop) Notify(ctx context.Context, summary, body string, urgent bool) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	summary = capText(textsafe.Clean(summary), maxSummary)
	body = capText(cleanLines(body), maxBody)

	bus := d.Bus
	if bus == nil {
		bus = SessionBus
	}
	busErr := bus(ctx, summary, escapeMarkup(body), urgent)
	if busErr == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("notify: %w", busErr)
	}
	look, run := d.LookPath, d.Run
	if look == nil {
		look = exec.LookPath
	}
	if run == nil {
		run = runQuiet
	}
	path, err := look("notify-send")
	if err == nil {
		urgency := "normal"
		if urgent {
			urgency = "critical"
		}
		// notify-send hands the body to the same service: no markup either.
		err = run(ctx, path, "--app-name=viewDI", "--urgency="+urgency, "--", summary, escapeMarkup(body))
		if err == nil {
			return nil
		}
		return fmt.Errorf("notify: D-Bus: %v; notify-send: %w", busErr, err)
	}
	d.once.Do(func() {
		if d.Logger != nil {
			d.Logger.Debug("desktop notifications unavailable", "dbus", busErr.Error(), "notify_send", err.Error())
		}
	})
	return ErrNoNotifier
}

// SessionBus calls org.freedesktop.Notifications.Notify on the session bus
// (DBUS_SESSION_BUS_ADDRESS), with a private connection closed afterwards:
// notifications are rare, a connection held open is not worth it.
func SessionBus(ctx context.Context, summary, body string, urgent bool) error {
	conn, err := dbus.ConnectSessionBus(dbus.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("session bus: %w", err)
	}
	defer func() { _ = conn.Close() }()
	urgency := byte(1) // normal
	if urgent {
		urgency = 2 // critical
	}
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.CallWithContext(ctx, "org.freedesktop.Notifications.Notify", 0,
		"viewDI", uint32(0), "", summary, body, []string{},
		map[string]dbus.Variant{"urgency": dbus.MakeVariant(urgency)}, int32(-1))
	if call.Err != nil {
		return fmt.Errorf("Notify: %w", call.Err)
	}
	return nil
}

// runQuiet runs a command with its output discarded: it would corrupt the
// TUI.
func runQuiet(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

// escapeMarkup escapes the body markup the specification allows (a
// subset of HTML), so an image name is shown as written.
func escapeMarkup(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func capText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// cleanLines sanitises each line of s, keeping the line breaks.
func cleanLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = textsafe.Clean(l)
	}
	return strings.Join(lines, "\n")
}
