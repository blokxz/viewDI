package fixtures

import (
	"net/url"
	"strings"
	"testing"
)

func TestSanitizeJSON(t *testing.T) {
	s := NewSanitizer("nas.example.lan", "alice")
	in := `{
	  "Name": "nas.example.lan",
	  "Env": ["PATH=/usr/bin", "DB_PASSWORD=hunter2", "API_URL=http://203.0.113.9:8080"],
	  "IPAM": {"Config": [{"Subnet": "172.20.0.0/16", "Gateway": "172.20.0.1"}]},
	  "IPAddress": "172.20.0.3",
	  "GlobalIPv6Address": "2a01:4f8:1:2::abcd",
	  "MacAddress": "a4:5e:60:12:34:56",
	  "Mounts": [{"Source": "/home/alice/app/config"}],
	  "Labels": {"owner": "alice", "com.docker.compose.project": "web"},
	  "token": "s3cr3t",
	  "certificate": "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----",
	  "loopback": "127.0.0.1",
	  "time": "12:00:01"
	}`
	out := string(s.JSON([]byte(in)))
	for _, leak := range []string{"nas.example.lan", "hunter2", "203.0.113.9", "172.20.", "2a01:4f8", "a4:5e:60", "alice", "s3cr3t", "MIIB"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q:\n%s", leak, out)
		}
	}
	for _, keep := range []string{
		`"PATH=/usr/bin"`, `"DB_PASSWORD=<redacted>"`, `"10.1.0.0/16"`, `"10.1.0.1"`, `"10.1.0.3"`,
		`"127.0.0.1"`, `"12:00:01"`, `/home/user/app/config`, `"com.docker.compose.project": "web"`,
		`"02:00:00:00:00:01"`, `"viewdi-host"`,
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("missing %s:\n%s", keep, out)
		}
	}
}

func TestSanitizeConsistent(t *testing.T) {
	s := NewSanitizer()
	a, b := s.Text("10.10.0.1"), s.Text("gateway 10.10.0.1/24 member 10.10.0.21")
	if !strings.Contains(b, a+"/24") || !strings.Contains(b, strings.TrimSuffix(a, "1")+"21") {
		t.Fatalf("inconsistent: %q %q", a, b)
	}
}

func FuzzSanitize(f *testing.F) {
	for _, seed := range []string{
		`{"a":"192.168.1.1","Env":["X=1"]}`, `not json 10.0.0.1 fe80::1 aa:bb:cc:dd:ee:ff`,
		`[1,2,{"token":"x"}]`, `{"time":"2026-10-29T12:00:00Z"}`, `"::ffff:1.2.3.4"`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		s := NewSanitizer("hostname")
		out := s.JSON(data)
		if len(out) > 4*len(data)+1024 {
			t.Fatalf("output grew from %d to %d bytes", len(data), len(out))
		}
		_ = s.Text(string(data))
	})
}

func TestSanitizeFrames(t *testing.T) {
	frame := func(kind byte, p string) []byte {
		h := []byte{kind, 0, 0, 0, 0, 0, 0, byte(len(p))}
		return append(h, p...)
	}
	in := append(frame(1, "connect from 192.168.1.20 ok\n"), frame(2, "mac 00:11:22:33:44:55\n")...)
	out := NewSanitizer().Frames(in)
	// Walk the frames: sizes must match the rewritten payloads.
	var texts []string
	for len(out) >= 8 {
		n := int(out[7]) | int(out[6])<<8
		texts = append(texts, string(out[8:8+n]))
		out = out[8+n:]
	}
	if len(texts) != 2 || strings.Contains(texts[0], "192.168.1.20") || strings.Contains(texts[1], "00:11:22") {
		t.Fatalf("frames %q", texts)
	}
	if len(out) != 0 {
		t.Fatalf("%d trailing bytes", len(out))
	}
}

func TestBodyKind(t *testing.T) {
	cases := []struct {
		eng, path, query string
		want             int
	}{
		{"docker", "/containers/abc/stats", "stream=true", kindJSONLines},
		{"docker", "/containers/abc/stats", "one-shot=true&stream=false", kindJSON},
		{"docker", "/containers/abc/logs", "stdout=1", kindFrames},
		{"incus", "/1.0/instances/x/console", "project=default", kindText},
		{"incus", "/1.0/instances/x/logs/lxc.log", "project=default", kindText},
		{"incus", "/1.0/instances/x/state", "project=default", kindJSON},
	}
	for _, c := range cases {
		q, _ := url.ParseQuery(c.query)
		if got := bodyKind(c.eng, c.path, q); got != c.want {
			t.Errorf("bodyKind(%s %s?%s) = %d, want %d", c.eng, c.path, c.query, got, c.want)
		}
	}
}
