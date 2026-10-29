package engine

import (
	"context"
	"sync"
)

// FeedBuffer is the default event feed capacity. Events are never dropped:
// a full feed applies backpressure to the stream reader instead.
const FeedBuffer = 64

// Feed is an event subscription. Adapters create it with NewFeed, Send
// events in order and Close it exactly once when the stream ends.
type Feed struct {
	c    chan Event
	once sync.Once
	mu   sync.Mutex
	err  error
}

// NewFeed returns an open feed with the given buffer.
func NewFeed(buffer int) *Feed {
	return &Feed{c: make(chan Event, buffer)}
}

// C delivers events in stream order until the feed closes.
func (f *Feed) C() <-chan Event { return f.c }

// Send delivers ev, blocking while the feed is full. It reports false if ctx
// ended first.
func (f *Feed) Send(ctx context.Context, ev Event) bool {
	select {
	case f.c <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// Close ends the feed with the reason err (nil for a clean end). Only the
// first call has an effect; Send must not be called afterwards.
func (f *Feed) Close(err error) {
	f.once.Do(func() {
		f.mu.Lock()
		f.err = err
		f.mu.Unlock()
		close(f.c)
	})
}

// Err reports why the feed closed; valid once C is closed.
func (f *Feed) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}
