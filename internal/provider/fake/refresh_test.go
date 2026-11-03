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

func TestRebuildIncus(t *testing.T) {
	w := NewWorld(nil)
	Homelab(w)
	i := w.Incus
	ctx := context.Background()
	lab, _ := i.Instance("web-lab")
	chk, ok := engine.CheckOf(lab)
	if !ok {
		t.Fatal("web-lab has no image check")
	}
	i.SetRemoteImage(chk.Ref, "newbuild0000000000000000000000000000000000000000000000000000abcd", "20261103")
	before, _ := i.ImageUpdateStatus(ctx, chk)
	if before.State != engine.ImageOutdated {
		t.Fatalf("before: %+v", before)
	}

	i.FailRecreates(errors.New("did not boot"))
	var phases []string
	record := func(p engine.Progress) { phases = append(phases, p.Phase) }
	if _, err := i.Recreate(ctx, lab.ID, engine.RecreateOptions{Source: chk.Ref}, record); engine.Classify(err) != engine.ClassConflict {
		t.Fatalf("a failing rebuild: %v", err)
	}
	if got, _ := i.Instance("web-lab"); got.State != lab.State {
		t.Errorf("state after the rollback: %v, want %v", got.State, lab.State)
	}
	if !slices.Contains(phases, engine.PhaseRollingBack) {
		t.Errorf("phases %v", phases)
	}
	if st, _ := i.ImageUpdateStatus(ctx, chk); st.State != engine.ImageOutdated {
		t.Errorf("a rolled back rebuild changed the image: %+v", st)
	}

	i.FailRecreates(nil)
	phases = nil
	snap, err := i.Recreate(ctx, lab.ID, engine.RecreateOptions{Source: chk.Ref}, record)
	if err != nil || snap == "" || snap[:len("viewdi-pre-rebuild-")] != "viewdi-pre-rebuild-" {
		t.Fatalf("Recreate = %q, %v", snap, err)
	}
	if got := phases[0]; got != engine.PhaseSnapshotting {
		t.Errorf("the snapshot must come first: %v", phases)
	}
	if got, _ := i.Instance("web-lab"); got.State != lab.State {
		t.Errorf("state after the rebuild: %v, want %v", got.State, lab.State)
	}
	after, _ := engine.CheckOf(mustInstance(t, i, "web-lab"))
	if st, _ := i.ImageUpdateStatus(ctx, after); st.State != engine.ImageUpToDate {
		t.Errorf("after the rebuild: %+v", st)
	}

	if _, err := i.Recreate(ctx, lab.ID, engine.RecreateOptions{}, nil); !errors.Is(err, engine.ErrUnsupported) {
		t.Errorf("no image source: %v", err)
	}
}

func mustInstance(t *testing.T, e *Engine, name string) engine.Instance {
	t.Helper()
	in, ok := e.Instance(name)
	if !ok {
		t.Fatalf("no %s", name)
	}
	return in
}
