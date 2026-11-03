package fake

import (
	"context"
	"errors"
	"slices"
	"strings"
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
	opts := engine.RecreateOptions{Source: chk.Ref, Copy: true}

	// Snapshots stop a rebuild before anything changes.
	i.SetSnapshots("web-lab", "limpia", "antes-hostkey")
	_, err := i.Recreate(ctx, lab.ID, opts, func(engine.Progress) {})
	if engine.Classify(err) != engine.ClassConflict || !strings.Contains(err.Error(), "2 snapshots") {
		t.Fatalf("an instance with snapshots: %v", err)
	}
	if got, _ := i.Instance("web-lab"); got.State != lab.State {
		t.Errorf("a refused rebuild touched the instance: %v", got.State)
	}
	i.SetSnapshots("web-lab")

	i.FailRecreates(errors.New("did not boot"))
	var phases []string
	record := func(p engine.Progress) { phases = append(phases, p.Phase) }
	if _, err := i.Recreate(ctx, lab.ID, opts, record); engine.Classify(err) != engine.ClassConflict {
		t.Fatalf("a failing rebuild: %v", err)
	}
	if got, _ := i.Instance("web-lab"); got.State != lab.State {
		t.Errorf("state after the rollback: %v, want %v", got.State, lab.State)
	}
	if !slices.Contains(phases, engine.PhaseRollingBack) {
		t.Errorf("phases %v", phases)
	}
	all, _ := i.ListInstances(ctx, engine.ListOptions{})
	for _, in := range all {
		if strings.Contains(in.Name, "-pre-rebuild-") {
			t.Errorf("the rollback left a copy: %s", in.Name)
		}
	}
	if st, _ := i.ImageUpdateStatus(ctx, chk); st.State != engine.ImageOutdated {
		t.Errorf("a rolled back rebuild changed the image: %+v", st)
	}

	i.FailRecreates(nil)
	phases = nil
	keep, err := i.Recreate(ctx, lab.ID, opts, record)
	if err != nil || !strings.HasPrefix(keep, "web-lab-pre-rebuild-") {
		t.Fatalf("Recreate = %q, %v", keep, err)
	}
	if !slices.Contains(phases, engine.PhaseCopying) || phases[len(phases)-1] == engine.PhaseRollingBack {
		t.Errorf("phases %v", phases)
	}
	if c, ok := i.Instance(keep); !ok || c.State != engine.StateStopped {
		t.Errorf("the copy: %+v (%v)", c, ok)
	}
	if got, _ := i.Instance("web-lab"); got.State != lab.State {
		t.Errorf("state after the rebuild: %v, want %v", got.State, lab.State)
	}
	after, _ := engine.CheckOf(mustInstance(t, i, "web-lab"))
	if st, _ := i.ImageUpdateStatus(ctx, after); st.State != engine.ImageUpToDate {
		t.Errorf("after the rebuild: %+v", st)
	}

	// Without a copy none is kept.
	i.SetRemoteImage(chk.Ref, "newer00000000000000000000000000000000000000000000000000000000ffff", "20261104")
	none, err := i.Recreate(ctx, lab.ID, engine.RecreateOptions{Source: chk.Ref}, nil)
	if err != nil || none != "" {
		t.Errorf("without a copy: %q, %v", none, err)
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
