package docker

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/blokxz/viewDI/internal/engine"
)

// InspectRaw implements engine.Engine with the daemon's inspect JSON.
func (a *Adapter) InspectRaw(ctx context.Context, id string) ([]byte, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	res, err := a.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return nil, classify("inspect "+short(id), err)
	}
	return res.Raw, nil
}

// WatchStats implements engine.Engine with the streaming stats endpoint:
// the daemon pushes one sample per second; samples closer than interval
// to the previous one are skipped.
func (a *Adapter) WatchStats(ctx context.Context, id string, interval time.Duration) (*engine.Stream[engine.Sample], error) {
	res, err := a.cli.ContainerStats(ctx, id, client.ContainerStatsOptions{Stream: true})
	if err != nil {
		return nil, classify("watch stats "+short(id), err)
	}
	s := engine.NewStream[engine.Sample](1)
	go func() {
		defer res.Body.Close()
		dec := json.NewDecoder(res.Body)
		var last time.Time
		for {
			var st container.StatsResponse
			err := dec.Decode(&st)
			switch {
			case ctx.Err() != nil:
				s.Close(ctx.Err())
				return
			case errors.Is(err, io.EOF):
				s.Close(nil)
				return
			case err != nil:
				s.Close(classify("watch stats "+short(id), err))
				return
			}
			if st.Read.IsZero() || st.Read.Year() < 2 { // not running
				continue
			}
			if !last.IsZero() && st.Read.Sub(last) < interval-interval/10 {
				continue
			}
			last = st.Read
			if !s.Send(ctx, detailSample(id, st)) {
				s.Close(ctx.Err())
				return
			}
		}
	}()
	return s, nil
}

func detailSample(id string, st container.StatsResponse) engine.Sample {
	s := fromStats(id, st)
	for name, n := range st.Networks {
		s.Ifaces = append(s.Ifaces, engine.IfaceCounters{Name: name, Rx: n.RxBytes, Tx: n.TxBytes})
	}
	slices.SortFunc(s.Ifaces, func(a, b engine.IfaceCounters) int { return cmp.Compare(a.Name, b.Name) })
	return s
}
