package engine

import "testing"

func TestIncusAlias(t *testing.T) {
	for _, tc := range []struct{ os, release, variant, want string }{
		{"Debian", "bookworm", "", "images:debian/bookworm/default"},
		{"Ubuntu", "noble", "cloud", "images:ubuntu/noble/cloud"},
		{"Archlinux", "current", "default", "images:archlinux/current/default"},
		{"", "bookworm", "", ""},
		{"Debian", "", "", ""},
		{"De/bian", "x", "", ""},
		{"Debian", "book worm", "", ""},
	} {
		if got := IncusAlias(tc.os, tc.release, tc.variant); got != tc.want {
			t.Errorf("IncusAlias(%q, %q, %q) = %q, want %q", tc.os, tc.release, tc.variant, got, tc.want)
		}
	}
}

func TestCheckOf(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Instance
		want ImageCheck
		ok   bool
	}{
		{"docker tag", Instance{Image: "nginx:1.27", Extra: &DockerExtra{}}, ImageCheck{Engine: Docker, Ref: "nginx:1.27"}, true},
		{"docker by id", Instance{Image: "sha256:abc", Extra: &DockerExtra{}}, ImageCheck{}, false},
		{"docker by digest", Instance{Image: "nginx@sha256:abc", Extra: &DockerExtra{}}, ImageCheck{}, false},
		{"docker empty", Instance{Extra: &DockerExtra{}}, ImageCheck{}, false},
		{
			"incus vm", Instance{Kind: KindVM, Extra: &IncusExtra{ImageAlias: "images:ubuntu/noble/default", ImageArch: "amd64", BaseImage: "8be0"}},
			ImageCheck{Engine: Incus, Ref: "images:ubuntu/noble/default", Arch: "amd64", VM: true, Local: "8be0"}, true,
		},
		{"incus local image", Instance{Extra: &IncusExtra{BaseImage: "8be0"}}, ImageCheck{}, false},
		{"no extra", Instance{Image: "nginx"}, ImageCheck{}, false},
	} {
		got, ok := CheckOf(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: CheckOf = %+v, %v; want %+v, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}
