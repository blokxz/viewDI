package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Class
	}{
		{"eacces", &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.EACCES)}, ClassPermission},
		{"enoent", &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ENOENT)}, ClassMissing},
		{"refused", &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}, ClassTransient},
		{"timeout", fmt.Errorf("ping: %w", context.DeadlineExceeded), ClassTransient},
		{"canceled", fmt.Errorf("list: %w", context.Canceled), ClassCanceled},
		{"eof", io.EOF, ClassTransient},
		{"explicit", Errorf(ClassPermission, "info", errors.New("403 Forbidden")), ClassPermission},
		{"not found", NotFound("inspect", "container abc"), ClassNotFound},
		{"wrapped explicit", fmt.Errorf("outer: %w", Errorf(ClassMissing, "x", io.EOF)), ClassMissing},
	}
	for _, c := range cases {
		if got := Classify(c.err); got != c.want {
			t.Errorf("%s: Classify = %v, want %v", c.name, got, c.want)
		}
	}
	if !IsTimeout(Wrap("ping", context.DeadlineExceeded)) || IsTimeout(io.EOF) {
		t.Error("IsTimeout")
	}
	if !IsEOF(Wrap("events", io.ErrUnexpectedEOF)) {
		t.Error("IsEOF")
	}
	if !errors.Is(NotFound("inspect", "x"), ErrNotFound) {
		t.Error("NotFound must wrap ErrNotFound")
	}
}

func TestFeed(t *testing.T) {
	f := NewFeed(1)
	ctx, cancel := context.WithCancel(context.Background())
	if !f.Send(ctx, Event{Name: "a"}) {
		t.Fatal("send")
	}
	cancel()
	if f.Send(ctx, Event{Name: "b"}) {
		t.Fatal("send on a full feed must give up when ctx ends")
	}
	f.Close(io.EOF)
	f.Close(nil) // second close is a no-op
	var got []string
	for ev := range f.C() {
		got = append(got, ev.Name)
	}
	if len(got) != 1 || got[0] != "a" || !errors.Is(f.Err(), io.EOF) {
		t.Fatalf("got %v err %v", got, f.Err())
	}
}

func TestKeyAndUptime(t *testing.T) {
	i := Instance{ID: "default/web", Engine: Incus, State: StateRunning, Started: time.Unix(100, 0)}
	if i.Key() != "incus/default/web" || i.Key().Engine() != Incus {
		t.Fatalf("key %q", i.Key())
	}
	if got := i.Uptime(time.Unix(160, 0)); got != time.Minute {
		t.Fatalf("uptime %v", got)
	}
	i.State = StateStopped
	if i.Uptime(time.Unix(160, 0)) != 0 {
		t.Fatal("stopped instances have no uptime")
	}
}

func TestWithDefaultTimeout(t *testing.T) {
	ctx, cancel := WithDefaultTimeout(context.Background())
	defer cancel()
	if d, ok := ctx.Deadline(); !ok || time.Until(d) > DefaultTimeout {
		t.Fatal("default deadline missing")
	}
	parent, pc := context.WithTimeout(context.Background(), time.Hour)
	defer pc()
	ctx2, c2 := WithDefaultTimeout(parent)
	defer c2()
	if d, _ := ctx2.Deadline(); time.Until(d) < 50*time.Minute {
		t.Fatal("existing deadline must be kept")
	}
}
