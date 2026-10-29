package fake

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/engine/enginetest"
)

func TestCreatePullProgress(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	e := New(engine.Docker, clk)
	e.SetActionDelay(4 * time.Second)
	var (
		mu  sync.Mutex
		got []engine.Progress
	)
	type result struct {
		id  string
		err error
	}
	done := make(chan result, 1)
	go func() {
		id, err := e.Create(context.Background(), engine.CreateSpec{Name: "web", Image: "nginx:1.27", Pull: true}, func(p engine.Progress) {
			mu.Lock()
			got = append(got, p)
			mu.Unlock()
		})
		done <- result{id, err}
	}()
	// Four pull steps take half the delay each, then "creating" all of it.
	for _, d := range []time.Duration{2 * time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second, 4 * time.Second} {
		if !clk.WaitPending(1, enginetest.Timeout) {
			t.Fatal("create not waiting")
		}
		clk.Advance(d)
	}
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 5 || got[0].Phase != "pulling" || got[3] != (engine.Progress{Phase: "pulling", Current: 100 << 20, Total: 100 << 20}) || got[4].Phase != "creating" {
		t.Errorf("progress %+v", got)
	}
	for i := 1; i < 4; i++ {
		if got[i].Current <= got[i-1].Current {
			t.Errorf("progress goes back at %d: %+v", i, got)
		}
	}
	if !e.hasImage("nginx:1.27") {
		t.Error("pulled image not listed")
	}
	if in, ok := e.Instance("web"); !ok || in.ID != r.id || in.State != engine.StateCreated {
		t.Errorf("instance %+v", in)
	}
}

func TestCreateCanceledMidPull(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	e := New(engine.Docker, clk)
	e.SetActionDelay(4 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := e.Create(ctx, engine.CreateSpec{Name: "web", Image: "nginx:1.27", Pull: true}, nil)
		done <- err
	}()
	if !clk.WaitPending(1, enginetest.Timeout) {
		t.Fatal("create not waiting")
	}
	cancel()
	if err := <-done; engine.Classify(err) != engine.ClassCanceled {
		t.Fatalf("err %v", err)
	}
	if _, ok := e.Instance("web"); ok || e.hasImage("nginx:1.27") {
		t.Error("a canceled create left state behind")
	}
}
