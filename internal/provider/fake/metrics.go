package fake

import (
	"context"
	"hash/fnv"
	"math"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

// Shape is the time profile of a synthetic metric.
type Shape int

// Metric shapes (docs/05-quality-plan.md §1.2: Metrics(Sine/Spike/Flat)).
const (
	Flat  Shape = iota // constant
	Sine               // smooth wave around the base
	Spike              // base load with a 4× burst for a fifth of every period
)

// Profile drives the synthetic counters of one instance.
type Profile struct {
	Shape    Shape
	Period   time.Duration // wave or spike period (default 60 s)
	CPU      float64       // cores used at the base load
	CPULimit float64       // cores; 0 = unlimited
	Mem      uint64        // bytes used at the base load
	MemLimit uint64        // bytes; 0 = unlimited
	Net      float64       // received bytes/s; transmit is a third of it
	Blk      float64       // read bytes/s; writes are half of it
}

// factor is the load multiplier at t.
func (p Profile) factor(t time.Time) float64 {
	period := p.Period
	if period <= 0 {
		period = time.Minute
	}
	phase := float64(t.UnixNano()%int64(period)) / float64(period)
	switch p.Shape {
	case Sine:
		return 1 + 0.6*math.Sin(2*math.Pi*phase)
	case Spike:
		if phase < 0.2 {
			return 4
		}
	}
	return 1
}

// defaultProfile derives a stable, varied profile from the instance name.
func defaultProfile(in engine.Instance) Profile {
	h := fnv.New32a()
	_, _ = h.Write([]byte(in.Name))
	n := h.Sum32()
	p := Profile{
		Shape:  Shape(n % 3),
		Period: time.Duration(30+n%60) * time.Second,
		CPU:    0.02 + float64(n%40)/100,
		Mem:    uint64(32+n%480) << 20,
		Net:    float64(n%200) * 1024,
		Blk:    float64(n%50) * 4096,
	}
	if in.Kind == engine.KindVM {
		p.CPU *= 3
		p.Mem *= 4
		p.MemLimit = 4 << 30
		p.CPULimit = 2
	}
	return p
}

// meter integrates a profile into cumulative counters.
type meter struct {
	profile           Profile
	last              time.Time
	cpu               time.Duration
	netRx, netTx      float64
	blkRead, blkWrite float64
}

func (m *meter) advance(now time.Time) {
	if m.last.IsZero() || !now.After(m.last) {
		m.last = now
		return
	}
	dt := now.Sub(m.last).Seconds()
	f := m.profile.factor(m.last.Add(now.Sub(m.last) / 2))
	m.cpu += time.Duration(m.profile.CPU * f * dt * float64(time.Second))
	m.netRx += m.profile.Net * f * dt
	m.netTx += m.profile.Net / 3 * f * dt
	m.blkRead += m.profile.Blk * f * dt
	m.blkWrite += m.profile.Blk / 2 * f * dt
	m.last = now
}

// SetProfile replaces the synthetic metrics profile of an instance.
func (e *Engine) SetProfile(name string, p Profile) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	in, ok := e.byName(name)
	if !ok {
		return false
	}
	m := e.meterFor(in)
	m.advance(e.clk.Now())
	m.profile = p
	return true
}

func (e *Engine) meterFor(in *engine.Instance) *meter {
	m, ok := e.meters[in.ID]
	if !ok {
		m = &meter{profile: defaultProfile(*in)}
		e.meters[in.ID] = m
	}
	return m
}

// Stats implements engine.Engine with synthetic counters. Counters restart
// from zero whenever the instance (re)starts, like real cgroups do.
func (e *Engine) Stats(ctx context.Context, ids []string) ([]engine.Sample, error) {
	if err := e.enter(ctx, "stats"); err != nil {
		return nil, err
	}
	now := e.clk.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]engine.Sample, 0, len(ids))
	for _, id := range ids {
		in, ok := e.instances[id]
		if !ok || in.State != engine.StateRunning {
			continue
		}
		m := e.meterFor(in)
		m.advance(now)
		f := m.profile.factor(now)
		mem := uint64(float64(m.profile.Mem) * (0.9 + 0.1*f))
		if m.profile.MemLimit > 0 {
			mem = min(mem, m.profile.MemLimit)
		}
		out = append(out, engine.Sample{
			InstanceID: id, Time: now,
			Has:      engine.HasCPU | engine.HasMem | engine.HasNet | engine.HasBlk | engine.HasPIDs,
			CPU:      m.cpu,
			CPULimit: m.profile.CPULimit,
			MemUsed:  mem, MemLimit: m.profile.MemLimit,
			NetRx: uint64(m.netRx), NetTx: uint64(m.netTx),
			BlkRead: uint64(m.blkRead), BlkWrite: uint64(m.blkWrite),
			PIDs: 3 + uint64(len(in.Name)),
		})
	}
	return out, nil
}
