package clock

import (
	"context"
	"errors"
	"testing"
	"time"
)

var epoch = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func TestFakeAfterFiresInOrder(t *testing.T) {
	f := NewFake(epoch)
	a, b := f.After(2*time.Second), f.After(time.Second)
	f.Advance(1500 * time.Millisecond)
	select {
	case got := <-b:
		if !got.Equal(epoch.Add(time.Second)) {
			t.Fatalf("b fired at %v", got)
		}
	default:
		t.Fatal("b did not fire")
	}
	select {
	case <-a:
		t.Fatal("a fired early")
	default:
	}
	f.Advance(time.Second)
	<-a
	if !f.Now().Equal(epoch.Add(2500 * time.Millisecond)) {
		t.Fatalf("now = %v", f.Now())
	}
}

func TestFakeAfterFuncStop(t *testing.T) {
	f := NewFake(epoch)
	ran := make(chan struct{}, 1)
	tm := f.AfterFunc(time.Second, func() { ran <- struct{}{} })
	if !tm.Stop() || tm.Stop() {
		t.Fatal("Stop must succeed once")
	}
	f.Advance(time.Hour)
	select {
	case <-ran:
		t.Fatal("stopped timer ran")
	default:
	}
	if f.Pending() != 0 {
		t.Fatalf("pending = %d", f.Pending())
	}
}

func TestWithTimeoutFake(t *testing.T) {
	f := NewFake(epoch)
	ctx, cancel := WithTimeout(context.Background(), f, 5*time.Second)
	defer cancel()
	f.Advance(4 * time.Second)
	if ctx.Err() != nil {
		t.Fatal("expired early")
	}
	f.Advance(time.Second)
	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("err = %v", ctx.Err())
	}
}

func TestWithTimeoutCancel(t *testing.T) {
	f := NewFake(epoch)
	ctx, cancel := WithTimeout(context.Background(), f, time.Second)
	cancel()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("err = %v", ctx.Err())
	}
	if f.Pending() != 0 {
		t.Fatal("cancel must stop the timer")
	}
}

func TestWithTimeoutReal(t *testing.T) {
	ctx, cancel := WithTimeout(context.Background(), Real{}, time.Hour)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("real clock must set a deadline")
	}
}

func TestEventually(t *testing.T) {
	n := 0
	if !Eventually(time.Second, func() bool { n++; return n == 3 }) {
		t.Fatal("condition that becomes true reported false")
	}
	if Eventually(10*time.Millisecond, func() bool { return false }) {
		t.Fatal("false condition reported true")
	}
}
