package main

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// openAddress opens the web interface of an instance (the b and B keys) in the
// default browser. Unlike release notes, which must be https, these are the
// plain http addresses of machines on your own networks, so the rule is about
// where they point instead: only a private, loopback, link-local or CGNAT
// IPv4 address, or localhost. The URL is built from what the daemons report,
// which a hostile container could influence, so it must never become a way to
// send the browser to the internet or to another scheme.
func openAddress(raw string) error {
	u, err := safeAddress(raw)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "xdg-open", u).Run()
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func safeAddress(raw string) (string, error) {
	if raw == "" || len(raw) > maxURL {
		return "", errors.New("refusing to open an address of that length")
	}
	for _, r := range raw {
		if r <= ' ' || r == 0x7f {
			return "", errors.New("refusing to open an address with control characters or spaces")
		}
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return "", fmt.Errorf("refusing to open a malformed address: %w", err)
	case u.Scheme != "http" && u.Scheme != "https":
		return "", errors.New("refusing to open an address that is not http or https")
	case u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "":
		return "", errors.New("refusing to open an address with credentials, a query or a fragment")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return "", errors.New("refusing to open an address with an invalid port")
		}
	}
	host := u.Hostname()
	if host == "localhost" {
		return u.String(), nil
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.Is4() {
		return "", errors.New("refusing to open an address that is not an IPv4 address or localhost")
	}
	if !(ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip)) {
		return "", fmt.Errorf("refusing to open %s: not a private or local address", strings.TrimSpace(host))
	}
	return u.String(), nil
}
