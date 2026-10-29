package docker

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/blokxz/viewDI/internal/engine"
)

// maxStats caps one stats response (untrusted input).
const maxStats = 1 << 20

// Stats implements engine.Engine with one-shot stats requests (no
// streaming connection per container), at most maxInspect at a time.
func (a *Adapter) Stats(ctx context.Context, ids []string) ([]engine.Sample, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	var (
		mu       sync.Mutex
		out      = make([]engine.Sample, 0, len(ids))
		firstErr error
		wg       sync.WaitGroup
		sem      = make(chan struct{}, maxInspect)
	)
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			s, ok, err := a.stats(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil && engine.Classify(err) != engine.ClassNotFound && firstErr == nil:
				firstErr = err
			case ok:
				out = append(out, s)
			}
		}()
	}
	wg.Wait()
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func (a *Adapter) stats(ctx context.Context, id string) (engine.Sample, bool, error) {
	res, err := a.cli.ContainerStats(ctx, id, client.ContainerStatsOptions{})
	if err != nil {
		return engine.Sample{}, false, classify("stats "+short(id), err)
	}
	defer res.Body.Close()
	var st container.StatsResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, maxStats)).Decode(&st); err != nil {
		return engine.Sample{}, false, engine.Wrap("stats "+short(id), err)
	}
	if st.Read.IsZero() || strings.HasPrefix(st.Read.Format(time.RFC3339), "0001") {
		return engine.Sample{}, false, nil // not running
	}
	return fromStats(id, st), true, nil
}

func fromStats(id string, st container.StatsResponse) engine.Sample {
	s := engine.Sample{
		InstanceID: id,
		Time:       st.Read.UTC(),
		Has:        engine.HasCPU | engine.HasMem | engine.HasNet | engine.HasBlk | engine.HasPIDs,
		CPU:        time.Duration(st.CPUStats.CPUUsage.TotalUsage),
		MemLimit:   st.MemoryStats.Limit,
		PIDs:       st.PidsStats.Current,
	}
	// Like docker stats: usage minus the inactive page cache.
	inactive := st.MemoryStats.Stats["inactive_file"]
	if v, ok := st.MemoryStats.Stats["total_inactive_file"]; ok { // cgroup v1
		inactive = v
	}
	if st.MemoryStats.Usage > inactive {
		s.MemUsed = st.MemoryStats.Usage - inactive
	}
	for _, n := range st.Networks {
		s.NetRx += n.RxBytes
		s.NetTx += n.TxBytes
	}
	for _, b := range st.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(b.Op) {
		case "read":
			s.BlkRead += b.Value
		case "write":
			s.BlkWrite += b.Value
		}
	}
	return s
}
