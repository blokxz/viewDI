package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
)

// selectRow puts the Table cursor on name.
func (d *driver) selectRow(name string) {
	d.t.Helper()
	d.press("3", "home")
	for i, in := range d.table().Rows() {
		if in.Name == name {
			for range i {
				d.press("down")
			}
			return
		}
	}
	d.t.Fatalf("no row %s", name)
}

func newBrowseDriver(t *testing.T, opened *[]string, mutates ...func(*Options)) *driver {
	t.Helper()
	open := func(url string) error { *opened = append(*opened, url); return nil }
	d := newDriver(t, append([]func(*Options){withEngines(nil), func(o *Options) { o.OpenAddress = open }}, mutates...)...)
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	return d
}

func TestBrowseOpensTheBestAddress(t *testing.T) {
	var opened []string
	d := newBrowseDriver(t, &opened)

	d.selectRow("web-lab") // an Incus container with an address, nothing published
	d.press("b")
	if len(opened) != 1 || opened[0] != "http://10.10.0.21" {
		t.Fatalf("opened %q", opened)
	}
	if got := d.lastToast(); !strings.Contains(got, "Opened http://10.10.0.21") {
		t.Fatalf("toast %q", got)
	}

	d.selectRow("web-api") // Docker: the published port wins over the internal address
	d.press("b")
	if opened[len(opened)-1] != "http://localhost:8080" {
		t.Fatalf("opened %q", opened)
	}
}

func TestBrowseChooseListsEveryAddress(t *testing.T) {
	var opened []string
	d := newBrowseDriver(t, &opened)
	d.selectRow("web-api")
	d.press("B")
	screen := d.screen()
	for _, want := range []string{"localhost:8080", "http://172.20.0.3", "https://172.20.0.3", "http://172.21.0.5"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("picker lacks %q:\n%s", want, screen)
		}
	}
	d.press("down", "enter") // the second entry
	if len(opened) != 1 || opened[0] != "http://172.20.0.3" {
		t.Fatalf("opened %q", opened)
	}
}

func TestBrowseExplainsWhyNot(t *testing.T) {
	var opened []string
	d := newBrowseDriver(t, &opened)

	d.selectRow("old-backup") // stopped
	d.press("b")
	if got := d.lastToast(); !strings.Contains(got, "is not running") {
		t.Fatalf("toast %q", got)
	}

	// A running VM that reports no address (no agent, no lease) says why.
	for _, in := range d.m.Snapshot().Instances {
		if in.Name == "k3s-node1" {
			in.Networks = nil
			d.send(ChangesMsg{Changes: []store.Change{store.InstanceUpserted{Instance: in}}})
		}
	}
	d.selectRow("k3s-node1")
	d.press("b")
	if got := d.lastToast(); !strings.Contains(got, "no incus-agent") || len(opened) != 0 {
		t.Fatalf("toast %q, opened %q", got, opened)
	}
}

func TestBrowseWorksReadOnlyAndReportsAFailure(t *testing.T) {
	var opened []string
	d := newBrowseDriver(t, &opened, func(o *Options) { o.ReadOnly = true })
	d.selectRow("web-lab")
	d.press("b")
	if len(opened) != 1 {
		t.Fatalf("--read-only must still open pages, opened %q", opened)
	}

	d2 := newDriver(t, withEngines(nil), func(o *Options) {
		o.OpenAddress = func(string) error { return errors.New(`exec: "xdg-open": executable file not found in $PATH`) }
	})
	d2.send(ChangesMsg{Changes: homelab(t, nil)})
	d2.selectRow("web-lab")
	d2.press("b")
	if got := d2.lastToast(); !strings.Contains(got, "Could not open http://10.10.0.21") {
		t.Fatalf("toast %q", got)
	}

	// Without an opener the address is shown.
	d3 := newDriver(t, withEngines(nil))
	d3.send(ChangesMsg{Changes: homelab(t, nil)})
	d3.selectRow("web-lab")
	d3.press("b")
	if got := d3.lastToast(); !strings.Contains(got, "Address: http://10.10.0.21") {
		t.Fatalf("toast %q", got)
	}
}

func TestBrowseTargetsRules(t *testing.T) {
	in := engine.Instance{
		Name: "x", Engine: engine.Docker,
		Ports: []engine.Port{
			{HostPort: 8443, ContainerPort: 443, Protocol: "tcp"},
			{HostIP: "192.168.1.5", HostPort: 3000, ContainerPort: 3000, Protocol: "tcp"},
			{HostPort: 53, ContainerPort: 53, Protocol: "udp"}, // not a web port
			{ContainerPort: 80, Protocol: "tcp"},               // not published
		},
		Networks: []engine.Attachment{{Network: "n", IPv4: "172.18.0.4"}},
	}
	var got []string
	for _, t := range browseTargets(in, nil) {
		got = append(got, t.URL)
	}
	want := "https://localhost:8443,http://192.168.1.5:3000,http://172.18.0.4,https://172.18.0.4"
	if strings.Join(got, ",") != want {
		t.Fatalf("targets %v", got)
	}
}

func TestBrowseTargetsLabelAndExtraPorts(t *testing.T) {
	in := engine.Instance{
		Name: "box", Engine: engine.Incus,
		Labels:   map[string]string{"viewdi.url": "https://{ip}:9090/system"},
		Networks: []engine.Attachment{{Network: "incusbr0", IPv4: "10.91.227.10"}},
	}
	got := browseTargets(in, []uint16{9090, 8006})
	var urls []string
	for _, tg := range got {
		urls = append(urls, tg.URL)
	}
	want := "https://10.91.227.10:9090/system,http://10.91.227.10,https://10.91.227.10," +
		"https://10.91.227.10:9090,http://10.91.227.10:9090,https://10.91.227.10:8006,http://10.91.227.10:8006"
	if strings.Join(urls, ",") != want {
		t.Fatalf("targets %v", urls)
	}

	for name, label := range map[string]string{
		"not http":        "ftp://{ip}/x",
		"javascript":      "javascript:alert(1)",
		"no address":      "https://{ip}/", // the instance below has none
		"too long":        "http://x/" + strings.Repeat("a", 600),
		"empty":           "  ",
		"relative":        "/system",
		"scheme-relative": "//10.0.0.1/",
	} {
		bare := engine.Instance{Name: "b", Labels: map[string]string{"viewdi.url": label}}
		if name != "no address" {
			bare.Networks = []engine.Attachment{{IPv4: "10.0.0.2"}}
		}
		if tg, ok := labelTarget(bare); ok {
			t.Errorf("%s: label %q accepted as %q", name, label, tg.URL)
		}
	}
}

func TestBrowseLabelWinsForB(t *testing.T) {
	var opened []string
	d := newBrowseDriver(t, &opened, func(o *Options) { o.BrowsePorts = []uint16{9090} })
	d.selectRow("web-lab") // Incus container, 10.10.0.21
	d.press("B")
	if !strings.Contains(d.screen(), "https://10.10.0.21:9090") {
		t.Fatalf("picker lacks the extra port:\n%s", d.screen())
	}
}
