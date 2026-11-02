package incus

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/blokxz/viewDI/internal/engine"
)

// An instance reports its addresses through the guest agent, which exists for
// containers and for VMs running incus-agent. A VM without it (OPNsense, other
// BSDs, appliances) has a MAC address and a DHCP lease on the managed bridge,
// and nothing else. The lease table of the network gives the address back.
//
// The table is one request per project and network, cached for leaseTTL, and
// only asked for when a running instance has a NIC without an address. A
// network that has no lease table (unmanaged bridges, macvlan) answers an
// error once and is not asked again for leaseMissTTL.
const (
	leaseTTL     = 15 * time.Second
	leaseMissTTL = 2 * time.Minute
	leaseTimeout = 2 * time.Second
)

type leaseKey struct{ project, network string }

type leaseEntry struct {
	at    time.Time
	ttl   time.Duration
	byMAC map[string]string // lower-case MAC → IPv4
}

type leaseCache struct {
	mu      sync.Mutex
	entries map[leaseKey]leaseEntry
	// fetch is replaced in tests.
	fetch func(ctx context.Context, project, network string) ([]api.NetworkLease, error)
	now   func() time.Time
}

func (a *Adapter) leaseFetch(ctx context.Context, project, network string) ([]api.NetworkLease, error) {
	if a.leases.fetch != nil {
		return a.leases.fetch(ctx, project, network)
	}
	return a.client(ctx, project).GetNetworkLeases(network)
}

func (a *Adapter) leaseNow() time.Time {
	if a.leases.now != nil {
		return a.leases.now()
	}
	return time.Now()
}

// addLeases fills in the IPv4 address of running instances whose NIC reports
// none, from the DHCP leases of the network, matched by MAC address.
func (a *Adapter) addLeases(ctx context.Context, list []engine.Instance) {
	need := map[leaseKey]bool{}
	for _, in := range list {
		if in.State != engine.StateRunning {
			continue
		}
		for _, at := range in.Networks {
			if at.IPv4 == "" && at.MAC != "" && at.Network != "" {
				need[leaseKey{in.Project, at.Network}] = true
			}
		}
	}
	if len(need) == 0 {
		return
	}
	tables := make(map[leaseKey]map[string]string, len(need))
	for k := range need {
		tables[k] = a.leaseTable(ctx, k)
	}
	for i := range list {
		if list[i].State != engine.StateRunning {
			continue
		}
		for j := range list[i].Networks {
			at := &list[i].Networks[j]
			if at.IPv4 != "" || at.MAC == "" {
				continue
			}
			if ip, ok := tables[leaseKey{list[i].Project, at.Network}][strings.ToLower(at.MAC)]; ok {
				at.IPv4 = ip
			}
		}
	}
}

func (a *Adapter) leaseTable(ctx context.Context, k leaseKey) map[string]string {
	c := &a.leases
	now := a.leaseNow()
	c.mu.Lock()
	if e, ok := c.entries[k]; ok && now.Sub(e.at) < e.ttl {
		c.mu.Unlock()
		return e.byMAC
	}
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, leaseTimeout)
	defer cancel()
	leases, err := a.leaseFetch(ctx, k.project, k.network)
	e := leaseEntry{at: now, ttl: leaseTTL, byMAC: map[string]string{}}
	if err != nil {
		e.ttl = leaseMissTTL
	}
	for _, l := range leases {
		if ip, perr := netip.ParseAddr(l.Address); perr == nil && ip.Is4() && l.Hwaddr != "" {
			e.byMAC[strings.ToLower(l.Hwaddr)] = l.Address
		}
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[leaseKey]leaseEntry{}
	}
	c.entries[k] = e
	c.mu.Unlock()
	return e.byMAC
}
