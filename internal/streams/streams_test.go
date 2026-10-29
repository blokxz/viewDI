package streams

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/provider/fake"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func setup(t *testing.T, opts Options) (*fake.World, *Registry) {
	t.Helper()
	w := fake.NewWorld(nil)
	fake.Homelab(w)
	ctx, cancel := context.WithCancel(context.Background())
	r := New(ctx, opts, w.Engines()...)
	t.Cleanup(func() {
		cancel()
		r.Wait()
	})
	return w, r
}

func key(t *testing.T, w *fake.World, id engine.ID, name string) engine.Key {
	t.Helper()
	in, ok := w.Engine(id).Instance(name)
	if !ok {
		t.Fatalf("no instance %s", name)
	}
	return in.Key()
}

// collect reads batches until done says so or the deadline passes.
func collect(t *testing.T, s *Subscription, done func(acc *Batch) bool) Batch {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var acc Batch
	for !done(&acc) {
		b, ok := s.Next(ctx)
		if !ok {
			t.Fatalf("no more batches; so far %d lines, %d samples, inspect %v, logs %+v, stats %+v",
				len(acc.Lines), len(acc.Samples), acc.Inspect != nil, acc.Logs, acc.Stats)
		}
		acc.Lines = append(acc.Lines, b.Lines...)
		acc.Dropped += b.Dropped
		acc.Samples = append(acc.Samples, b.Samples...)
		if b.Inspect != nil {
			acc.Inspect = b.Inspect
		}
		if b.Logs != nil {
			acc.Logs = b.Logs
		}
		if b.Stats != nil {
			acc.Stats = b.Stats
		}
	}
	return acc
}

func TestSubscribeDeliversEverything(t *testing.T) {
	w, r := setup(t, Options{})
	s := r.Subscribe(key(t, w, engine.Docker, "web-api"), Request{Logs: true, Tail: 20, Stats: true, Inspect: true})
	defer s.Close()
	acc := collect(t, s, func(b *Batch) bool { return len(b.Lines) >= 20 && len(b.Samples) > 0 && b.Inspect != nil })
	if acc.Logs == nil || acc.Logs.State != Live || acc.Stats == nil || acc.Stats.State != Live {
		t.Errorf("statuses %+v %+v", acc.Logs, acc.Stats)
	}
	for _, l := range acc.Lines {
		if strings.ContainsRune(l.Text, 0x1b) {
			t.Fatalf("unsanitised line %q", l.Text)
		}
	}
	if acc.Inspect.Err != nil || acc.Inspect.Instance.Name != "web-api" || len(acc.Inspect.Raw) == 0 {
		t.Errorf("inspect %+v", acc.Inspect)
	}
	if len(acc.Inspect.Instance.Env) == 0 {
		t.Error("inspect lacks the environment")
	}
}

func TestOpenCloseLeavesNothingRunning(t *testing.T) {
	w, r := setup(t, Options{})
	k := key(t, w, engine.Docker, "nginx-proxy")
	for range 100 {
		s := r.Subscribe(k, Request{Logs: true, Stats: true, Inspect: true})
		s.Close()
	}
	if !clock.Eventually(5*time.Second, func() bool { return r.Active() == 0 }) {
		t.Fatalf("%d subscriptions still running", r.Active())
	}
	// goleak (TestMain) checks that no goroutine survived.
}

func TestRemovedInstance(t *testing.T) {
	_, r := setup(t, Options{})
	s := r.Subscribe(engine.MakeKey(engine.Incus, "default/ghost"), Request{Logs: true, Stats: true, Inspect: true})
	defer s.Close()
	acc := collect(t, s, func(b *Batch) bool { return b.Logs != nil && b.Stats != nil && b.Inspect != nil })
	if acc.Logs.State != Removed || acc.Stats.State != Removed {
		t.Errorf("statuses %v %v, want removed", acc.Logs.State, acc.Stats.State)
	}
	if engine.Classify(acc.Inspect.Err) != engine.ClassNotFound {
		t.Errorf("inspect err %v", acc.Inspect.Err)
	}
}

func TestUnknownEngine(t *testing.T) {
	_, r := setup(t, Options{})
	s := r.Subscribe("podman/x", Request{Logs: true})
	s.mu.Lock()
	st := s.pending.Logs
	s.mu.Unlock()
	if st == nil || st.State != Failed {
		t.Fatalf("status %+v", st)
	}
}

func TestSlowReaderDropsOldestLines(t *testing.T) {
	w, r := setup(t, Options{MaxPending: 50})
	w.Docker.SetLogProfile("web-api", fake.LogProfile{Rate: 2000})
	s := r.Subscribe(key(t, w, engine.Docker, "web-api"), Request{Logs: true, Tail: 10})
	defer s.Close()
	// The UI is busy: nothing reads until lines were dropped.
	if !clock.Eventually(5*time.Second, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.pending.Dropped > 0
	}) {
		t.Fatal("no line dropped")
	}
	acc := collect(t, s, func(b *Batch) bool { return b.Dropped > 0 })
	if len(acc.Lines) > 50 {
		t.Fatalf("%d lines pending, cap 50", len(acc.Lines))
	}
	for i := 1; i < len(acc.Lines); i++ {
		if acc.Lines[i].Time.Before(acc.Lines[i-1].Time) {
			t.Fatal("lines out of order after dropping")
		}
	}
}

func TestReconnectResumesWithoutDuplicates(t *testing.T) {
	w, r := setup(t, Options{})
	w.Docker.SetLogProfile("nginx-proxy", fake.LogProfile{Rate: 50})
	s := r.Subscribe(key(t, w, engine.Docker, "nginx-proxy"), Request{Logs: true, Tail: 5, Stats: true})
	defer s.Close()
	collect(t, s, func(b *Batch) bool { return len(b.Lines) > 10 })

	w.Docker.Disconnect()
	acc := collect(t, s, func(b *Batch) bool { return b.Logs != nil && b.Logs.State == Retrying })
	if acc.Logs.Err == "" || acc.Logs.Retry.IsZero() {
		t.Errorf("retrying status %+v", acc.Logs)
	}
	w.Docker.Recover()
	s.Refresh()
	acc = collect(t, s, func(b *Batch) bool { return b.Logs != nil && b.Logs.State == Live && len(b.Lines) > 20 })
	for i := 1; i < len(acc.Lines); i++ {
		if !acc.Lines[i].Time.After(acc.Lines[i-1].Time) {
			t.Fatalf("line %d at %v not after %v (duplicate or out of order)", i, acc.Lines[i].Time, acc.Lines[i-1].Time)
		}
	}
}

func TestStoppedInstanceEndsAndResumes(t *testing.T) {
	w, r := setup(t, Options{})
	w.Docker.SetLogProfile("nginx-proxy", fake.LogProfile{Rate: 50})
	s := r.Subscribe(key(t, w, engine.Docker, "nginx-proxy"), Request{Logs: true, Tail: 1})
	defer s.Close()
	collect(t, s, func(b *Batch) bool { return len(b.Lines) > 3 })
	w.Docker.SetState("nginx-proxy", engine.StateStopped)
	collect(t, s, func(b *Batch) bool { return b.Logs != nil && b.Logs.State == Ended })
	w.Docker.SetState("nginx-proxy", engine.StateRunning)
	s.Refresh()
	collect(t, s, func(b *Batch) bool { return b.Logs != nil && b.Logs.State == Live && len(b.Lines) > 3 })
}

func TestBatchesAreThrottled(t *testing.T) {
	w, r := setup(t, Options{})
	w.Docker.SetLogProfile("web-api", fake.LogProfile{Rate: 1000})
	s := r.Subscribe(key(t, w, engine.Docker, "web-api"), Request{Logs: true, Tail: 1})
	defer s.Close()
	ctx := context.Background()
	var prev time.Time
	for i := range 5 {
		if _, ok := s.Next(ctx); !ok {
			t.Fatal("ended")
		}
		now := time.Now()
		if i > 0 && now.Sub(prev) < MinInterval-5*time.Millisecond {
			t.Fatalf("batches %v apart", now.Sub(prev))
		}
		prev = now
	}
}
