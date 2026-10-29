package clock

import (
	"slices"
	"sync"
	"time"
)

// Fake is a manually advanced clock for tests and scripted scenarios.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*waiter
	added   chan struct{} // signalled whenever a waiter is registered
}

type waiter struct {
	at   time.Time
	ch   chan time.Time // After
	fn   func()         // AfterFunc
	done bool
}

// NewFake returns a fake clock set to start.
func NewFake(start time.Time) *Fake {
	return &Fake{now: start, added: make(chan struct{}, 1)}
}

// Now implements Clock.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After implements Clock.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	f.add(&waiter{ch: ch}, d)
	return ch
}

// AfterFunc implements Clock.
func (f *Fake) AfterFunc(d time.Duration, fn func()) Timer {
	w := &waiter{fn: fn}
	f.add(w, d)
	return fakeTimer{f, w}
}

func (f *Fake) add(w *waiter, d time.Duration) {
	f.mu.Lock()
	w.at = f.now.Add(d)
	if d <= 0 {
		f.mu.Unlock()
		f.fire(w, w.at)
		return
	}
	f.waiters = append(f.waiters, w)
	f.mu.Unlock()
	select {
	case f.added <- struct{}{}:
	default:
	}
}

// Advance moves the clock forward by d, firing every timer that comes due,
// in deadline order.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	target := f.now.Add(d)
	f.mu.Unlock()
	for {
		f.mu.Lock()
		slices.SortStableFunc(f.waiters, func(a, b *waiter) int { return a.at.Compare(b.at) })
		if len(f.waiters) == 0 || f.waiters[0].at.After(target) {
			f.now = target
			f.mu.Unlock()
			return
		}
		w := f.waiters[0]
		f.waiters = f.waiters[1:]
		if w.at.After(f.now) {
			f.now = w.at
		}
		f.mu.Unlock()
		f.fire(w, w.at)
	}
}

func (f *Fake) fire(w *waiter, at time.Time) {
	f.mu.Lock()
	if w.done {
		f.mu.Unlock()
		return
	}
	w.done = true
	f.mu.Unlock()
	if w.ch != nil {
		w.ch <- at
	}
	if w.fn != nil {
		go w.fn()
	}
}

// Pending returns the number of timers not yet fired or stopped.
func (f *Fake) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waiters)
}

// WaitPending blocks until at least n timers are pending or timeout (real
// time) elapses, and reports whether the condition was met. Tests use it to
// let goroutines reach their next wait before advancing the clock.
func (f *Fake) WaitPending(n int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		if f.Pending() >= n {
			return true
		}
		select {
		case <-f.added:
		case <-time.After(time.Millisecond):
		case <-deadline:
			return f.Pending() >= n
		}
	}
}

type fakeTimer struct {
	f *Fake
	w *waiter
}

func (t fakeTimer) Stop() bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	if t.w.done {
		return false
	}
	t.w.done = true
	t.f.waiters = slices.DeleteFunc(t.f.waiters, func(w *waiter) bool { return w == t.w })
	return true
}
