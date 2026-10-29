package fake

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
)

func homelabOnFake(t *testing.T) (*World, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	w := NewWorld(clk)
	Homelab(w)
	return w, clk
}

func recv(t *testing.T, s *engine.Stream[engine.LogLine], n int) []engine.LogLine {
	t.Helper()
	var out []engine.LogLine
	for len(out) < n {
		select {
		case l, ok := <-s.C():
			if !ok {
				t.Fatalf("stream closed after %d of %d lines: %v", len(out), n, s.Err())
			}
			out = append(out, l)
		case <-time.After(2 * time.Second):
			t.Fatalf("got %d of %d lines", len(out), n)
		}
	}
	return out
}

func TestLogsTailIsOrderedAndDeterministic(t *testing.T) {
	w, _ := homelabOnFake(t)
	in, _ := w.Docker.Instance("web-db")
	read := func() []engine.LogLine {
		s, err := w.Docker.Logs(context.Background(), in.ID, engine.LogOptions{Tail: 20})
		if err != nil {
			t.Fatal(err)
		}
		var out []engine.LogLine
		for l := range s.C() {
			out = append(out, l)
		}
		return out
	}
	a, b := read(), read()
	if len(a) != 20 || len(b) != 20 {
		t.Fatalf("tail 20 gave %d and %d lines", len(a), len(b))
	}
	var errs int
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("line %d differs: %+v vs %+v", i, a[i], b[i])
		}
		if i > 0 && a[i].Time.Before(a[i-1].Time) {
			t.Fatalf("line %d out of order", i)
		}
		if a[i].Source == engine.Stderr {
			errs++
		}
	}
	if errs != 5 { // web-db: one error every 4 lines
		t.Errorf("%d stderr lines in 20, want 5", errs)
	}
}

func TestLogsFollowTracksTheInstance(t *testing.T) {
	w, clk := homelabOnFake(t)
	d := w.Docker
	in, _ := d.Instance("nginx-proxy") // 5 lines/s
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := d.Logs(ctx, in.ID, engine.LogOptions{Tail: 2, Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	first := recv(t, s, 2)
	clk.WaitPending(1, time.Second)
	clk.Advance(time.Second)
	more := recv(t, s, 5)
	if !more[0].Time.After(first[1].Time) {
		t.Fatalf("followed line %v not after the tail %v", more[0].Time, first[1].Time)
	}

	d.SetState("nginx-proxy", engine.StateStopped)
	clk.WaitPending(1, time.Second)
	clk.Advance(time.Second)
	for range s.C() {
	}
	if s.Err() != nil {
		t.Fatalf("stopped instance ended the stream with %v, want a clean end", s.Err())
	}
}

func TestLogsFollowEndsOnDisconnect(t *testing.T) {
	w, clk := homelabOnFake(t)
	in, _ := w.Docker.Instance("web-api")
	s, err := w.Docker.Logs(context.Background(), in.ID, engine.LogOptions{Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	w.Docker.Disconnect()
	clk.WaitPending(1, time.Second)
	clk.Advance(time.Second)
	for range s.C() {
	}
	if !errors.Is(s.Err(), io.EOF) {
		t.Fatalf("err %v, want EOF", s.Err())
	}
}

func TestLogsResumeAcrossRestarts(t *testing.T) {
	w, clk := homelabOnFake(t)
	d := w.Docker
	in, _ := d.Instance("web-api")
	tail := func() engine.LogLine {
		s, err := d.Logs(context.Background(), in.ID, engine.LogOptions{Tail: 1})
		if err != nil {
			t.Fatal(err)
		}
		var last engine.LogLine
		for l := range s.C() {
			last = l
		}
		return last
	}
	before := tail()
	d.SetState("web-api", engine.StateStopped)
	clk.Advance(time.Hour)
	if got := tail(); got != before {
		t.Fatalf("a stopped instance logged: %+v after %+v", got, before)
	}
	d.SetState("web-api", engine.StateRunning)
	clk.Advance(10 * time.Second)
	after := tail()
	if !after.Time.After(before.Time.Add(time.Hour)) {
		t.Fatalf("restarted log at %v, want after the restart", after.Time)
	}
}

func TestLogsCarryEscapes(t *testing.T) {
	// The API image colours its levels; the UI must sanitise them.
	w, _ := homelabOnFake(t)
	in, _ := w.Docker.Instance("web-api")
	s, _ := w.Docker.Logs(context.Background(), in.ID, engine.LogOptions{Tail: 5})
	found := false
	for l := range s.C() {
		found = found || strings.Contains(l.Text, "\x1b[")
	}
	if !found {
		t.Fatal("no escape sequence in web-api's log")
	}
}

func TestWatchStatsDetail(t *testing.T) {
	w, clk := homelabOnFake(t)
	in, _ := w.Incus.Instance("web-lab")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := w.Incus.WatchStats(ctx, in.ID, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a := <-s.C()
	clk.WaitPending(1, time.Second)
	clk.Advance(time.Second)
	b := <-s.C()
	if len(a.Ifaces) != 1 || a.Ifaces[0].Name != "eth0" {
		t.Errorf("ifaces %+v", a.Ifaces)
	}
	if len(a.Disks) != 2 || a.Disks[0].Name != "data" || a.Disks[1].Name != "root" {
		t.Errorf("disks %+v", a.Disks)
	}
	if !b.Time.After(a.Time) || b.CPU < a.CPU {
		t.Errorf("second sample %+v not after %+v", b, a)
	}

	w.Incus.Remove("web-lab")
	clk.WaitPending(1, time.Second)
	clk.Advance(time.Second)
	for range s.C() {
	}
	if engine.Classify(s.Err()) != engine.ClassNotFound {
		t.Fatalf("removed instance: %v, want not-found", s.Err())
	}
}
