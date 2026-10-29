package docker

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/network"

	"github.com/blokxz/viewDI/internal/engine"
)

func TestContainerConfig(t *testing.T) {
	spec := engine.CreateSpec{
		Name: "web", Image: "nginx:1.27", Env: []string{"A=1"}, Labels: map[string]string{"k": "v"},
		Ports: []engine.Port{
			{HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
			{HostIP: "127.0.0.1", HostPort: 53, ContainerPort: 53, Protocol: "udp"},
			{ContainerPort: 9000, Protocol: "tcp"},
		},
		Volumes:       []engine.Mount{{Source: "/srv/www", Destination: "/usr/share/nginx/html", ReadOnly: true}, {Source: "data", Destination: "/data"}},
		Network:       "web_default",
		RestartPolicy: "unless-stopped",
	}
	cfg, host, err := containerConfig(spec)
	if err != nil {
		t.Fatal(err)
	}
	p80, p53, p9000 := network.MustParsePort("80/tcp"), network.MustParsePort("53/udp"), network.MustParsePort("9000/tcp")
	switch {
	case cfg.Image != "nginx:1.27" || !slices.Equal(cfg.Env, spec.Env) || cfg.Labels["k"] != "v":
		t.Errorf("config %+v", cfg)
	case len(cfg.ExposedPorts) != 3:
		t.Errorf("exposed %v", cfg.ExposedPorts)
	case len(host.PortBindings[p80]) != 1 || host.PortBindings[p80][0].HostPort != "8080":
		t.Errorf("80 %+v", host.PortBindings[p80])
	case len(host.PortBindings[p53]) != 1 || host.PortBindings[p53][0].HostIP != netip.MustParseAddr("127.0.0.1"):
		t.Errorf("53 %+v", host.PortBindings[p53])
	case len(host.PortBindings[p9000]) != 0:
		t.Errorf("9000 is exposed only: %+v", host.PortBindings[p9000])
	case !slices.Equal(host.Binds, []string{"/srv/www:/usr/share/nginx/html:ro", "data:/data"}):
		t.Errorf("binds %v", host.Binds)
	case string(host.RestartPolicy.Name) != "unless-stopped" || string(host.NetworkMode) != "web_default":
		t.Errorf("host %+v", host)
	}
	if _, _, err := containerConfig(engine.CreateSpec{Ports: []engine.Port{{HostIP: "nope", ContainerPort: 80, Protocol: "tcp"}}}); engine.Classify(err) != engine.ClassConflict {
		t.Errorf("bad host IP: %v", err)
	}
}

func TestLoadResult(t *testing.T) {
	for _, tc := range []struct{ in, want, err string }{
		{in: `{"stream":"Loaded image: a:1\n"}{"stream":"Loaded image: b:2\n"}`, want: "a:1, b:2"},
		{in: `{"stream":"Loaded image ID: sha256:abc\n"}`, want: "sha256:abc"},
		{in: `{"status":"sha256:def"}`, want: "sha256:def"},
		{in: `{"errorDetail":{"message":"invalid tar"},"error":"invalid tar"}`, err: "invalid tar"},
		{in: `{"stream":"nothing\n"}`, err: "no image"},
		{in: `{not json`, err: "invalid"},
	} {
		got, err := loadResult(strings.NewReader(tc.in))
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: err %v, want %q", tc.in, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: %q, %v", tc.in, got, err)
		}
	}
}

func TestPullProgress(t *testing.T) {
	srv, a := serve(t, fixtureSets(t)[0])
	var got []engine.Progress
	if err := a.pull(context.Background(), "nginx:1.27", func(p engine.Progress) { got = append(got, p) }); err != nil {
		t.Fatal(err)
	}
	if m := srv.Misses(); len(m) > 0 {
		t.Fatalf("misses %v", m)
	}
	want := []engine.Progress{
		{Phase: "pulling", Current: 100, Total: 1000},
		{Phase: "pulling", Current: 150, Total: 1500},
		{Phase: "pulling", Current: 1050, Total: 1500},
		{Phase: "pulling", Current: 1500, Total: 1500},
	}
	if !slices.Equal(got, want) {
		t.Errorf("progress %+v", got)
	}
	err := a.pull(context.Background(), "nginx:9.99", func(engine.Progress) {})
	if err == nil || errors.Is(err, context.Canceled) {
		t.Errorf("missing tag: %v", err)
	}
}
