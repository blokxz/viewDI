package engine

import (
	"reflect"
	"testing"
)

func TestParsePorts(t *testing.T) {
	got, err := ParsePorts("8080:80, 127.0.0.1:5432:5432 53:53/udp,9000")
	if err != nil {
		t.Fatal(err)
	}
	want := []Port{
		{HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
		{HostIP: "127.0.0.1", HostPort: 5432, ContainerPort: 5432, Protocol: "tcp"},
		{HostPort: 53, ContainerPort: 53, Protocol: "udp"},
		{ContainerPort: 9000, Protocol: "tcp"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	for _, bad := range []string{"0", "x:80", "80:70000", "1:2:3:4", "80/icmp", "80:"} {
		if _, err := ParsePorts(bad); err == nil {
			t.Errorf("ParsePorts(%q) accepted", bad)
		}
	}
}

func TestParseEnvVolumesLabels(t *testing.T) {
	env, err := ParseEnv("A=1, B_2=x=y")
	if err != nil || !reflect.DeepEqual(env, []string{"A=1", "B_2=x=y"}) {
		t.Fatalf("env %v %v", env, err)
	}
	if _, err := ParseEnv("1A=x"); err == nil {
		t.Error("bad env name accepted")
	}
	vols, err := ParseVolumes("data:/var/lib/data, /srv/conf:/etc/app:ro")
	if err != nil {
		t.Fatal(err)
	}
	want := []Mount{{Type: "volume", Source: "data", Destination: "/var/lib/data"}, {Type: "bind", Source: "/srv/conf", Destination: "/etc/app", ReadOnly: true}}
	if !reflect.DeepEqual(vols, want) {
		t.Fatalf("vols %+v", vols)
	}
	for _, bad := range []string{"data", "data:rel", "a:/b:rx", ":/x"} {
		if _, err := ParseVolumes(bad); err == nil {
			t.Errorf("ParseVolumes(%q) accepted", bad)
		}
	}
	labels, err := ParseLabels("tier=web owner=me")
	if err != nil || labels["tier"] != "web" || labels["owner"] != "me" {
		t.Fatalf("labels %v %v", labels, err)
	}
	if l, _ := ParseLabels(" "); l != nil {
		t.Error("empty labels must be nil")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		e    ID
		name string
		ok   bool
	}{
		{Docker, "", true},
		{Docker, "web_1.x", true},
		{Docker, "-x", false},
		{Incus, "", false},
		{Incus, "web-1", true},
		{Incus, "1web", false},
		{Incus, "web-", false},
		{Incus, "web_1", false},
	}
	for _, c := range cases {
		if err := ValidateName(c.e, c.name); (err == nil) != c.ok {
			t.Errorf("ValidateName(%s, %q) = %v", c.e, c.name, err)
		}
	}
	for ref, ok := range map[string]bool{"nginx:1.27": true, "ghcr.io/me/api@sha256:ab": true, "images:debian/12": true, "": false, "bad ref": false} {
		if err := ValidateImage(ref); (err == nil) != ok {
			t.Errorf("ValidateImage(%q) = %v", ref, err)
		}
	}
	for m, ok := range map[string]bool{"": true, "2GiB": true, "512MiB": true, "50%": true, "2G": false, "lots": false} {
		if err := ValidateMemory(m); (err == nil) != ok {
			t.Errorf("ValidateMemory(%q) = %v", m, err)
		}
	}
}

func FuzzParsers(f *testing.F) {
	for _, s := range []string{"8080:80/udp", "a=b", "x:/y:ro", "1.2.3.4:1:2"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if ps, err := ParsePorts(s); err == nil {
			for _, p := range ps {
				if p.ContainerPort == 0 {
					t.Fatalf("port 0 from %q", s)
				}
			}
		}
		_, _ = ParseEnv(s)
		if vs, err := ParseVolumes(s); err == nil {
			for _, v := range vs {
				if v.Destination == "" || v.Destination[0] != '/' {
					t.Fatalf("relative destination from %q", s)
				}
			}
		}
		_, _ = ParseLabels(s)
	})
}
