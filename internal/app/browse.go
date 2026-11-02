package app

import (
	"fmt"
	"net/netip"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/picker"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// pickerBrowse identifies the "choose the address" picker.
const pickerBrowse = "browse"

// addressOpenedMsg reports whether an address opened in the browser.
type addressOpenedMsg struct {
	url string
	err error
}

// target is one address a browser can be sent to.
type target struct {
	URL, Label, Detail string
}

// httpsPorts are the container ports that speak TLS by convention.
var httpsPorts = map[uint16]bool{443: true, 8443: true, 9443: true}

// browseTargets lists where a browser can reach an instance, best guess first:
// the ports it publishes on the Docker host (through localhost, or the host
// address it is bound to), then each of its own IPv4 addresses. Addresses get
// both schemes: a web UI that only speaks TLS (a firewall appliance) usually
// redirects plain http, but not always, so https is offered too.
func browseTargets(in engine.Instance) []target {
	var out []target
	seen := map[string]bool{}
	add := func(t target) {
		if !seen[t.URL] {
			seen[t.URL] = true
			out = append(out, t)
		}
	}
	for _, p := range in.Ports {
		if p.HostPort == 0 || (p.Protocol != "" && p.Protocol != "tcp") {
			continue
		}
		scheme := "http"
		if httpsPorts[p.ContainerPort] || httpsPorts[p.HostPort] {
			scheme = "https"
		}
		host := "localhost"
		if ip, err := netip.ParseAddr(p.HostIP); err == nil && !ip.IsUnspecified() && ip.Is4() {
			host = ip.String()
		}
		url := scheme + "://" + host + ":" + strconv.Itoa(int(p.HostPort))
		add(target{URL: url, Label: url, Detail: fmt.Sprintf("published port %d → %d", p.HostPort, p.ContainerPort)})
	}
	for _, ip := range in.IPs() {
		add(target{URL: "http://" + ip, Label: "http://" + ip, Detail: "address of " + in.Name})
		add(target{URL: "https://" + ip, Label: "https://" + ip, Detail: "address of " + in.Name})
	}
	return out
}

// browse is the b and B keys: open the selected instance in the default
// browser (b: the best address, B: choose one).
func (m Model) browse(choose bool) (tea.Model, tea.Cmd) {
	in, ok := m.selected()
	if !ok {
		return m, toast.Show(theme.LevelInfo, "No instance selected")
	}
	if in.State != engine.StateRunning {
		return m, toast.Show(theme.LevelInfo, textsafe.Clean(in.Name)+" is not running")
	}
	targets := browseTargets(in)
	if len(targets) == 0 {
		hint := "it publishes no port and reports no address"
		if in.Engine == engine.Incus && in.Kind == engine.KindVM {
			hint = "it reports no address: no incus-agent in the VM and no DHCP lease on its bridge"
		}
		return m, toast.Show(theme.LevelWarn, textsafe.Clean(in.Name)+": "+hint)
	}
	if !choose || len(targets) == 1 {
		return m, m.openAddress(targets[0].URL)
	}
	items := make([]picker.Item, 0, len(targets))
	for _, t := range targets {
		items = append(items, picker.Item{ID: t.URL, Label: t.Label, Detail: t.Detail})
	}
	return m, modal.Open(picker.New(m.keys, pickerBrowse, "Open "+textsafe.Clean(in.Name)+" in the browser", items, ""))
}

// openAddress sends url to the browser in a command, or shows it in a toast
// when the program was built without an opener.
func (m Model) openAddress(url string) tea.Cmd {
	open := m.opts.OpenAddress
	if open == nil {
		return toast.Show(theme.LevelInfo, "Address: "+textsafe.Clean(url))
	}
	return func() tea.Msg { return addressOpenedMsg{url: url, err: open(url)} }
}

func (m Model) addressOpened(msg addressOpenedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, toast.Show(theme.LevelWarn, "Could not open "+textsafe.Clean(msg.url)+": "+textsafe.Clean(msg.err.Error()))
	}
	return m, toast.Show(theme.LevelOK, "Opened "+textsafe.Clean(msg.url)+" in your browser")
}

func isBrowseAction(a string) bool { return a == keys.ActBrowse || a == keys.ActBrowseChoose }
