//go:build fixturerec

// Command fixturerec records real Docker or Incus API responses into
// testdata/fixtures/<engine>/<version>/ for the adapter tests. Every request
// the adapter makes goes through a sanitising, recording proxy; events are
// captured for a short window, during which you can start or stop an
// instance to record a lifecycle sequence.
//
//	go run -tags fixturerec ./cmd/fixturerec -engine docker
//	go run -tags fixturerec ./cmd/fixturerec -engine incus -events 20s
//
// Review the output before committing it: the sanitiser replaces addresses,
// host and user names, environment values and secrets, but it cannot know
// every private string in your setup (pass extra ones with -redact).
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/gorilla/websocket"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/fixtures"
	"github.com/blokxz/viewDI/internal/provider/docker"
	"github.com/blokxz/viewDI/internal/provider/incus"
)

func main() {
	eng := flag.String("engine", "docker", "docker or incus")
	socket := flag.String("socket", "", "daemon socket path (default: discovered)")
	out := flag.String("out", "testdata/fixtures", "fixtures root")
	window := flag.Duration("events", 15*time.Second, "how long to record events")
	redact := flag.String("redact", "", "comma-separated extra strings to replace")
	flag.Parse()
	if err := run(*eng, *socket, *out, *window, *redact); err != nil {
		fmt.Fprintln(os.Stderr, "fixturerec:", err)
		os.Exit(1)
	}
}

func run(eng, socket, out string, window time.Duration, redact string) error {
	if socket == "" {
		switch eng {
		case "docker":
			host, _ := docker.Discover("", "", xdg.RuntimeDir, os.Getenv)
			socket = strings.TrimPrefix(host, "unix://")
		case "incus":
			socket, _ = incus.Discover("", "", os.Getenv, incus.StatProbe)
		default:
			return fmt.Errorf("unknown engine %q", eng)
		}
	}
	names := []string{}
	if h, err := os.Hostname(); err == nil {
		names = append(names, h)
	}
	if u, err := user.Current(); err == nil {
		names = append(names, u.Username)
	}
	if redact != "" {
		names = append(names, strings.Split(redact, ",")...)
	}
	san := fixtures.NewSanitizer(names...)
	rec := fixtures.NewRecorder(eng, socket, san)
	tmp, err := os.MkdirTemp("", "fixturerec-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	proxy := filepath.Join(tmp, "proxy.sock")
	stop, err := rec.Listen(proxy)
	if err != nil {
		return err
	}
	defer stop()

	var e engine.Engine
	if eng == "docker" {
		e, err = docker.New("unix://" + proxy)
	} else {
		// The user socket keeps its name so the adapter knows it is restricted.
		if incus.IsUserSocket(socket) {
			p := filepath.Join(tmp, "unix.socket.user")
			if err := os.Rename(proxy, p); err != nil {
				return err
			}
			proxy = p
		}
		e, err = incus.New(context.Background(), proxy)
	}
	if err != nil {
		return err
	}
	defer e.Close()

	ctx := context.Background()
	info, err := e.Info(ctx)
	if err != nil {
		return fmt.Errorf("info: %w", err)
	}
	san.Names = append(san.Names, info.Host)
	list, err := e.ListInstances(ctx, engine.ListOptions{})
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, p := range info.Projects {
		if _, err := e.ListInstances(ctx, engine.ListOptions{Project: p}); err != nil {
			return fmt.Errorf("list project %s: %w", p, err)
		}
	}
	ids := make([]string, 0, len(list))
	for _, in := range list {
		if _, err := e.Inspect(ctx, in.ID); err != nil {
			return fmt.Errorf("inspect %s: %w", in.Name, err)
		}
		ids = append(ids, in.ID)
	}
	if _, err := e.Stats(ctx, ids); err != nil {
		return fmt.Errorf("stats: %w", err)
	}
	if err := recordDetail(ctx, e, list); err != nil {
		return err
	}
	if _, err := e.Networks(ctx, engine.ListOptions{}); err != nil {
		return fmt.Errorf("networks: %w", err)
	}
	if _, err := e.Volumes(ctx, engine.ListOptions{}); err != nil {
		return fmt.Errorf("volumes: %w", err)
	}
	_ = e.Ping(ctx)

	fmt.Printf("recording events for %s — start/stop an instance now…\n", window)
	ectx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	if eng == "docker" {
		feed, err := e.Events(ectx)
		if err != nil {
			return fmt.Errorf("events: %w", err)
		}
		for ev := range feed.C() {
			fmt.Println(" ", ev.Action, ev.Name)
		}
	} else if err := incusEvents(ectx, socket, rec); err != nil {
		return fmt.Errorf("events: %w", err)
	}

	version := info.Version
	if eng == "docker" {
		version = "v" + info.APIVersion
	}
	dir := filepath.Join(out, eng, version)
	idx := fixtures.Index{APIVersion: info.APIVersion, ServerVersion: info.Version, Note: "Recorded with cmd/fixturerec on " + time.Now().Format(time.DateOnly)}
	if err := rec.Write(dir, idx); err != nil {
		return err
	}
	fmt.Println("wrote", dir, "— review it before committing")
	return nil
}

// incusEvents records raw lifecycle events from the real socket's websocket.
// recordDetail records the detail-view streams (raw inspect, log tail,
// two seconds of stats) of up to three running instances.
func recordDetail(ctx context.Context, e engine.Engine, list []engine.Instance) error {
	n := 0
	for _, in := range list {
		if in.State != engine.StateRunning || n == 3 {
			continue
		}
		n++
		if _, err := e.InspectRaw(ctx, in.ID); err != nil {
			return fmt.Errorf("inspect raw %s: %w", in.Name, err)
		}
		logs, err := e.Logs(ctx, in.ID, engine.LogOptions{Tail: 20})
		if err != nil {
			return fmt.Errorf("logs %s: %w", in.Name, err)
		}
		for range logs.C() {
		}
		sctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		stats, err := e.WatchStats(sctx, in.ID, time.Second)
		if err != nil {
			cancel()
			return fmt.Errorf("watch stats %s: %w", in.Name, err)
		}
		for range stats.C() {
		}
		cancel()
	}
	return nil
}

func incusEvents(ctx context.Context, socket string, rec *fixtures.Recorder) error {
	d := websocket.Dialer{NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var nd net.Dialer
		return nd.DialContext(ctx, "unix", socket)
	}}
	conn, resp, err := d.DialContext(ctx, "ws://unix.socket/1.0/events?type=lifecycle&all-projects=true", http.Header{})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return nil // window over
		}
		fmt.Println(" ", string(data))
		rec.AddEvent(data)
	}
}
