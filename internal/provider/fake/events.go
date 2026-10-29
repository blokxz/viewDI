package fake

import (
	"context"
	"errors"
	"sync"

	"github.com/blokxz/viewDI/internal/engine"
)

// subscriber queues events without bound and forwards them to its feed in
// order, so mutating the world never blocks on a slow reader and no event is
// ever dropped.
type subscriber struct {
	feed   *engine.Feed
	mu     sync.Mutex
	queue  []engine.Event
	wake   chan struct{}
	ended  chan struct{}
	endErr error
	once   sync.Once
}

// Events implements engine.Engine.
func (e *Engine) Events(ctx context.Context) (*engine.Feed, error) {
	if err := e.enter(ctx, "events"); err != nil {
		return nil, err
	}
	s := &subscriber{
		feed:  engine.NewFeed(engine.FeedBuffer),
		wake:  make(chan struct{}, 1),
		ended: make(chan struct{}),
	}
	e.mu.Lock()
	e.subs[s] = struct{}{}
	e.mu.Unlock()
	go func() {
		defer func() {
			e.mu.Lock()
			delete(e.subs, s)
			e.mu.Unlock()
		}()
		s.run(ctx, e)
	}()
	return s.feed, nil
}

func (s *subscriber) run(ctx context.Context, e *Engine) {
	for {
		s.mu.Lock()
		batch := s.queue
		s.queue = nil
		s.mu.Unlock()
		for _, ev := range batch {
			// A hung daemon stalls its stream (F3): hold events back.
			for {
				hung, changed := e.hung()
				if !hung {
					break
				}
				select {
				case <-ctx.Done():
					s.feed.Close(ctx.Err())
					return
				case <-s.ended:
					s.feed.Close(s.endErr)
					return
				case <-changed:
				}
			}
			if !s.feed.Send(ctx, ev) {
				s.feed.Close(ctx.Err())
				return
			}
		}
		select {
		case <-ctx.Done():
			s.feed.Close(ctx.Err())
			return
		case <-s.ended:
			s.feed.Close(s.endErr)
			return
		case <-s.wake:
		}
	}
}

func (s *subscriber) push(ev engine.Event) {
	s.mu.Lock()
	s.queue = append(s.queue, ev)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// end closes the subscription with err once pending events are dropped:
// a broken stream loses what it had not delivered, like a real socket.
func (s *subscriber) end(err error) {
	s.once.Do(func() {
		if err == nil {
			err = errors.New("events: stream closed")
		}
		s.endErr = err
		close(s.ended)
	})
}

// hung reports whether the engine hangs, and a channel closed on the next
// fault change.
func (e *Engine) hung() (bool, <-chan struct{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.fault == faultHang, e.faultChanged
}

func (e *Engine) emit(in engine.Instance, kind engine.EventKind, act string) {
	if e.id == engine.Incus {
		if a, ok := incusActions[act]; ok {
			act = a
		}
	}
	ev := engine.Event{Engine: e.id, Kind: kind, Action: act, InstanceID: in.ID, Name: in.Name, Time: e.clk.Now()}
	e.mu.Lock()
	subs := make([]*subscriber, 0, len(e.subs))
	for s := range e.subs {
		subs = append(subs, s)
	}
	failing := e.fault == faultFail
	e.mu.Unlock()
	if failing {
		return
	}
	for _, s := range subs {
		s.push(ev)
	}
}
