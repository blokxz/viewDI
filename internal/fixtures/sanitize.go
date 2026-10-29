package fixtures

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

// Redacted replaces secret values.
const Redacted = "<redacted>"

// Sanitizer rewrites recorded payloads so fixtures can be committed: IP
// addresses, MAC addresses, host and user names, environment values, tokens
// and certificates are replaced. Replacements are consistent within one
// Sanitizer (the same address always maps to the same replacement), so
// subnets, gateways and members still line up.
type Sanitizer struct {
	// Names are literal strings (host names, user names) replaced by
	// NameReplacement.
	Names           []string
	NameReplacement string

	v4  map[[2]byte]byte   // /16 network → second octet under 10.0.0.0/8
	v6  map[[8]byte]uint16 // /64 prefix → index under fd00::/16
	mac map[string]string
}

// NewSanitizer returns a sanitizer that also replaces the given names.
func NewSanitizer(names ...string) *Sanitizer {
	var keep []string
	for _, n := range names {
		if len(strings.TrimSpace(n)) >= 3 { // too-short names would mangle unrelated text
			keep = append(keep, n)
		}
	}
	// Longest first, so "host.lan" is replaced before "host".
	sort.Slice(keep, func(i, j int) bool { return len(keep[i]) > len(keep[j]) })
	return &Sanitizer{
		Names: keep, NameReplacement: "viewdi-host",
		v4: map[[2]byte]byte{}, v6: map[[8]byte]uint16{}, mac: map[string]string{},
	}
}

var (
	secretKey = regexp.MustCompile(`(?i)(pass|secret|token|key|cert|auth|credential|private)`)
	// Keys that match secretKey but hold harmless metadata.
	safeKeys = map[string]bool{
		"auth_methods": true, "auth": true, "auth_user_method": true,
		"Labels": true, "labels": true, "KernelVersion": true,
	}
	safeEnv = map[string]bool{"PATH": true, "HOME": true, "LANG": true, "TERM": true, "container": true}
	macRe   = regexp.MustCompile(`\b[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}\b`)
	ipv4Re  = regexp.MustCompile(`\b(\d{1,3}\.){3}\d{1,3}\b`)
	ipv6Re  = regexp.MustCompile(`[0-9a-fA-F]{0,4}(:[0-9a-fA-F]{0,4}){2,7}`)
	homeRe  = regexp.MustCompile(`/home/[^/"\s]+`)
)

// JSON sanitises a JSON document. Invalid JSON is sanitised as text.
func (s *Sanitizer) JSON(data []byte) []byte {
	var v any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return []byte(s.Text(string(data)))
	}
	v = s.value("", v)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return []byte(s.Text(string(data)))
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

func (s *Sanitizer) value(key string, v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if isSecretKey(k) {
				if str, ok := child.(string); ok && str != "" {
					x[k] = Redacted
					continue
				}
			}
			x[k] = s.value(k, child)
		}
		return x
	case []any:
		for i, child := range x {
			if str, ok := child.(string); ok && (key == "Env" || key == "env") {
				x[i] = s.env(str)
				continue
			}
			x[i] = s.value(key, child)
		}
		return x
	case string:
		if strings.Contains(x, "-----BEGIN") {
			return Redacted
		}
		if (key == "Env" || key == "env") && strings.Contains(x, "=") {
			return s.env(x)
		}
		return s.Text(x)
	}
	return v
}

func isSecretKey(k string) bool { return !safeKeys[k] && secretKey.MatchString(k) }

// env masks the value of a KEY=VALUE pair unless the key is harmless.
func (s *Sanitizer) env(kv string) string {
	k, _, ok := strings.Cut(kv, "=")
	if !ok || safeEnv[k] {
		return s.Text(kv)
	}
	return k + "=" + Redacted
}

// Text sanitises free text.
func (s *Sanitizer) Text(t string) string {
	for _, n := range s.Names {
		t = strings.ReplaceAll(t, n, s.NameReplacement)
	}
	t = homeRe.ReplaceAllString(t, "/home/user")
	t = macRe.ReplaceAllStringFunc(t, s.macAddr)
	t = ipv4Re.ReplaceAllStringFunc(t, s.ip)
	t = ipv6Re.ReplaceAllStringFunc(t, s.ip)
	return t
}

func (s *Sanitizer) macAddr(m string) string {
	low := strings.ToLower(m)
	if low == "00:00:00:00:00:00" || low == "ff:ff:ff:ff:ff:ff" {
		return m
	}
	if r, ok := s.mac[low]; ok {
		return r
	}
	n := len(s.mac) + 1
	r := fmt.Sprintf("02:00:00:00:%02x:%02x", n>>8&0xff, n&0xff)
	s.mac[low] = r
	return r
}

// ip maps an address, keeping loopback, unspecified, link-local and
// multicast addresses and the host part of the original, so CIDRs and
// gateways stay consistent.
func (s *Sanitizer) ip(m string) string {
	a, err := netip.ParseAddr(m)
	if err != nil || a.IsLoopback() || a.IsUnspecified() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsInterfaceLocalMulticast() {
		return m
	}
	if a.Is4() {
		b := a.As4()
		net := [2]byte{b[0], b[1]}
		n, ok := s.v4[net]
		if !ok {
			n = byte(len(s.v4) + 1)
			s.v4[net] = n
		}
		return netip.AddrFrom4([4]byte{10, n, b[2], b[3]}).String()
	}
	b := a.As16()
	var prefix [8]byte
	copy(prefix[:], b[:8])
	n, ok := s.v6[prefix]
	if !ok {
		n = uint16(len(s.v6) + 1)
		s.v6[prefix] = n
	}
	var out [16]byte
	out[0], out[1] = 0xfd, 0x00
	out[6], out[7] = byte(n>>8), byte(n)
	out[14], out[15] = b[14], b[15] // keep the low 16 bits of the host part
	return netip.AddrFrom16(out).String()
}

// Frames sanitises a Docker multiplexed log stream frame by frame, keeping
// the framing valid (payload sizes change with the replacements).
func (s *Sanitizer) Frames(data []byte) []byte {
	var out []byte
	for len(data) >= 8 {
		size := int(binary.BigEndian.Uint32(data[4:8]))
		if size > len(data)-8 {
			break
		}
		payload := []byte(s.Text(string(data[8 : 8+size])))
		var hdr [8]byte
		copy(hdr[:4], data[:4])
		binary.BigEndian.PutUint32(hdr[4:], uint32(len(payload)))
		out = append(append(out, hdr[:]...), payload...)
		data = data[8+size:]
	}
	return out
}
