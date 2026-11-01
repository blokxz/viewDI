package main

import (
	"context"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/provider/fake"
)

// The demo lab names machines of the homelab scenario, so --mock shows
// live links rather than missing members.
func TestDemoLabsMatchHomelab(t *testing.T) {
	w := fake.NewWorld(clock.NewFake(time.Unix(0, 0)))
	fake.Homelab(w)
	have := map[config.LabMember]bool{}
	for _, e := range []*fake.Engine{w.Docker, w.Incus} {
		ins, err := e.ListInstances(context.Background(), engine.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range ins {
			have[config.LabMember{Engine: string(in.Engine), Project: in.Project, Name: in.Name}] = true
		}
	}
	ls := demoLabs()
	if len(ls.Labs) != 1 || len(ls.Labs[0].Links) == 0 {
		t.Fatalf("demo labs %+v", ls)
	}
	for _, m := range ls.Labs[0].Members {
		if !have[m] {
			t.Errorf("demo member %s is not a homelab machine", m)
		}
	}
}

func TestAnimationTick(t *testing.T) {
	on := config.Config{Tree: config.TreeConfig{Animation: true, AnimationMS: 200}}
	off := config.Config{Tree: config.TreeConfig{Animation: false, AnimationMS: 200}}
	for _, c := range []struct {
		cfg     config.Config
		noColor bool
		want    time.Duration
	}{
		{on, false, 200 * time.Millisecond},
		{on, true, 0},
		{off, false, 0},
	} {
		if got := animationTick(c.cfg, c.noColor); got != c.want {
			t.Errorf("animationTick(%+v, %v) = %v, want %v", c.cfg.Tree, c.noColor, got, c.want)
		}
	}
}
