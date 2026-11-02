package updates

import (
	"slices"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

func TestCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"29.1.2":                        "v29.1.2",
		"v6.0.5":                        "v6.0.5",
		"6.18":                          "v6.18.0",
		"docker-v29.1.2":                "v29.1.2",
		"5:29.1.2-1~debian.12~bookworm": "v29.1.2",
		"1:6.0.5-1":                     "v6.0.5",
		"3:29.1.2-1.fc41":               "v29.1.2",
		"29.1.2-rc.1":                   "v29.1.2",
		"1.2.3.4":                       "v1.2.3",
		"":                              "",
		"latest":                        "",
		"(none)":                        "",
		"6.0.5-0ubuntu1~24.04":          "v6.0.5",
		"202610281200":                  "v202610281200.0.0",
	} {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLevelOf(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		sec  bool
		want Level
	}{
		{"29.0.1", "29.0.2", false, LevelPatch},
		{"29.0.1", "29.1.0", false, LevelMinor},
		{"28.5.2", "29.0.0", false, LevelMajor},
		{"6.0.5", "6.0.6", true, LevelSecurity},
		{"unknown", "29.0.0", false, LevelMinor},
	} {
		if got := LevelOf(tc.a, tc.b, tc.sec); got != tc.want {
			t.Errorf("LevelOf(%s, %s, %v) = %v, want %v", tc.a, tc.b, tc.sec, got, tc.want)
		}
	}
	if !Newer("29.0.1", "29.1.2") || Newer("29.1.2", "29.1.2") || Newer("29.1.2", "29.0.1") || Newer("x", "1.0") {
		t.Error("Newer")
	}
	if ParseLevel("SECURITY") != LevelSecurity || ParseLevel("bogus") != LevelMinor || ParseLevel("patch") != LevelPatch {
		t.Error("ParseLevel")
	}
}

func TestIncusChannel(t *testing.T) {
	for _, tc := range []struct{ v, override, want string }{
		{"6.0.5", "auto", "LTS"},
		{"6.18", "auto", "feature"},
		{"6.0.5", "stable", "feature"},
		{"6.18", "lts", "LTS"},
		{"", "", "feature"},
	} {
		if got := IncusChannel(tc.v, tc.override); got != tc.want {
			t.Errorf("IncusChannel(%q, %q) = %q, want %q", tc.v, tc.override, got, tc.want)
		}
	}
}

func TestIgnored(t *testing.T) {
	pats := []string{"postgres:*", "registry.local/*", "debian/*/*"}
	for ref, want := range map[string]bool{
		"postgres:17":                    true,
		"postgres":                       false,
		"registry.local/app:1":           true,
		"registry.local/team/app:1":      true,
		"registry.localhost/app:1":       false,
		"images:debian/bookworm/default": true,
		"images:ubuntu/noble/default":    false,
		"nginx:1.27":                     false,
	} {
		if got := Ignored(pats, ref); got != want {
			t.Errorf("Ignored(%q) = %v, want %v", ref, got, want)
		}
	}
	if !Ignored([]string{"debian/*"}, "images:debian/bookworm/default") {
		t.Error("debian/* does not cover the alias")
	}
	if Ignored([]string{"[bad"}, "x") {
		t.Error("a malformed pattern matched")
	}
}

func TestDiff(t *testing.T) {
	r := Report{
		Engines: []EngineStatus{
			{Engine: engine.Incus, Installed: "6.0.5", Available: "6.0.6", Level: LevelSecurity},
			{Engine: engine.Docker, Installed: "29.1.2"},
		},
		Images: []ImageUpdate{{Check: engine.ImageCheck{Engine: engine.Docker, Ref: "nginx:1.27"}, Available: "sha256:b"}},
	}
	seen := r.Diff(nil)
	if len(r.New) != 2 || r.New[0].Text != "Incus 6.0.6 (security)" || r.New[1].Key != "image/docker/nginx:1.27" {
		t.Fatalf("first diff: %+v", r.New)
	}
	r2 := r
	r2.Diff(seen)
	if len(r2.New) != 0 {
		t.Fatalf("same report again: %+v", r2.New)
	}
	r3 := r
	r3.Images = []ImageUpdate{{Check: engine.ImageCheck{Engine: engine.Docker, Ref: "nginx:1.27"}, Available: "sha256:c"}}
	r3.Diff(seen)
	if len(r3.New) != 1 || r3.New[0].Version != "sha256:c" {
		t.Fatalf("a newer digest is new: %+v", r3.New)
	}
	if want := []string{"engine/incus@6.0.6", "image/docker/nginx:1.27@sha256:b"}; !slices.Equal(seen, want) {
		t.Errorf("seen %v", seen)
	}
	vm := ImageUpdate{Check: engine.ImageCheck{Engine: engine.Incus, Ref: "images:ubuntu/noble/default", Arch: "amd64", VM: true}}
	if vm.Key() != "image/incus/images:ubuntu/noble/default/amd64/vm" {
		t.Errorf("key %q", vm.Key())
	}
}

func TestDecisions(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	var d Decisions
	d = d.Snooze("engine/docker", now)
	if d.Visible("engine/docker", "29.2.0", now.Add(6*24*time.Hour)) {
		t.Error("snoozed update visible within a week")
	}
	if !d.Visible("engine/docker", "29.2.0", now.Add(8*24*time.Hour)) {
		t.Error("snooze did not expire")
	}
	if d2 := d.Snooze("engine/docker", now.Add(time.Hour)); !d2.Visible("engine/docker", "x", now.Add(time.Hour)) {
		t.Error("z again must unsnooze")
	}
	d = d.Ignore("image/docker/nginx:1.27", "sha256:b")
	if d.Visible("image/docker/nginx:1.27", "sha256:b", now) {
		t.Error("ignored version visible")
	}
	if !d.Visible("image/docker/nginx:1.27", "sha256:c", now) {
		t.Error("a newer version must show again")
	}
	if d3 := d.Ignore("image/docker/nginx:1.27", "sha256:b"); !d3.Visible("image/docker/nginx:1.27", "sha256:b", now) {
		t.Error("I again must unignore")
	}
	if _, ok := d.Snoozed["engine/docker"]; !ok {
		t.Error("Ignore must not change the receiver's snoozes")
	}
	var big Decisions
	for i := range MaxDecisions + 10 {
		big = big.Snooze(string(rune('a'+i%26))+time.Duration(i).String(), now.Add(time.Duration(i)*time.Second))
	}
	if len(big.Snoozed) > MaxDecisions {
		t.Errorf("%d snoozes kept", len(big.Snoozed))
	}
}
