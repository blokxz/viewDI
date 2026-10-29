package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

// WatchStats implements engine.Engine: one synthetic sample per interval
// while the instance runs, with per-interface counters and, on Incus, disk
// usage.
func (e *Engine) WatchStats(ctx context.Context, id string, interval time.Duration) (*engine.Stream[engine.Sample], error) {
	if err := e.enter(ctx, "watch stats"); err != nil {
		return nil, err
	}
	e.mu.Lock()
	_, ok := e.instances[id]
	e.mu.Unlock()
	if !ok {
		return nil, engine.NotFound("watch stats", "instance "+id)
	}
	if interval <= 0 {
		interval = time.Second
	}
	s := engine.NewStream[engine.Sample](1)
	go func() {
		first := true
		for {
			if !first {
				select {
				case <-ctx.Done():
					s.Close(ctx.Err())
					return
				case <-e.clk.After(interval):
				}
			}
			first = false
			e.mu.Lock()
			in, alive := e.instances[id]
			fault, ferr, closed := e.fault, e.faultErr, e.closed
			var smp engine.Sample
			running := alive && in.State == engine.StateRunning
			if running && fault == faultNone && !closed {
				smp = e.detailSampleLocked(in, e.clk.Now())
			}
			e.mu.Unlock()
			switch {
			case closed:
				s.Close(io.EOF)
				return
			case fault == faultFail:
				s.Close(streamErr("watch stats", ferr))
				return
			case !alive:
				s.Close(engine.NotFound("watch stats", "instance "+id))
				return
			case fault == faultHang || !running:
				continue
			}
			if !s.Send(ctx, smp) {
				s.Close(ctx.Err())
				return
			}
		}
	}()
	return s, nil
}

// detailSampleLocked adds per-interface counters and disk usage to a sample.
func (e *Engine) detailSampleLocked(in *engine.Instance, now time.Time) engine.Sample {
	s := e.sampleLocked(in, now)
	n := uint64(len(in.Networks))
	for i := range in.Networks {
		rx, tx := s.NetRx/max(n, 1), s.NetTx/max(n, 1)
		if i == 0 {
			rx += s.NetRx % max(n, 1)
			tx += s.NetTx % max(n, 1)
		}
		s.Ifaces = append(s.Ifaces, engine.IfaceCounters{Name: fmt.Sprintf("eth%d", i), Rx: rx, Tx: tx})
	}
	if e.id == engine.Incus {
		h := uint64(hashName(in.Name))
		s.Disks = append(s.Disks, engine.DiskUsage{Name: "root", Used: 800<<20 + h%(2<<30)})
		for _, m := range in.Mounts {
			if m.Name != "" {
				s.Disks = append(s.Disks, engine.DiskUsage{Name: m.Name, Used: 64<<20 + uint64(hashName(m.Name))%(8<<30)})
			}
		}
		slices.SortFunc(s.Disks, func(a, b engine.DiskUsage) int { return strings.Compare(a.Name, b.Name) })
	}
	return s
}

// InspectRaw implements engine.Engine with a document in the engine's own
// dialect (a subset of Docker's container JSON or Incus' instance JSON).
func (e *Engine) InspectRaw(ctx context.Context, id string) ([]byte, error) {
	if err := e.enter(ctx, "inspect"); err != nil {
		return nil, err
	}
	e.mu.Lock()
	in, ok := e.instances[id]
	var c engine.Instance
	if ok {
		c = clone(*in)
	}
	e.mu.Unlock()
	if !ok {
		return nil, engine.NotFound("inspect", "instance "+id)
	}
	var doc any
	if e.id == engine.Incus {
		doc = incusDoc(c)
	} else {
		doc = dockerDoc(c)
	}
	return json.Marshal(doc)
}

func rfc(t time.Time) string {
	if t.IsZero() {
		return "0001-01-01T00:00:00Z"
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func dockerDoc(in engine.Instance) map[string]any {
	x, _ := in.Extra.(*engine.DockerExtra)
	if x == nil {
		x = &engine.DockerExtra{}
	}
	nets := map[string]any{}
	for _, a := range in.Networks {
		nets[a.Network] = map[string]any{"IPAddress": a.IPv4, "GlobalIPv6Address": a.IPv6, "MacAddress": a.MAC}
	}
	ports := map[string]any{}
	for _, p := range in.Ports {
		ports[fmt.Sprintf("%d/%s", p.ContainerPort, p.Protocol)] = []map[string]string{{"HostIp": p.HostIP, "HostPort": fmt.Sprint(p.HostPort)}}
	}
	mounts := []map[string]any{}
	for _, m := range in.Mounts {
		mounts = append(mounts, map[string]any{"Type": m.Type, "Source": m.Source, "Destination": m.Destination, "RW": !m.ReadOnly})
	}
	health := map[string]any(nil)
	if in.Health != engine.HealthNone {
		health = map[string]any{"Status": in.Health.String(), "FailingStreak": 0}
	}
	return map[string]any{
		"Id":      in.ID,
		"Name":    "/" + in.Name,
		"Created": rfc(in.Created),
		"Path":    x.Command,
		"Image":   x.ImageID,
		"State": map[string]any{
			"Status": dockerStatus(in.State), "Running": in.State == engine.StateRunning,
			"Paused": in.State == engine.StatePaused, "Pid": in.PID, "ExitCode": x.ExitCode,
			"StartedAt": rfc(in.Started), "Health": health,
		},
		"Config": map[string]any{
			"Image": in.Image, "Env": in.Env, "Labels": in.Labels, "Cmd": strings.Fields(x.Command), "Tty": false,
		},
		"HostConfig":      map[string]any{"RestartPolicy": map[string]any{"Name": x.RestartPolicy, "MaximumRetryCount": 0}},
		"NetworkSettings": map[string]any{"Networks": nets, "Ports": ports},
		"Mounts":          mounts,
	}
}

func dockerStatus(s engine.State) string {
	if s == engine.StateStopped {
		return "exited"
	}
	return s.String()
}

func incusDoc(in engine.Instance) map[string]any {
	x, _ := in.Extra.(*engine.IncusExtra)
	if x == nil {
		x = &engine.IncusExtra{}
	}
	config := map[string]string{}
	for k, v := range in.Labels {
		config["user."+k] = v
	}
	for _, kv := range in.Env {
		k, v, _ := strings.Cut(kv, "=")
		config["environment."+k] = v
	}
	if x.ImageDescription != "" {
		config["image.description"] = x.ImageDescription
	}
	devices := map[string]map[string]string{"root": {"type": "disk", "path": "/", "pool": "default"}}
	for i, a := range in.Networks {
		devices[fmt.Sprintf("eth%d", i)] = map[string]string{"type": "nic", "network": a.Network, "name": fmt.Sprintf("eth%d", i)}
	}
	for _, m := range in.Mounts {
		ro := "false"
		if m.ReadOnly {
			ro = "true"
		}
		devices[m.Name] = map[string]string{"type": "disk", "source": m.Source, "path": m.Destination, "readonly": ro}
	}
	typ := "container"
	if in.Kind == engine.KindVM {
		typ = "virtual-machine"
	}
	return map[string]any{
		"name": in.Name, "project": in.Project, "type": typ, "status": incusStatus(in.State),
		"architecture": x.Architecture, "location": x.Location, "description": x.Description,
		"ephemeral": x.Ephemeral, "profiles": x.Profiles, "created_at": rfc(in.Created),
		"config": config, "devices": devices,
		"state": map[string]any{"pid": in.PID, "started_at": rfc(in.Started)},
	}
}

func incusStatus(s engine.State) string {
	switch s {
	case engine.StateRunning:
		return "Running"
	case engine.StatePaused:
		return "Frozen"
	case engine.StateError:
		return "Error"
	}
	return "Stopped"
}
