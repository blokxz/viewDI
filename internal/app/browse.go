package app

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

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

// urlLabel is the Docker label (or Incus `user.` key) that names where an
// instance's web interface is. "{ip}" stands for its first IPv4 address:
//
//	docker run --label viewdi.url=https://{ip}:9090/system ...
//	incus config set NAME user.viewdi.url https://{ip}:9090/system
const urlLabel = "viewdi.url"

// maxLabelURL caps the label, which comes from the instance and is untrusted.
const maxLabelURL = 512

// labelTarget returns the address the instance's viewdi.url label names, if
// it has a usable one. Only http and https are accepted here; the browser
// opener still applies its own rules (private or local hosts only) at open
// time, so a label cannot send the browser to the internet.
func labelTarget(in engine.Instance, ok func(string) bool) (target, bool) {
	raw := strings.TrimSpace(in.Labels[urlLabel])
	if raw == "" || len(raw) > maxLabelURL {
		return target{}, false
	}
	if strings.Contains(raw, "{ip}") {
		ip := in.PrimaryIP()
		if ip == "" {
			return target{}, false
		}
		raw = strings.ReplaceAll(raw, "{ip}", ip)
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return target{}, false
	}
	for _, r := range raw {
		if r <= ' ' || r == 0x7f {
			return target{}, false
		}
	}
	if ok != nil && !ok(raw) {
		return target{}, false // a host the browser would refuse: do not offer it
	}
	return target{URL: raw, Label: raw, Detail: "set by the " + urlLabel + " label"}, true
}

// browseTargets lists where a browser can reach an instance, best guess first:
// the address its viewdi.url label names, the ports it publishes on the Docker
// host (through localhost, or the host address it is bound to), then each of
// its own IPv4 addresses. Addresses get both schemes: a web UI that only
// speaks TLS (a firewall appliance) usually redirects plain http, but not
// always, so https is offered too. extra are the ports of the [browse]
// section: well-known web interfaces (Cockpit on 9090, Proxmox on 8006…) that
// are offered on every address after the default ports.
func browseTargets(in engine.Instance, extra []uint16, ok func(string) bool) []target {
	var out []target
	seen := map[string]bool{}
	add := func(t target) {
		if !seen[t.URL] {
			seen[t.URL] = true
			out = append(out, t)
		}
	}
	if t, good := labelTarget(in, ok); good {
		add(t)
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
	for _, ip := range in.IPs() {
		for _, port := range extra {
			for _, scheme := range []string{"https", "http"} {
				url := scheme + "://" + ip + ":" + strconv.Itoa(int(port))
				add(target{URL: url, Label: url, Detail: "port " + strconv.Itoa(int(port)) + " of " + in.Name})
			}
		}
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
	targets := browseTargets(in, m.opts.BrowsePorts, m.opts.AddressOK)
	if len(targets) == 0 {
		hint := "it publishes no port and reports no address"
		if in.Engine == engine.Incus && in.Kind == engine.KindVM {
			hint = "it reports no address: no incus-agent in the VM and no DHCP lease on its bridge"
		}
		if in.Labels[urlLabel] != "" {
			hint += "; its " + urlLabel + " label is not usable (it needs an address for {ip}, and must be http(s) to a private or local host)"
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
