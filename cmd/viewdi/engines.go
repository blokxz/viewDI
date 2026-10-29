package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/jobs"
	"github.com/blokxz/viewDI/internal/metrics/cgroup"
	"github.com/blokxz/viewDI/internal/metrics/sampler"
	"github.com/blokxz/viewDI/internal/provider/docker"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/provider/incus"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/streams"
	"github.com/blokxz/viewDI/internal/supervisor"
)

// source is what the UI talks to: the supervisor group for engine state,
// the sampler for metrics and the stream registry for the Detail view.
type source struct {
	*supervisor.Group
	sampler *sampler.Sampler
	streams *streams.Registry
	jobs    *jobs.Runner
	images  images
}

// images serves the create form's image lists from the engines.
type images map[engine.ID]engine.Engine

func (i images) Images(ctx context.Context, e engine.ID) ([]engine.Image, error) {
	eng, ok := i[e]
	if !ok {
		return nil, fmt.Errorf("no %s engine", e.Label())
	}
	return eng.Images(ctx)
}

func (i images) SearchImages(ctx context.Context, e engine.ID, q string) ([]engine.RemoteImage, error) {
	eng, ok := i[e]
	if !ok {
		return nil, fmt.Errorf("no %s engine", e.Label())
	}
	return eng.SearchImages(ctx, q)
}

func (s source) Metrics() <-chan store.MetricsSampled { return s.sampler.Out() }
func (s source) SetInterval(d time.Duration)          { s.sampler.SetInterval(d) }

// startEngines builds the engines (fake ones with --mock), starts one
// supervisor per engine and the metrics sampler, and returns the source for
// the UI plus a stop function that waits for every engine goroutine to end.
func startEngines(ctx context.Context, f flags, cfg config.Config, paths config.Paths, st config.State, refresh time.Duration, logger *slog.Logger) (source, func(), []config.Warning) {
	ctx, cancel := context.WithCancel(ctx)
	var targets []supervisor.Target
	var warns []config.Warning
	var closers []func() error
	done := make(chan struct{})

	if f.Mock != "" {
		sc := fake.Scenarios()[f.Mock]
		w := fake.NewWorld(nil)
		sc.Build(w)
		// Mock actions take a moment, so their pending state and create and
		// import progress are visible.
		w.Docker.SetActionDelay(700 * time.Millisecond)
		w.Incus.SetActionDelay(time.Second)
		go func() {
			defer close(done)
			sc.Run(ctx, w, func(s fake.Step) { logger.Debug("scenario", "name", sc.Name, "step", s.Desc) })
		}()
		targets = []supervisor.Target{
			{Engine: w.Docker, Endpoint: "mock://docker"},
			{Engine: w.Incus, Endpoint: "mock://incus"},
		}
	} else {
		close(done)
		host, src := docker.Discover(f.DockerHost, cfg.Docker.Host, paths.RuntimeDir, os.Getenv)
		logger.Info("docker endpoint", "host", host, "source", src)
		if d, err := docker.New(host); err != nil {
			warns = append(warns, config.Warning{Msg: fmt.Sprintf("Docker disabled: %v", err)})
		} else {
			targets = append(targets, supervisor.Target{Engine: d, Endpoint: host})
			closers = append(closers, d.Close)
		}
		sock, isrc := incus.Discover(f.IncusSocket, cfg.Incus.Socket, os.Getenv, incus.StatProbe)
		logger.Info("incus endpoint", "socket", sock, "source", isrc)
		if i, err := incus.New(ctx, sock); err != nil {
			warns = append(warns, config.Warning{Msg: fmt.Sprintf("Incus disabled: %v", err)})
		} else {
			targets = append(targets, supervisor.Target{Engine: i, Endpoint: sock})
			closers = append(closers, i.Close)
		}
	}

	g := supervisor.NewGroup(supervisor.Options{Logger: logger}, targets...)
	g.SetInitialProject(st.IncusProject)
	g.Start(ctx)

	// cgroup files are read directly on real hosts with a unified
	// hierarchy; fake engines (and other hosts) use the engine APIs.
	var cg *cgroup.Reader
	if f.Mock == "" {
		if r := cgroup.New(); r.Available() {
			cg = r
		} else {
			logger.Info("cgroup v2 not readable, sampling through the engine APIs")
		}
	}
	srcs := make([]sampler.Source, 0, len(g.Supervisors()))
	for _, sup := range g.Supervisors() {
		srcs = append(srcs, sup)
	}
	smp := sampler.New(sampler.Options{Interval: refresh, Cgroup: cg, Logger: logger}, srcs...)
	sampled := make(chan struct{})
	go func() { defer close(sampled); smp.Run(ctx) }()

	engines := make([]engine.Engine, 0, len(targets))
	imgs := images{}
	for _, t := range targets {
		engines = append(engines, t.Engine)
		imgs[t.Engine.ID()] = t.Engine
	}
	reg := streams.New(ctx, streams.Options{Logger: logger}, engines...)
	runner := jobs.New(ctx, jobs.Options{Logger: logger}, engines...)

	return source{Group: g, sampler: smp, streams: reg, jobs: runner, images: imgs}, func() {
		cancel()
		g.Wait()
		reg.Wait()
		runner.Wait()
		<-sampled
		<-done
		for _, c := range closers {
			_ = c()
		}
	}, warns
}
