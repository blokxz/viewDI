package fixtures

import (
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
