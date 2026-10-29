package incus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"

	"github.com/blokxz/viewDI/internal/engine"
)

// Act implements engine.Engine. Incus refuses actions already in effect
// ("already running"), so the state is read first: an action in effect is
// a no-op, one the state does not allow is a conflict. Restarting a
// stopped instance starts it, like Docker does.
func (a *Adapter) Act(ctx context.Context, id string, action engine.Action, opts engine.ActionOptions) error {
	ctx, cancel := context.WithTimeout(ctx, engine.ActionTimeout(opts))
	defer cancel()
	op := string(action) + " " + id
	project, name := a.split(id)
	c := a.client(ctx, project)
	st, _, err := c.GetInstanceState(name)
	if err != nil {
		return classify(ctx, op, err)
	}
	put := api.InstanceStatePut{Action: string(action), Timeout: int(engine.DefaultStopTimeout / time.Second), Force: opts.Force}
	if opts.Timeout > 0 {
		put.Timeout = int(opts.Timeout / time.Second)
	}
	running, frozen := st.StatusCode == api.Running, st.StatusCode == api.Frozen
	switch action {
	case engine.Start:
		if running {
			return nil
		}
		if frozen {
			return engine.Errorf(engine.ClassConflict, op, errors.New("instance is frozen: unfreeze it instead"))
		}
	case engine.Stop:
		if !running && !frozen {
			return nil
		}
		put.Force = put.Force || frozen // a frozen instance cannot shut down cleanly
	case engine.Restart:
		if frozen {
			return engine.Errorf(engine.ClassConflict, op, errors.New("instance is frozen: unfreeze it first"))
		}
		if !running {
			put.Action = string(engine.Start)
		}
	case engine.Pause:
		if frozen {
			return nil
		}
		if !running {
			return engine.Errorf(engine.ClassConflict, op, errors.New("instance is not running"))
		}
		put.Action = "freeze"
	case engine.Unpause:
		if running {
			return nil
		}
		if !frozen {
			return engine.Errorf(engine.ClassConflict, op, errors.New("instance is not frozen"))
		}
		put.Action = "unfreeze"
	default:
		return engine.Errorf(engine.ClassConflict, op, errors.New("unknown action"))
	}
	if put.Action != "stop" && put.Action != "restart" {
		put.Timeout = 0
	}
	o, err := c.UpdateInstanceState(name, put, "")
	if err != nil {
		return classify(ctx, op, err)
	}
	return a.wait(ctx, op, o)
}

// Delete implements engine.Engine. Incus refuses to delete a running
// instance: opts.Stop (or Force) stops it first, forcibly with Force.
func (a *Adapter) Delete(ctx context.Context, id string, opts engine.DeleteOptions) error {
	ctx, cancel := context.WithTimeout(ctx, engine.ActionTimeout(engine.ActionOptions{}))
	defer cancel()
	op := "delete " + id
	project, name := a.split(id)
	c := a.client(ctx, project)
	st, _, err := c.GetInstanceState(name)
	if err != nil {
		return classify(ctx, op, err)
	}
	if st.StatusCode == api.Running || st.StatusCode == api.Frozen {
		if !opts.Stop && !opts.Force {
			return engine.Errorf(engine.ClassConflict, op, errors.New("instance is running: stop it first"))
		}
		put := api.InstanceStatePut{Action: "stop", Timeout: int(engine.DefaultStopTimeout / time.Second), Force: opts.Force || st.StatusCode == api.Frozen}
		o, err := c.UpdateInstanceState(name, put, "")
		if err != nil {
			return classify(ctx, op, err)
		}
		if err := a.wait(ctx, op, o); err != nil {
			return err
		}
	}
	o, err := c.DeleteInstance(name)
	if err != nil {
		return classify(ctx, op, err)
	}
	return a.wait(ctx, op, o)
}

// wait waits for a background operation through the /wait endpoint with
// ctx (the client's own wait cannot be canceled without an event listener).
func (a *Adapter) wait(ctx context.Context, op string, o incus.Operation) error {
	id := o.Get().ID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix.socket/1.0/operations/"+url.PathEscape(id)+"/wait?timeout=-1", nil)
	if err != nil {
		return engine.Wrap(op, err)
	}
	resp, err := a.srv.DoHTTP(req)
	if err != nil {
		return classify(ctx, op, err)
	}
	defer resp.Body.Close()
	var env struct {
		Type     string        `json:"type"`
		Error    string        `json:"error"`
		Code     int           `json:"error_code"`
		Metadata api.Operation `json:"metadata"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env); err != nil {
		return classify(ctx, op, err)
	}
	switch {
	case env.Type == "error" && resp.StatusCode == http.StatusNotFound:
		return engine.NotFound(op, "operation "+id)
	case env.Type == "error":
		return engine.Errorf(engine.ClassTransient, op, fmt.Errorf("HTTP %d: %s", env.Code, env.Error))
	case env.Metadata.StatusCode == api.Success:
		return nil
	case env.Metadata.StatusCode == api.Cancelled:
		return engine.Errorf(engine.ClassCanceled, op, errors.New("operation canceled"))
	case env.Metadata.Err != "":
		return engine.Errorf(engine.ClassConflict, op, errors.New(env.Metadata.Err))
	}
	return engine.Errorf(engine.ClassTransient, op, fmt.Errorf("operation ended as %s", env.Metadata.Status))
}
