package incus

import (
	"os"
	"strings"
	"testing"

	"github.com/blokxz/viewDI/internal/engine"
)

func TestParseIndex(t *testing.T) {
	f, err := os.Open("../../../testdata/simplestreams/streams/v1/images.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := parseIndex(f, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	want := []engine.RemoteImage{
		{Ref: "images:alpine/3.20", Description: "Alpine 3.20 amd64"},
		{Ref: "images:debian/12", Description: "Debian 12 amd64", VM: true},
		{Ref: "images:debian/12/cloud", Description: "Debian 12 amd64 (cloud)", VM: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i].Ref != want[i].Ref || got[i].VM != want[i].VM || !strings.EqualFold(got[i].Description, want[i].Description) {
			t.Errorf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
	if _, err := parseIndex(strings.NewReader("{"), "amd64"); err == nil {
		t.Error("truncated index parsed")
	}
}

func TestOpProgress(t *testing.T) {
	for _, tc := range []struct {
		meta map[string]any
		want engine.Progress
		ok   bool
	}{
		{map[string]any{"download_progress": "rootfs: 45% (12.3MB/s)"}, engine.Progress{Phase: "downloading", Current: 45, Total: 100}, true},
		{map[string]any{"create_instance_from_image_unpack_progress": "Unpack: 7%"}, engine.Progress{Phase: "create_instance_from_image_unpack", Current: 7, Total: 100}, true},
		{map[string]any{"fs_progress": "80%"}, engine.Progress{Phase: "fs", Current: 80, Total: 100}, true},
		{map[string]any{"download_progress": "rootfs: 12.3MB"}, engine.Progress{}, false},
		{map[string]any{"fingerprint": "abc"}, engine.Progress{}, false},
		{nil, engine.Progress{}, false},
	} {
		got, ok := opProgress(tc.meta)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%v: %+v %v", tc.meta, got, ok)
		}
	}
}

func TestInstancePost(t *testing.T) {
	post, err := instancePost(engine.CreateSpec{
		Name: "lab", Image: "images:debian/12", Kind: engine.KindVM, Profiles: []string{"default"},
		CPU: 2, Memory: "1GiB", Pool: "fast", Network: "incusbr0",
	})
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case post.Name != "lab" || post.Type != "virtual-machine" || len(post.Profiles) != 1:
		t.Errorf("post %+v", post)
	case post.Source.Alias != "debian/12" || post.Source.Server != Remotes["images"] || post.Source.Protocol != "simplestreams":
		t.Errorf("source %+v", post.Source)
	case post.Config["limits.cpu"] != "2" || post.Config["limits.memory"] != "1GiB":
		t.Errorf("config %v", post.Config)
	case post.Devices["root"]["pool"] != "fast" || post.Devices["eth0"]["network"] != "incusbr0":
		t.Errorf("devices %v", post.Devices)
	}
	local, err := instancePost(engine.CreateSpec{Name: "c1", Image: "alpine/3.20"})
	switch {
	case err != nil:
		t.Fatal(err)
	case local.Type != "container" || local.Source.Server != "" || local.Source.Alias != "alpine/3.20" || len(local.Devices) != 0 || len(local.Config) != 0:
		t.Errorf("local %+v", local)
	}
	if _, err := instancePost(engine.CreateSpec{Name: "x", Image: "nope:debian/12"}); engine.Classify(err) != engine.ClassConflict {
		t.Errorf("unknown remote: %v", err)
	}
}
