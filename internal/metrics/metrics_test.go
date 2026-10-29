package metrics

import (
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

var t0 = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

const all = engine.HasCPU | engine.HasMem | engine.HasNet | engine.HasBlk

func TestRate(t *testing.T) {
	prev := engine.Sample{Time: t0, Has: all, CPU: time.Second, NetRx: 1000, NetTx: 500, BlkRead: 0, BlkWrite: 4096}
	cur := engine.Sample{Time: t0.Add(2 * time.Second), Has: all, CPU: 2 * time.Second, NetRx: 3000, NetTx: 500, BlkRead: 8192, BlkWrite: 4096, MemUsed: 7, MemLimit: 10}
	p, ok := Rate(prev, cur)
	if !ok || p.CPU != 50 || p.NetRx != 1000 || p.NetTx != 0 || p.BlkRead != 4096 || p.MemUsed != 7 || p.MemPercent() != 70 {
		t.Fatalf("%+v", p)
	}
	// Restart: counters reset, no negative or huge rate.
	reset := cur
	reset.Time, reset.CPU, reset.NetRx = cur.Time.Add(time.Second), 10*time.Millisecond, 10
	p, _ = Rate(cur, reset)
	if p.CPU != 0 || p.NetRx != 0 {
		t.Fatalf("reset: %+v", p)
	}
	if _, ok := Rate(cur, prev); ok {
		t.Fatal("out-of-order samples")
	}
	// A field missing on either side is not reported.
	noBlk := cur
	noBlk.Has &^= engine.HasBlk
	if p, _ := Rate(prev, noBlk); p.Has&engine.HasBlk != 0 || p.BlkRead != 0 {
		t.Fatalf("missing field: %+v", p)
	}
}

func TestSum(t *testing.T) {
	s := Sum(t0, []Point{{CPU: 10, MemUsed: 5, MemLimit: 10, CPULimit: 1}, {CPU: 20, MemUsed: 5, MemLimit: 0, CPULimit: 2}})
	if s.CPU != 30 || s.MemUsed != 10 || s.MemLimit != 0 || s.CPULimit != 3 || s.MemPercent() != -1 {
		t.Fatalf("%+v", s)
	}
	if (Point{CPU: 150, CPULimit: 2}).CPUOfLimit() != 75 {
		t.Fatal("CPUOfLimit")
	}
}

func TestRing(t *testing.T) {
	r := NewRing[int](3)
	if _, ok := r.Last(); ok || r.Len() != 0 {
		t.Fatal("empty ring")
	}
	for i := 1; i <= 5; i++ {
		r.Push(i)
	}
	if got := r.Tail(nil, 10); len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Fatalf("tail %v", got)
	}
	if got := r.Tail(nil, 2); got[0] != 4 || got[1] != 5 {
		t.Fatalf("tail 2 %v", got)
	}
	if v, _ := r.Last(); v != 5 || r.Len() != 3 || r.Cap() != 3 {
		t.Fatal("last/len")
	}
}

func TestFormat(t *testing.T) {
	for n, want := range map[uint64]string{0: "0B", 1023: "1023B", 1024: "1.0K", 340 << 20: "340M", 3 << 30: "3.0G", 1 << 62: "4.0E"} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
	if BytesRate(2.4*1024*1024) != "2.4M/s" || Percent(7.84) != "7.8" || Percent(214.2) != "214" {
		t.Error("rates and percents")
	}
}
