// Package metrics turns cumulative resource counters into per-second rates,
// keeps fixed-size history rings and aggregates instances per engine.
// Sub-packages read the counters: cgroup (cgroup v2 files), openmetrics
// (the Incus /1.0/metrics parser) and sampler (the scheduler).
package metrics

import (
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

// Point is one instance (or engine aggregate) reading, in rates.
type Point struct {
	Time     time.Time
	Has      engine.Fields
	CPU      float64 // percent of one core (100 = one core busy), like docker stats
	CPULimit float64 // cores; 0 = unlimited or unknown
	MemUsed  uint64
	MemLimit uint64  // 0 = unlimited or unknown
	NetRx    float64 // bytes/s
	NetTx    float64
	BlkRead  float64
	BlkWrite float64
	PIDs     uint64
}

// MemPercent is memory use relative to the limit, or -1 without a limit.
func (p Point) MemPercent() float64 {
	if p.MemLimit == 0 || p.Has&engine.HasMem == 0 {
		return -1
	}
	return float64(p.MemUsed) / float64(p.MemLimit) * 100
}

// CPUOfLimit is CPU use relative to the CPU limit (100 = limit reached), or
// relative to one core without a limit.
func (p Point) CPUOfLimit() float64 {
	if p.CPULimit > 0 {
		return p.CPU / p.CPULimit
	}
	return p.CPU
}

// Rate turns two consecutive samples of the same instance into a Point.
// A counter that went backwards (the instance restarted, or the counter
// wrapped) yields a zero rate for that interval rather than a huge spike.
// ok is false when the samples are not in time order.
func Rate(prev, cur engine.Sample) (p Point, ok bool) {
	dt := cur.Time.Sub(prev.Time).Seconds()
	if dt <= 0 {
		return Point{}, false
	}
	p = Point{
		Time: cur.Time, Has: cur.Has, CPULimit: cur.CPULimit,
		MemUsed: cur.MemUsed, MemLimit: cur.MemLimit, PIDs: cur.PIDs,
	}
	if cur.Has&prev.Has&engine.HasCPU != 0 && cur.CPU >= prev.CPU {
		p.CPU = float64(cur.CPU-prev.CPU) / float64(time.Second) / dt * 100
	}
	rate := func(a, b uint64) float64 {
		if b < a {
			return 0
		}
		return float64(b-a) / dt
	}
	if cur.Has&prev.Has&engine.HasNet != 0 {
		p.NetRx, p.NetTx = rate(prev.NetRx, cur.NetRx), rate(prev.NetTx, cur.NetTx)
	} else {
		p.Has &^= engine.HasNet
	}
	if cur.Has&prev.Has&engine.HasBlk != 0 {
		p.BlkRead, p.BlkWrite = rate(prev.BlkRead, cur.BlkRead), rate(prev.BlkWrite, cur.BlkWrite)
	} else {
		p.Has &^= engine.HasBlk
	}
	if prev.Has&engine.HasCPU == 0 {
		p.Has &^= engine.HasCPU
	}
	return p, true
}

// Sum aggregates instance points into an engine total at time t. Limits
// are summed only when every instance has one.
func Sum(t time.Time, points []Point) Point {
	total := Point{Time: t}
	unlimitedMem, unlimitedCPU := false, false
	for _, p := range points {
		total.Has |= p.Has
		total.CPU += p.CPU
		total.MemUsed += p.MemUsed
		total.NetRx += p.NetRx
		total.NetTx += p.NetTx
		total.BlkRead += p.BlkRead
		total.BlkWrite += p.BlkWrite
		total.PIDs += p.PIDs
		total.MemLimit += p.MemLimit
		total.CPULimit += p.CPULimit
		unlimitedMem = unlimitedMem || p.MemLimit == 0
		unlimitedCPU = unlimitedCPU || p.CPULimit == 0
	}
	if unlimitedMem {
		total.MemLimit = 0
	}
	if unlimitedCPU {
		total.CPULimit = 0
	}
	return total
}
