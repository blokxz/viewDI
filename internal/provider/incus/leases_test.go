package incus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/blokxz/viewDI/internal/engine"
)

func vm(name, mac, ip string) engine.Instance {
	return engine.Instance{
		Engine: engine.Incus, Name: name, Project: "default", State: engine.StateRunning,
		Networks: []engine.Attachment{{Network: "incusbr0", MAC: mac, IPv4: ip}},
	}
}

func TestLeasesFillTheAddressOfAnAgentlessVM(t *testing.T) {
	calls := 0
	now := time.Unix(1000, 0)
	a := &Adapter{}
	a.leases.now = func() time.Time { return now }
	a.leases.fetch = func(_ context.Context, project, network string) ([]api.NetworkLease, error) {
		calls++
		if project != "default" || network != "incusbr0" {
			t.Errorf("asked %s/%s", project, network)
		}
		return []api.NetworkLease{
			{Hostname: "opnsense", Hwaddr: "BC:24:11:AA:BB:CC", Address: "10.10.0.50", Type: "dynamic"},
			{Hostname: "v6only", Hwaddr: "bc:24:11:00:00:01", Address: "fd42::5", Type: "dynamic"},
		}, nil
	}
	list := []engine.Instance{
		vm("opnsense", "bc:24:11:aa:bb:cc", ""),    // agentless: filled from the lease
		vm("web", "bc:24:11:00:00:02", "10.0.0.7"), // the agent already reported it
		vm("v6", "bc:24:11:00:00:01", ""),          // only an IPv6 lease: stays empty
	}
	a.addLeases(context.Background(), list)
	if got := list[0].PrimaryIP(); got != "10.10.0.50" {
		t.Fatalf("opnsense = %q", got)
	}
	if list[1].PrimaryIP() != "10.0.0.7" || list[2].PrimaryIP() != "" {
		t.Fatalf("web=%q v6=%q", list[1].PrimaryIP(), list[2].PrimaryIP())
	}
	if calls != 1 {
		t.Fatalf("%d lease requests for one network", calls)
	}

	// Cached for leaseTTL, asked again after.
	a.addLeases(context.Background(), []engine.Instance{vm("opnsense", "bc:24:11:aa:bb:cc", "")})
	if calls != 1 {
		t.Fatalf("cache missed: %d requests", calls)
	}
	now = now.Add(leaseTTL + time.Second)
	a.addLeases(context.Background(), []engine.Instance{vm("opnsense", "bc:24:11:aa:bb:cc", "")})
	if calls != 2 {
		t.Fatalf("stale cache kept: %d requests", calls)
	}
}

func TestLeasesAreNotAskedForWhenNothingIsMissing(t *testing.T) {
	a := &Adapter{}
	a.leases.fetch = func(context.Context, string, string) ([]api.NetworkLease, error) {
		t.Fatal("leases requested although every address is known")
		return nil, nil
	}
	stopped := vm("off", "bc:24:11:00:00:09", "")
	stopped.State = engine.StateStopped
	a.addLeases(context.Background(), []engine.Instance{vm("a", "bc:24:11:00:00:02", "10.0.0.7"), stopped})
}

func TestNetworkWithoutLeasesIsNotAskedAgainSoon(t *testing.T) {
	calls := 0
	now := time.Unix(0, 0)
	a := &Adapter{}
	a.leases.now = func() time.Time { return now }
	a.leases.fetch = func(context.Context, string, string) ([]api.NetworkLease, error) {
		calls++
		return nil, errors.New("not a managed network")
	}
	for range 3 {
		a.addLeases(context.Background(), []engine.Instance{vm("x", "bc:24:11:00:00:03", "")})
	}
	if calls != 1 {
		t.Fatalf("%d requests for a network without leases", calls)
	}
	now = now.Add(leaseMissTTL + time.Second)
	a.addLeases(context.Background(), []engine.Instance{vm("x", "bc:24:11:00:00:03", "")})
	if calls != 2 {
		t.Fatalf("never retried: %d", calls)
	}
}
