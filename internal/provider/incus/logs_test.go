package incus

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/blokxz/viewDI/internal/engine"
)

func texts(lines []engine.LogLine) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return out
}

func TestDiffer(t *testing.T) {
	d := &differ{src: engine.Stdout}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	first := d.next([]byte("a\nb\nc\npartial"), now, true)
	if got := texts(first); !slices.Equal(got, []string{"a", "b", "c"}) || !first[0].Time.IsZero() {
		t.Fatalf("first = %q (time %v)", got, first[0].Time)
	}
	if got := d.next([]byte("a\nb\nc\npartial line\nd\n"), now, false); !slices.Equal(texts(got), []string{"partial line", "d"}) || !got[0].Time.Equal(now) {
		t.Fatalf("second = %q", texts(got))
	}
	if got := d.next([]byte("a\nb\nc\npartial line\nd\n"), now, false); len(got) != 0 {
		t.Fatalf("unchanged log gave %q", texts(got))
	}
	// The ring buffer rotated: old lines fell off the front.
	if got := d.next([]byte("c\npartial line\nd\ne\n"), now, false); !slices.Equal(texts(got), []string{"e"}) {
		t.Fatalf("rotated = %q", texts(got))
	}
	// Cleared (instance restarted): everything is new.
	if got := d.next([]byte("boot\n"), now, false); !slices.Equal(texts(got), []string{"boot"}) {
		t.Fatalf("cleared = %q", texts(got))
	}
}

func TestOverlapNeedsContext(t *testing.T) {
	// A repeated last line must not anchor on an older copy of itself.
	prev := []string{"x", "ok", "y", "ok"}
	cur := []string{"x", "ok", "y", "ok", "z", "ok"}
	if got := overlap(prev, cur); got != 4 {
		t.Fatalf("overlap = %d, want 4", got)
	}
}

func TestLogsFromFixture(t *testing.T) {
	_, a := serve(t, fixtureSets(t)[0])
	s, err := a.Logs(context.Background(), "default/web-lab", engine.LogOptions{Tail: 4})
	if err != nil {
		t.Fatal(err)
	}
	var lines []engine.LogLine
	for l := range s.C() {
		lines = append(lines, l)
	}
	if s.Err() != nil || len(lines) != 6 {
		t.Fatalf("%d lines, err %v: %q", len(lines), s.Err(), texts(lines))
	}
	if lines[0].Source != engine.System || !strings.Contains(lines[0].Text, "cgfsng") {
		t.Errorf("first line %+v, want the runtime log", lines[0])
	}
	if lines[5].Source != engine.Stdout || lines[5].Text != "" { // the prompt line has no newline yet
		t.Errorf("last line %+v", lines[5])
	}
	if !strings.Contains(lines[2].Text, "Started nginx") {
		t.Errorf("console line %q", lines[2].Text)
	}
}

func TestWatchStatsFromFixture(t *testing.T) {
	_, a := serve(t, fixtureSets(t)[0])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := a.WatchStats(ctx, "default/web-lab", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	smp := <-s.C()
	cancel()
	for range s.C() {
	}
	if len(smp.Ifaces) != 1 || smp.Ifaces[0] != (engine.IfaceCounters{Name: "eth0", Rx: 1024, Tx: 2048}) {
		t.Errorf("ifaces %+v", smp.Ifaces)
	}
	if len(smp.Disks) != 2 || smp.Disks[0] != (engine.DiskUsage{Name: "data", Used: 524288000}) || smp.Disks[1].Name != "root" {
		t.Errorf("disks %+v", smp.Disks)
	}
}

func TestEnvAndDeviceNames(t *testing.T) {
	in := fromInstance(api.InstanceFull{Instance: api.Instance{
		Name: "x", Project: "default", StatusCode: api.Stopped,
		InstancePut:     api.InstancePut{Config: map[string]string{"environment.B": "2", "environment.A": "1", "image.description": "Debian 12"}},
		ExpandedDevices: map[string]map[string]string{"data": {"type": "disk", "source": "vol", "pool": "p", "path": "/data"}},
	}}, "default")
	if !slices.Equal(in.Env, []string{"A=1", "B=2"}) {
		t.Errorf("env %v", in.Env)
	}
	if len(in.Mounts) != 1 || in.Mounts[0].Name != "data" || in.Mounts[0].Source != "p/vol" {
		t.Errorf("mounts %+v", in.Mounts)
	}
	if x := in.Extra.(*engine.IncusExtra); x.ImageDescription != "Debian 12" {
		t.Errorf("image description %q", x.ImageDescription)
	}
}

func FuzzDiffer(f *testing.F) {
	f.Add([]byte("a\nb\n"), []byte("b\nc\n"))
	f.Fuzz(func(t *testing.T, a, b []byte) {
		d := &differ{src: engine.Stdout}
		d.next(a, time.Time{}, true)
		for _, l := range d.next(b, time.Time{}, false) {
			if len(l.Text) > engine.MaxLogLine {
				t.Fatalf("line of %d bytes", len(l.Text))
			}
		}
	})
}
