package docker

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"github.com/blokxz/viewDI/internal/engine"
)

// Compose labels.
const (
	labelComposeProject = "com.docker.compose.project"
	labelComposeService = "com.docker.compose.service"
)

func state(s container.ContainerState) engine.State {
	switch s {
	case container.StateCreated:
		return engine.StateCreated
	case container.StateRunning:
		return engine.StateRunning
	case container.StatePaused:
		return engine.StatePaused
	case container.StateRestarting:
		return engine.StateRestarting
	case container.StateRemoving:
		return engine.StateRemoving
	case container.StateExited:
		return engine.StateStopped
	case container.StateDead:
		return engine.StateDead
	}
	return engine.StateUnknown
}

func health(s container.HealthStatus) engine.Health {
	switch s {
	case container.Starting:
		return engine.HealthStarting
	case container.Healthy:
		return engine.HealthHealthy
	case container.Unhealthy:
		return engine.HealthUnhealthy
	}
	return engine.HealthNone
}

// healthFromStatus reads "(healthy)" style suffixes of the status text, for
// daemons whose list response has no Health field.
func healthFromStatus(status string) engine.Health {
	switch {
	case strings.Contains(status, "(healthy)"):
		return engine.HealthHealthy
	case strings.Contains(status, "(unhealthy)"):
		return engine.HealthUnhealthy
	case strings.Contains(status, "(health: starting)"):
		return engine.HealthStarting
	}
	return engine.HealthNone
}

// exitCode parses "Exited (1) 3 hours ago".
func exitCode(status string) int {
	var code int
	if _, err := fmt.Sscanf(status, "Exited (%d)", &code); err != nil {
		return 0
	}
	return code
}

func fromSummary(s container.Summary) engine.Instance {
	name := s.ID
	if len(s.Names) > 0 {
		name = strings.TrimPrefix(s.Names[0], "/")
	}
	in := engine.Instance{
		ID:      s.ID,
		Engine:  engine.Docker,
		Name:    name,
		Kind:    engine.KindContainer,
		State:   state(s.State),
		Status:  s.Status,
		Image:   image(s.Image),
		Created: time.Unix(s.Created, 0).UTC(),
		Labels:  s.Labels,
	}
	if s.Health != nil {
		in.Health = health(s.Health.Status)
	} else {
		in.Health = healthFromStatus(s.Status)
	}
	if s.NetworkSettings != nil {
		in.Networks = attachments(s.NetworkSettings.Networks)
	}
	for _, p := range s.Ports {
		in.Ports = append(in.Ports, engine.Port{
			HostIP: addr(p.IP), HostPort: p.PublicPort, ContainerPort: p.PrivatePort, Protocol: p.Type,
		})
	}
	sortPorts(in.Ports)
	in.Mounts = mounts(s.Mounts)
	in.Extra = &engine.DockerExtra{
		ComposeProject: s.Labels[labelComposeProject],
		ComposeService: s.Labels[labelComposeService],
		Command:        s.Command,
		ExitCode:       exitCode(s.Status),
	}
	return in
}

func fromInspect(c container.InspectResponse) engine.Instance {
	in := engine.Instance{
		ID:      c.ID,
		Engine:  engine.Docker,
		Name:    strings.TrimPrefix(c.Name, "/"),
		Kind:    engine.KindContainer,
		Created: parseTime(c.Created).Truncate(time.Second),
	}
	extra := &engine.DockerExtra{Command: strings.TrimSpace(c.Path + " " + strings.Join(c.Args, " "))}
	if c.State != nil {
		in.State = state(c.State.Status)
		in.Status = string(c.State.Status)
		extra.ExitCode = c.State.ExitCode
		if c.State.Health != nil {
			in.Health = health(c.State.Health.Status)
		}
		if in.State == engine.StateRunning {
			in.Started = parseTime(c.State.StartedAt)
			in.PID = c.State.Pid
		}
	}
	if c.Config != nil {
		in.Image = image(c.Config.Image)
		in.Labels = c.Config.Labels
		extra.ComposeProject = c.Config.Labels[labelComposeProject]
		extra.ComposeService = c.Config.Labels[labelComposeService]
	}
	if c.HostConfig != nil {
		extra.RestartPolicy = string(c.HostConfig.RestartPolicy.Name)
	}
	if c.NetworkSettings != nil {
		in.Networks = attachments(c.NetworkSettings.Networks)
		for port, binds := range c.NetworkSettings.Ports {
			base := engine.Port{ContainerPort: port.Num(), Protocol: string(port.Proto())}
			if len(binds) == 0 {
				in.Ports = append(in.Ports, base)
			}
			for _, b := range binds {
				p := base
				p.HostIP = addr(b.HostIP)
				var hp uint16
				_, _ = fmt.Sscanf(b.HostPort, "%d", &hp)
				p.HostPort = hp
				in.Ports = append(in.Ports, p)
			}
		}
		sortPorts(in.Ports)
	}
	in.Mounts = mounts(c.Mounts)
	in.Extra = extra
	return in
}

func attachments(nets map[string]*network.EndpointSettings) []engine.Attachment {
	var out []engine.Attachment
	for name, ep := range nets {
		if ep == nil {
			continue
		}
		out = append(out, engine.Attachment{
			Network: name,
			IPv4:    addr(ep.IPAddress),
			IPv6:    addr(ep.GlobalIPv6Address),
			MAC:     ep.MacAddress.String(),
		})
	}
	slices.SortFunc(out, func(a, b engine.Attachment) int { return cmp.Compare(a.Network, b.Network) })
	return out
}

func mounts(ms []container.MountPoint) []engine.Mount {
	var out []engine.Mount
	for _, m := range ms {
		src := m.Source
		if m.Name != "" && m.Type == "volume" {
			src = m.Name
		}
		out = append(out, engine.Mount{Type: string(m.Type), Source: src, Destination: m.Destination, ReadOnly: !m.RW})
	}
	slices.SortFunc(out, func(a, b engine.Mount) int { return cmp.Compare(a.Destination, b.Destination) })
	return out
}

func sortPorts(ps []engine.Port) {
	slices.SortFunc(ps, func(a, b engine.Port) int {
		return cmp.Or(cmp.Compare(a.ContainerPort, b.ContainerPort), cmp.Compare(a.Protocol, b.Protocol),
			cmp.Compare(a.HostIP, b.HostIP), cmp.Compare(a.HostPort, b.HostPort))
	})
}

func addr(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	return a.String()
}

// image shortens bare image IDs ("sha256:9f3c…") to 12 hex digits.
func image(ref string) string {
	if rest, ok := strings.CutPrefix(ref, "sha256:"); ok && len(rest) > 12 {
		return rest[:12]
	}
	return ref
}

func parseTime(s string) time.Time {
	if s == "" || strings.HasPrefix(s, "0001-") {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
