package hub

import (
	"context"
	"errors"
	"slices"
	"strings"
)

// Static is a fixed list of repositories that answers like the Hub without a
// network: --mock and tests use it, so they never reach hub.docker.com.
type Static []Repo

// Search returns the repositories whose name or description contains query.
func (s Static) Search(_ context.Context, query string, limit int) ([]Repo, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, errors.New("type something to search for")
	}
	var out []Repo
	for _, r := range s {
		if strings.Contains(strings.ToLower(r.Ref+" "+r.Description), q) {
			out = append(out, r)
		}
	}
	return out[:min(len(out), clamp(limit))], nil
}

// Top returns the official repositories, most downloaded first.
func (s Static) Top(_ context.Context, limit int) ([]Repo, error) {
	var out []Repo
	for _, r := range s {
		if r.Official {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b Repo) int {
		switch {
		case a.Pulls > b.Pulls:
			return -1
		case a.Pulls < b.Pulls:
			return 1
		}
		return 0
	})
	return out[:min(len(out), clamp(limit))], nil
}

// Sample is the list --mock offers.
func Sample() Static {
	return Static{
		{Ref: "nginx", Description: "Official build of Nginx.", Stars: 21416, Pulls: 13_544_827_346, Official: true},
		{Ref: "alpine", Description: "A minimal Docker image based on Alpine Linux.", Stars: 11579, Pulls: 12_446_992_049, Official: true},
		{Ref: "ubuntu", Description: "Ubuntu is a Debian-based Linux operating system.", Stars: 17873, Pulls: 10_139_898_963, Official: true},
		{Ref: "redis", Description: "Redis is the world's fastest data platform.", Stars: 13300, Pulls: 8_100_000_000, Official: true},
		{Ref: "postgres", Description: "The PostgreSQL object-relational database system.", Stars: 14000, Pulls: 7_500_000_000, Official: true},
		{Ref: "grafana/grafana", Description: "The open observability platform.", Stars: 3000, Pulls: 4_000_000_000},
		{Ref: "containrrr/watchtower", Description: "A process for automating Docker container base image updates.", Stars: 2300, Pulls: 2_000_000_000},
		{Ref: "nginx/nginx-ingress", Description: "NGINX and NGINX Plus Ingress Controllers for Kubernetes.", Stars: 124, Pulls: 1_088_362_532},
	}
}
