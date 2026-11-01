package main

import (
	"time"

	"github.com/blokxz/viewDI/internal/config"
)

// labOptions loads the labs and returns how to save them. Mock mode keeps
// its labs in memory, seeded with a demo lab, so fake machines never reach
// the user's labs.json.
func labOptions(f flags, paths config.Paths) (config.Labs, func(config.Labs) error, []config.Warning) {
	if f.Mock != "" {
		return demoLabs(), nil, nil
	}
	path := paths.LabsFile()
	ls, w := config.LoadLabs(path)
	return ls, func(ls config.Labs) error { return config.SaveLabs(path, ls) }, w
}

// demoLabs is the lab the homelab scenario starts with: a web stack that
// spans both engines.
func demoLabs() config.Labs {
	var ls config.Labs
	id, _ := ls.Add("web-stack")
	docker := func(name string) config.LabMember { return config.LabMember{Engine: "docker", Name: name} }
	incus := func(name string) config.LabMember {
		return config.LabMember{Engine: "incus", Project: "default", Name: name}
	}
	for _, l := range [][2]config.LabMember{
		{docker("nginx-proxy"), docker("web-api")},
		{docker("web-api"), docker("web-db")},
		{docker("web-api"), docker("redis-cache")},
		{docker("nginx-proxy"), incus("web-lab")},
		{incus("web-lab"), docker("postgres-dev")},
	} {
		_, _ = ls.Link(id, l[0], l[1])
	}
	return ls
}

// animationTick is the link animation's frame interval: 0 turns it off
// (disabled in config.toml, or NO_COLOR set).
func animationTick(cfg config.Config, noColor bool) time.Duration {
	if !cfg.Tree.Animation || noColor {
		return 0
	}
	return time.Duration(cfg.Tree.AnimationMS) * time.Millisecond
}
