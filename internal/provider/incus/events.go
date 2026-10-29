package incus

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/lxc/incus/v6/shared/api"

	"github.com/blokxz/viewDI/internal/engine"
)

// relevant lists the lifecycle actions that change what viewDI shows.
var relevant = map[string]engine.EventKind{
	api.EventLifecycleInstanceCreated:   engine.EventChanged,
	api.EventLifecycleInstanceStarted:   engine.EventChanged,
	api.EventLifecycleInstanceStopped:   engine.EventChanged,
	api.EventLifecycleInstanceShutdown:  engine.EventChanged,
	api.EventLifecycleInstanceRestarted: engine.EventChanged,
	api.EventLifecycleInstancePaused:    engine.EventChanged,
	api.EventLifecycleInstanceResumed:   engine.EventChanged,
	api.EventLifecycleInstanceUpdated:   engine.EventChanged,
	api.EventLifecycleInstanceRestored:  engine.EventChanged,
	api.EventLifecycleInstanceMigrated:  engine.EventChanged,
	api.EventLifecycleInstanceReady:     engine.EventChanged,
	api.EventLifecycleInstanceRenamed:   engine.EventChanged,
	api.EventLifecycleInstanceDeleted:   engine.EventRemoved,
}

// maxEvent caps one websocket message (untrusted input).
const maxEvent = 1 << 20

// Events implements engine.Engine. It reads lifecycle events from the
// websocket itself, so they arrive in order.
func (a *Adapter) Events(ctx context.Context) (*engine.Feed, error) {
	path := "/events?type=lifecycle"
	if !a.user {
		path += "&all-projects=true"
	}
	conn, err := a.client(ctx, "").RawWebsocket(path)
	if err != nil && !a.user && fallBack(err) {
		conn, err = a.client(ctx, api.ProjectDefaultName).RawWebsocket("/events?type=lifecycle")
	}
	if err != nil {
		return nil, classify(ctx, "events", err)
	}
	conn.SetReadLimit(maxEvent)
	feed := engine.NewFeed(engine.FeedBuffer)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	go func() {
		defer stop()
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				if ctx.Err() != nil {
					feed.Close(ctx.Err())
				} else {
					feed.Close(classify(ctx, "events", closeErr(err)))
				}
				return
			}
			for _, ev := range a.fromEvent(data) {
				if !feed.Send(ctx, ev) {
					feed.Close(ctx.Err())
					return
				}
			}
		}
	}()
	return feed, nil
}

// closeErr turns a normal websocket close into an EOF-like transient error.
func closeErr(err error) error {
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		return &engine.Error{Class: engine.ClassTransient, Op: "events", Err: err}
	}
	return err
}

// fromEvent maps one raw event. A rename yields a removal of the old
// instance and a change of the new one.
func (a *Adapter) fromEvent(data []byte) []engine.Event {
	var ev api.Event
	if err := json.Unmarshal(data, &ev); err != nil || ev.Type != api.EventTypeLifecycle {
		return nil
	}
	var lc api.EventLifecycle
	if err := json.Unmarshal(ev.Metadata, &lc); err != nil {
		return nil
	}
	kind, ok := relevant[lc.Action]
	if !ok {
		return nil
	}
	name, project, ok := instanceFromSource(lc.Source)
	if !ok {
		return nil // snapshots, backups, files…
	}
	if project == "" {
		project = firstNonEmpty(lc.Project, ev.Project, a.defaultProject())
	}
	out := engine.Event{
		Engine: engine.Incus, Kind: kind, Action: lc.Action,
		InstanceID: project + "/" + name, Name: name, Time: ev.Timestamp.UTC(),
	}
	if lc.Action == api.EventLifecycleInstanceRenamed {
		if old, _ := lc.Context["old_name"].(string); old != "" {
			gone := out
			gone.Kind, gone.InstanceID, gone.Name = engine.EventRemoved, project+"/"+old, old
			return []engine.Event{gone, out}
		}
	}
	return []engine.Event{out}
}

// instanceFromSource parses "/1.0/instances/<name>[?project=<p>]".
func instanceFromSource(src string) (name, project string, ok bool) {
	u, err := url.Parse(src)
	if err != nil {
		return "", "", false
	}
	rest, ok := strings.CutPrefix(u.Path, "/1.0/instances/")
	if !ok || rest == "" || strings.Contains(rest, "/") {
		return "", "", false
	}
	name, err = url.PathUnescape(rest)
	if err != nil {
		return "", "", false
	}
	return name, u.Query().Get("project"), true
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
