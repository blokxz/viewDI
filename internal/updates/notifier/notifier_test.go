package notifier

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var errNoBus = errors.New("no bus")

func TestBusFirst(t *testing.T) {
	var got []string
	ran := false
	d := &Desktop{
		Bus: func(_ context.Context, summary, body string, urgent bool) error {
			got = append(got, summary, body)
			if !urgent {
				t.Error("urgent lost")
			}
			return nil
		},
		Run: func(context.Context, string, ...string) error { ran = true; return nil },
	}
	if err := d.Notify(context.Background(), "viewDI: \x1b[31mIncus 6.0.6\x1b[0m", "a <b> & c\nline\x07 two", true); err != nil {
		t.Fatal(err)
	}
	if ran || len(got) != 2 || got[0] != "viewDI: Incus 6.0.6" || got[1] != "a &lt;b&gt; &amp; c\nline two" {
		t.Fatalf("sent %q, notify-send ran %v", got, ran)
	}
}

func TestNotifySendFallback(t *testing.T) {
	var args []string
	d := &Desktop{
		Bus:      func(context.Context, string, string, bool) error { return errNoBus },
		LookPath: func(string) (string, error) { return "/usr/bin/notify-send", nil },
		Run: func(_ context.Context, name string, a ...string) error {
			args = append([]string{name}, a...)
			return nil
		},
	}
	if err := d.Notify(context.Background(), "-rf summary", "body", false); err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/notify-send", "--app-name=viewDI", "--urgency=normal", "--", "-rf summary", "body"}
	if strings.Join(args, "|") != strings.Join(want, "|") {
		t.Fatalf("ran %q, want %q", args, want)
	}

	d.Run = func(context.Context, string, ...string) error { return errors.New("exit status 1") }
	if err := d.Notify(context.Background(), "s", "b", true); err == nil || !strings.Contains(err.Error(), "notify-send") {
		t.Fatalf("failed notify-send: %v", err)
	}
}

func TestNeitherLogsOnce(t *testing.T) {
	var buf strings.Builder
	var mu sync.Mutex
	d := &Desktop{
		Bus:      func(context.Context, string, string, bool) error { return errNoBus },
		LookPath: func(string) (string, error) { return "", exec.ErrNotFound },
		Logger:   slog.New(slog.NewTextHandler(lockedWriter{&mu, &buf}, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	for range 3 {
		if err := d.Notify(context.Background(), "s", "b", false); !errors.Is(err, ErrNoNotifier) {
			t.Fatalf("err %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if n := strings.Count(buf.String(), "desktop notifications unavailable"); n != 1 {
		t.Fatalf("%d log lines:\n%s", n, buf.String())
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	b  *strings.Builder
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func TestCaps(t *testing.T) {
	var summary, body string
	d := &Desktop{Bus: func(_ context.Context, s, b string, _ bool) error { summary, body = s, b; return nil }}
	_ = d.Notify(context.Background(), strings.Repeat("s", 500), strings.Repeat("b", 5000), false)
	if n := len([]rune(summary)); n != maxSummary {
		t.Errorf("summary of %d runes", n)
	}
	if n := len([]rune(body)); n != maxBody {
		t.Errorf("body of %d runes", n)
	}
}

func TestCanceledSkipsFallback(t *testing.T) {
	ran := false
	d := &Desktop{
		Bus: func(ctx context.Context, _, _ string, _ bool) error { <-ctx.Done(); return ctx.Err() },
		Run: func(context.Context, string, ...string) error { ran = true; return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.Notify(ctx, "s", "b", false); err == nil || ran {
		t.Fatalf("err %v, notify-send ran %v", err, ran)
	}
}

// notifications is a stand-in org.freedesktop.Notifications service.
type notifications struct {
	mu   sync.Mutex
	got  []string
	hint dbus.Variant
}

func (n *notifications) Notify(app string, _ uint32, _, summary, body string, _ []string, hints map[string]dbus.Variant, _ int32) (uint32, *dbus.Error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.got = append(n.got, app, summary, body)
	n.hint = hints["urgency"]
	return 7, nil
}

// TestSessionBus runs a private dbus-daemon with a stand-in service.
func TestSessionBus(t *testing.T) {
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("no dbus-daemon")
	}
	cmd := exec.Command(daemon, "--session", "--nofork", "--print-address")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Skip("dbus-daemon:", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	addr, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", strings.TrimSpace(addr))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := SessionBus(ctx, "s", "b", false); err == nil {
		t.Fatal("Notify succeeded without a service")
	}

	srv, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	svc := &notifications{}
	if err := srv.Export(svc, "/org/freedesktop/Notifications", "org.freedesktop.Notifications"); err != nil {
		t.Fatal(err)
	}
	if reply, err := srv.RequestName("org.freedesktop.Notifications", dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("RequestName: %v, %v", reply, err)
	}
	d := &Desktop{Run: func(context.Context, string, ...string) error { t.Error("notify-send ran"); return nil }}
	if err := d.Notify(ctx, "viewDI: 1 update", "Incus <6.0.6>", true); err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if strings.Join(svc.got, "|") != "viewDI|viewDI: 1 update|Incus &lt;6.0.6&gt;" || svc.hint.Value() != byte(2) {
		t.Fatalf("service got %q, urgency %v", svc.got, svc.hint)
	}
}
