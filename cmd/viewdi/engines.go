package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/provider/docker"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/provider/incus"
	"github.com/blokxz/viewDI/internal/supervisor"
)

// startEngines builds the engines (fake ones with --mock), starts one
// supervisor per engine, and returns the change source for the UI plus a
// stop function that waits for every engine goroutine to end.
func startEngines(ctx context.Context, f flags, cfg config.Config, paths config.Paths, st config.State, logger *slog.Logger) (*supervisor.Group, func(), []config.Warning) {
	ctx, cancel := context.WithCancel(ctx)
	var targets []supervisor.Target
	var warns []config.Warning
	var closers []func() error
	done := make(chan struct{})

	if f.Mock != "" {
		sc := fake.Scenarios()[f.Mock]
		w := fake.NewWorld(nil)
		sc.Build(w)
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
	return g, func() {
		cancel()
		g.Wait()
		<-done
		for _, c := range closers {
			_ = c()
		}
	}, warns
}
