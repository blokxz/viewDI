package incus

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/metrics/openmetrics"
)

// maxState bounds concurrent instance-state requests in the fallback path.
const maxState = 8

// Stats implements engine.Engine. One /1.0/metrics request covers every
// instance; restricted connections (which may not read it) fall back to
// one instance-state request per instance, which has no disk counters.
func (a *Adapter) Stats(ctx context.Context, ids []string) ([]engine.Sample, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	if !a.user {
		samples, err := a.metricsStats(ctx, ids)
		if err == nil {
			return samples, nil
		}
		if engine.Classify(err) != engine.ClassPermission {
			return nil, err
		}
	}
	return a.stateStats(ctx, ids)
}

func (a *Adapter) metricsStats(ctx context.Context, ids []string) ([]engine.Sample, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix.socket/1.0/metrics", nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.srv.DoHTTP(req)
	if err != nil {
		return nil, classify(ctx, "metrics", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		return nil, engine.Errorf(engine.ClassPermission, "metrics", fmt.Errorf("HTTP %d", resp.StatusCode))
	case resp.StatusCode != http.StatusOK:
		return nil, engine.Errorf(engine.ClassTransient, "metrics", fmt.Errorf("HTTP %d", resp.StatusCode))
	}
	parsed, err := openmetrics.Parse(resp.Body)
	if err != nil {
		return nil, engine.Wrap("metrics", err)
	}
	return fromMetrics(parsed, ids, time.Now().UTC()), nil
}

// fromMetrics folds Incus metric samples into one engine.Sample per
// requested instance.
func fromMetrics(parsed []openmetrics.Sample, ids []string, now time.Time) []engine.Sample {
	want := make(map[string]*engine.Sample, len(ids))
	type mem struct{ total, avail, free, cached float64 }
	mems := map[string]*mem{}
	for _, id := range ids {
		want[id] = &engine.Sample{InstanceID: id, Time: now}
		mems[id] = &mem{avail: -1, free: -1}
	}
	for _, m := range parsed {
		name := m.Label("name")
		if name == "" {
			continue
		}
		id := projectOr(m.Label("project")) + "/" + name
		s, ok := want[id]
		if !ok {
			continue
		}
		v := m.Value
		switch m.Name {
		case "incus_cpu_seconds_total":
			s.CPU += time.Duration(v * float64(time.Second))
			s.Has |= engine.HasCPU
		case "incus_cpu_effective_total":
			s.CPULimit = v
		case "incus_memory_MemTotal_bytes":
			mems[id].total = v
		case "incus_memory_MemAvailable_bytes":
			mems[id].avail = v
		case "incus_memory_MemFree_bytes":
			mems[id].free = v
		case "incus_memory_Cached_bytes":
			mems[id].cached = v
		case "incus_network_receive_bytes_total":
			if m.Label("device") != "lo" {
				s.NetRx += uint64(v)
				s.Has |= engine.HasNet
			}
		case "incus_network_transmit_bytes_total":
			if m.Label("device") != "lo" {
				s.NetTx += uint64(v)
				s.Has |= engine.HasNet
			}
		case "incus_disk_read_bytes_total":
			s.BlkRead += uint64(v)
			s.Has |= engine.HasBlk
		case "incus_disk_written_bytes_total":
			s.BlkWrite += uint64(v)
			s.Has |= engine.HasBlk
		case "incus_procs_total":
			s.PIDs = uint64(v)
			s.Has |= engine.HasPIDs
		}
	}
	out := make([]engine.Sample, 0, len(ids))
	for _, id := range ids {
		s := want[id]
		if s.Has == 0 {
			continue // not running, or a VM without incus-agent
		}
		if m := mems[id]; m.total > 0 {
			used := m.total - m.avail
			if m.avail < 0 {
				used = m.total - max(m.free, 0) - m.cached
			}
			s.MemUsed, s.MemLimit = uint64(max(used, 0)), uint64(m.total)
			s.Has |= engine.HasMem
		}
		out = append(out, *s)
	}
	return out
}

func projectOr(p string) string {
	if p == "" {
		return api.ProjectDefaultName
	}
	return p
}

func (a *Adapter) stateStats(ctx context.Context, ids []string) ([]engine.Sample, error) {
	var (
		mu       sync.Mutex
		out      []engine.Sample
		firstErr error
		wg       sync.WaitGroup
		sem      = make(chan struct{}, maxState)
	)
	for _, id := range ids {
		project, name, ok := strings.Cut(id, "/")
		if !ok {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			st, _, err := a.client(ctx, project).GetInstanceState(name)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if cerr := classify(ctx, "instance state "+id, err); engine.Classify(cerr) != engine.ClassNotFound && firstErr == nil {
					firstErr = cerr
				}
				return
			}
			if s, ok := fromState(id, st, time.Now().UTC()); ok {
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

func fromState(id string, st *api.InstanceState, now time.Time) (engine.Sample, bool) {
	if st == nil || st.StatusCode != api.Running {
		return engine.Sample{}, false
	}
	s := engine.Sample{
		InstanceID: id, Time: now,
		Has:  engine.HasCPU | engine.HasMem | engine.HasNet | engine.HasPIDs,
		CPU:  time.Duration(st.CPU.Usage),
		PIDs: uint64(max(st.Processes, 0)),
	}
	s.MemUsed = uint64(max(st.Memory.Usage, 0))
	s.MemLimit = uint64(max(st.Memory.Total, 0))
	for dev, n := range st.Network {
		if dev == "lo" || n.Type == "loopback" {
			continue
		}
		s.NetRx += uint64(max(n.Counters.BytesReceived, 0))
		s.NetTx += uint64(max(n.Counters.BytesSent, 0))
	}
	return s, true
}
