package docker

import (
	"context"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"

	"github.com/blokxz/viewDI/internal/engine"
)

// relevant lists the container actions that change what viewDI shows.
// Exec, attach, resize, top, copy… are noise.
var relevant = map[events.Action]bool{
	events.ActionCreate: true, events.ActionStart: true, events.ActionRestart: true,
	events.ActionStop: true, events.ActionDie: true, events.ActionKill: true,
	events.ActionPause: true, events.ActionUnPause: true, events.ActionDestroy: true,
	events.ActionRename: true, events.ActionUpdate: true, events.ActionOOM: true,
	events.ActionHealthStatus: true, events.ActionHealthStatusRunning: true,
	events.ActionHealthStatusHealthy: true, events.ActionHealthStatusUnhealthy: true,
}

// Events implements engine.Engine.
func (a *Adapter) Events(ctx context.Context) (*engine.Feed, error) {
	res := a.cli.Events(ctx, client.EventsListOptions{Filters: make(client.Filters).Add("type", string(events.ContainerEventType))})
	// The client returns once the response headers arrived, or with the
	// error already queued.
	select {
	case err := <-res.Err:
		if err == nil {
			err = context.Canceled
		}
		return nil, classify("events", err)
	default:
	}
	feed := engine.NewFeed(engine.FeedBuffer)
	go func() {
		for {
			select {
			case msg := <-res.Messages:
				ev, ok := fromMessage(msg)
				if !ok {
					continue
				}
				a.forgetStarted(ev.InstanceID)
				if !feed.Send(ctx, ev) {
					feed.Close(ctx.Err())
					return
				}
			case err := <-res.Err:
				if ctx.Err() != nil {
					feed.Close(ctx.Err())
					return
				}
				feed.Close(classify("events", err))
				return
			}
		}
	}()
	return feed, nil
}

func fromMessage(m events.Message) (engine.Event, bool) {
	if m.Type != events.ContainerEventType || !relevant[m.Action] || m.Actor.ID == "" {
		return engine.Event{}, false
	}
	kind := engine.EventChanged
	if m.Action == events.ActionDestroy {
		kind = engine.EventRemoved
	}
	t := time.Unix(0, m.TimeNano)
	if m.TimeNano == 0 {
		t = time.Unix(m.Time, 0)
	}
	return engine.Event{
		Engine:     engine.Docker,
		Kind:       kind,
		Action:     string(m.Action),
		InstanceID: m.Actor.ID,
		Name:       m.Actor.Attributes["name"],
		Time:       t.UTC(),
	}, true
}
