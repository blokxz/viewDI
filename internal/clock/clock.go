// Package clock abstracts time so that supervisors, scenarios and timeouts can
// run on a fake clock in tests (no time.Sleep in tests).
package clock

import (
	"context"
	"time"
)

// Clock is the time source injected everywhere time matters.
type Clock interface {
	Now() time.Time
	// After returns a channel that receives the time once d has elapsed.
	After(d time.Duration) <-chan time.Time
	// AfterFunc calls f in its own goroutine once d has elapsed.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a pending AfterFunc call.
type Timer interface {
	// Stop prevents the call; it reports false if it already ran or was stopped.
	Stop() bool
}

// Real is the wall clock.
type Real struct{}

// Now implements Clock.
func (Real) Now() time.Time { return time.Now() }

// After implements Clock.
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// AfterFunc implements Clock.
func (Real) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// WithTimeout is context.WithTimeout driven by c, so fake clocks can expire
// request deadlines deterministically. With the real clock it is exactly
// context.WithTimeout.
func WithTimeout(parent context.Context, c Clock, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := c.(Real); ok || c == nil {
		return context.WithTimeout(parent, d)
	}
	ctx, cancel := context.WithCancelCause(parent)
	t := c.AfterFunc(d, func() { cancel(context.DeadlineExceeded) })
	return &deadlineCtx{ctx}, func() {
		t.Stop()
		cancel(context.Canceled)
	}
}

// deadlineCtx reports context.DeadlineExceeded (not Canceled) from Err when
// the fake timeout fired, like a real deadline would.
type deadlineCtx struct{ context.Context }

func (c *deadlineCtx) Err() error {
	if err := c.Context.Err(); err != nil {
		if cause := context.Cause(c.Context); cause == context.DeadlineExceeded {
			return cause
		}
		return err
	}
	return nil
}
