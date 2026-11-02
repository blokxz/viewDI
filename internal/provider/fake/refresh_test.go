package fake

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/blokxz/viewDI/internal/engine"
)

func TestRecreateRollback(t *testing.T) {
	w := NewWorld(nil)
	Homelab(w)
	d := w.Docker
	old, _ := d.Instance("nginx-proxy")
	d.FailRecreates(errors.New("exit 1"))
	var phases []string
	id, err := d.Recreate(context.Background(), old.ID, engine.RecreateOptions{Pull: true}, func(p engine.Progress) {
		phases = append(phases, p.Phase)
	})
	if err == nil || id != "" || engine.Classify(err) != engine.ClassConflict {
		t.Fatalf("Recreate = %q, %v; want a conflict", id, err)
	}
	if !slices.Contains(phases, engine.PhaseRollingBack) {
		t.Errorf("phases %v", phases)
	}
	in, ok := d.Instance("nginx-proxy")
	if !ok || in.ID != old.ID || in.State != engine.StateRunning {
		t.Errorf("original after the rollback: %+v (ok %v)", in, ok)
	}
	if _, ok := d.Instance("nginx-proxy" + engine.OldSuffix); ok {
		t.Error("the -old name survived the rollback")
	}
	all, _ := d.ListInstances(context.Background(), engine.ListOptions{})
	if n := len(all); n != 9 {
		t.Errorf("%d containers after the rollback, want 9", n)
	}

	d.FailRecreates(nil)
	if _, err := d.Recreate(context.Background(), old.ID, engine.RecreateOptions{}, nil); err != nil {
		t.Fatalf("Recreate after FailRecreates(nil): %v", err)
	}
}

func TestRecreateRefusals(t *testing.T) {
	w := NewWorld(nil)
	Homelab(w)
	d := w.Docker
	ctx := context.Background()
	api, _ := d.Instance("web-api")
	if _, err := d.Recreate(ctx, api.ID, engine.RecreateOptions{}, nil); !errors.Is(err, engine.ErrUnsupported) {
		t.Errorf("compose container: %v", err)
	}
	px, _ := d.Instance("nginx-proxy")
	clash := px
	clash.ID, clash.Name = "", "nginx-proxy"+engine.OldSuffix
	d.Add(clash)
	if _, err := d.Recreate(ctx, px.ID, engine.RecreateOptions{}, nil); engine.Classify(err) != engine.ClassConflict || errors.Is(err, engine.ErrUnsupported) {
		t.Errorf("taken -old name: %v", err)
	}
	if in, _ := d.Instance("nginx-proxy"); in.ID != px.ID || in.State != engine.StateRunning {
		t.Errorf("a refused recreate touched the container: %+v", in)
	}
}

func TestRefreshIncusWithoutRemote(t *testing.T) {
	w := NewWorld(nil)
	Homelab(w)
	_, err := w.Incus.RefreshImage(context.Background(), engine.ImageCheck{Engine: engine.Incus, Ref: "custom/1"}, nil)
	if !errors.Is(err, engine.ErrUnsupported) {
		t.Errorf("refresh of a local alias: %v", err)
	}
}
