package incus

import (
	"cmp"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/blokxz/viewDI/internal/engine"
)

func state(c api.StatusCode) engine.State {
	switch c {
	case api.Running, api.Freezing, api.Thawed, api.Stopping:
		return engine.StateRunning
	case api.Stopped, api.Starting:
		return engine.StateStopped
	case api.Frozen:
		return engine.StatePaused
	case api.Error:
		return engine.StateError
	}
	return engine.StateUnknown
}

func fromInstance(in api.InstanceFull, defaultProject string) engine.Instance {
	project := in.Project
	if project == "" {
		project = defaultProject
	}
	out := engine.Instance{
		ID:      project + "/" + in.Name,
		Engine:  engine.Incus,
		Name:    in.Name,
		Project: project,
		Kind:    engine.KindContainer,
		State:   state(in.StatusCode),
		Status:  in.Status,
		Image:   imageName(in.Config),
		Created: in.CreatedAt.UTC(),
	}
	if in.Type == string(api.InstanceTypeVM) {
		out.Kind = engine.KindVM
	}
	if out.State == engine.StateRunning {
		switch {
		case in.State != nil && !in.State.StartedAt.IsZero():
			out.Started = in.State.StartedAt.UTC()
		case !in.LastUsedAt.IsZero():
			out.Started = in.LastUsedAt.UTC() // servers without state.started_at
		default:
			out.Started = out.Created
		}
	}
	for k, v := range in.Config {
		if name, ok := strings.CutPrefix(k, "user."); ok {
			if out.Labels == nil {
				out.Labels = map[string]string{}
			}
			out.Labels[name] = v
		}
	}
	devs := in.ExpandedDevices
	if devs == nil {
		devs = in.Devices
	}
	var st map[string]api.InstanceStateNetwork
	if in.State != nil {
		st = in.State.Network
	}
	for _, dn := range slices.Sorted(maps.Keys(devs)) {
		d := devs[dn]
		switch d["type"] {
		case "nic":
			out.Networks = append(out.Networks, attachment(dn, d, st))
		case "disk":
			if d["path"] == "/" {
				continue // root disk
			}
			src := d["source"]
			if d["pool"] != "" && !strings.HasPrefix(src, "/") {
				src = d["pool"] + "/" + src
			}
			out.Mounts = append(out.Mounts, engine.Mount{Type: "disk", Source: src, Destination: d["path"], ReadOnly: d["readonly"] == "true"})
		case "proxy":
			if p, ok := proxyPort(d["listen"], d["connect"]); ok {
				out.Ports = append(out.Ports, p)
			}
		}
	}
	slices.SortFunc(out.Ports, func(a, b engine.Port) int { return cmp.Compare(a.HostPort, b.HostPort) })
	out.Extra = &engine.IncusExtra{
		Profiles:     slices.Clone(in.Profiles),
		Location:     in.Location,
		Description:  in.Description,
		Ephemeral:    in.Ephemeral,
		Architecture: in.Architecture,
		BaseImage:    in.Config["volatile.base_image"],
	}
	return out
}

func attachment(dev string, d map[string]string, st map[string]api.InstanceStateNetwork) engine.Attachment {
	a := engine.Attachment{Network: cmp.Or(d["network"], d["parent"], dev), MAC: d["hwaddr"]}
	iface := cmp.Or(d["name"], dev)
	n, ok := st[iface]
	if !ok {
		return a
	}
	if n.Hwaddr != "" {
		a.MAC = n.Hwaddr
	}
	for _, addr := range n.Addresses {
		if addr.Scope != "global" {
			continue
		}
		switch {
		case addr.Family == "inet" && a.IPv4 == "":
			a.IPv4 = addr.Address
		case addr.Family == "inet6" && a.IPv6 == "":
			a.IPv6 = addr.Address
		}
	}
	return a
}

// proxyPort maps a proxy device ("tcp:0.0.0.0:80" → "tcp:127.0.0.1:8080").
func proxyPort(listen, connect string) (engine.Port, bool) {
	lproto, laddr, ok1 := strings.Cut(listen, ":")
	_, caddr, ok2 := strings.Cut(connect, ":")
	if !ok1 || !ok2 {
		return engine.Port{}, false
	}
	lhost, lport, err1 := net.SplitHostPort(laddr)
	_, cport, err2 := net.SplitHostPort(caddr)
	hp, err3 := strconv.ParseUint(lport, 10, 16)
	cp, err4 := strconv.ParseUint(cport, 10, 16)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return engine.Port{}, false
	}
	return engine.Port{HostIP: lhost, HostPort: uint16(hp), ContainerPort: uint16(cp), Protocol: lproto}, true
}

// imageName builds "debian/12" from the image.* keys Incus copies from the
// image, falling back to its description or base image fingerprint.
func imageName(cfg map[string]string) string {
	os := strings.ToLower(cfg["image.os"])
	ver := cmp.Or(cfg["image.version"], cfg["image.release"])
	switch {
	case os != "" && ver != "":
		return os + "/" + strings.ToLower(ver)
	case os != "":
		return os
	case cfg["image.description"] != "":
		return cfg["image.description"]
	case len(cfg["volatile.base_image"]) >= 12:
		return cfg["volatile.base_image"][:12]
	}
	return ""
}

