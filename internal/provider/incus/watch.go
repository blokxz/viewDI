package incus

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/blokxz/viewDI/internal/engine"
)

// minWatch bounds the instance-state polling rate.
const minWatch = 500 * time.Millisecond

// InspectRaw implements engine.Engine with the instance, its expanded
// config and its state, as the API returns them.
func (a *Adapter) InspectRaw(ctx context.Context, id string) ([]byte, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	project, name := a.split(id)
	in, _, err := a.client(ctx, project).GetInstanceFull(name)
	if err != nil {
		return nil, classify(ctx, "inspect "+id, err)
	}
	return json.Marshal(in)
}

// WatchStats implements engine.Engine by polling the instance state. The
// first read happens before WatchStats returns.
func (a *Adapter) WatchStats(ctx context.Context, id string, interval time.Duration) (*engine.Stream[engine.Sample], error) {
	project, name := a.split(id)
	interval = max(interval, minWatch)
	read := func() (*api.InstanceState, error) {
		rctx, cancel := engine.WithDefaultTimeout(ctx)
		defer cancel()
		st, _, err := a.client(rctx, project).GetInstanceState(name)
		return st, classify(rctx, "watch stats "+id, err)
	}
	st, err := read()
	if err != nil {
		return nil, err
	}
	s := engine.NewStream[engine.Sample](1)
	go func() {
		for {
			if smp, ok := detailSample(id, st, time.Now().UTC()); ok && !s.Send(ctx, smp) {
				s.Close(ctx.Err())
				return
			}
			select {
			case <-ctx.Done():
				s.Close(ctx.Err())
				return
			case <-time.After(interval):
			}
			if st, err = read(); err != nil {
				if ctx.Err() != nil {
					err = ctx.Err()
				}
				s.Close(err)
				return
			}
		}
	}()
	return s, nil
}

func detailSample(id string, st *api.InstanceState, now time.Time) (engine.Sample, bool) {
	s, ok := fromState(id, st, now)
	if !ok {
		return s, false
	}
	for dev, n := range st.Network {
		if dev == "lo" || n.Type == "loopback" {
			continue
		}
		s.Ifaces = append(s.Ifaces, engine.IfaceCounters{
			Name: dev, Rx: uint64(max(n.Counters.BytesReceived, 0)), Tx: uint64(max(n.Counters.BytesSent, 0)),
		})
	}
	slices.SortFunc(s.Ifaces, func(a, b engine.IfaceCounters) int { return cmp.Compare(a.Name, b.Name) })
	for dev, d := range st.Disk {
		s.Disks = append(s.Disks, engine.DiskUsage{Name: dev, Used: uint64(max(d.Usage, 0))})
	}
	slices.SortFunc(s.Disks, func(a, b engine.DiskUsage) int { return cmp.Compare(a.Name, b.Name) })
	return s, true
}
