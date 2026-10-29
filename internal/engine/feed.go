package engine

import (
	"context"
	"sync"
)

// FeedBuffer is the default event feed capacity. Events are never dropped:
// a full feed applies backpressure to the stream reader instead.
const FeedBuffer = 64

// Stream is an ordered subscription: events, log lines or samples. The
// adapter creates it with NewStream, Sends values in order and Closes it
// exactly once when the source ends.
type Stream[T any] struct {
	c    chan T
	once sync.Once
	mu   sync.Mutex
	err  error
}

// Feed is an event subscription.
type Feed = Stream[Event]

// NewStream returns an open stream with the given buffer.
func NewStream[T any](buffer int) *Stream[T] {
	return &Stream[T]{c: make(chan T, buffer)}
}

// NewFeed returns an open event feed with the given buffer.
func NewFeed(buffer int) *Feed { return NewStream[Event](buffer) }

// C delivers values in stream order until the stream closes.
func (f *Stream[T]) C() <-chan T { return f.c }

// Send delivers v, blocking while the stream is full. It reports false if
// ctx ended first.
func (f *Stream[T]) Send(ctx context.Context, v T) bool {
	select {
	case f.c <- v:
		return true
	case <-ctx.Done():
		return false
	}
}

// Close ends the stream with the reason err (nil for a clean end). Only the
// first call has an effect; Send must not be called afterwards.
func (f *Stream[T]) Close(err error) {
	f.once.Do(func() {
		f.mu.Lock()
		f.err = err
		f.mu.Unlock()
		close(f.c)
	})
}

// Err reports why the stream closed; valid once C is closed.
func (f *Stream[T]) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}
