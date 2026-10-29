package fixtures_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/fixtures"
	"github.com/blokxz/viewDI/internal/provider/docker"
)

// TestRecordRoundTrip records the hand-written Docker set through the proxy
// (standing in for a daemon) and checks the recording serves the same data.
func TestRecordRoundTrip(t *testing.T) {
	src := fixtures.Serve(t, "../../testdata/fixtures/docker/v1.52")
	rec := fixtures.NewRecorder("docker", src.Socket, fixtures.NewSanitizer("web-db-secret-host"))
	proxy := filepath.Join(filepath.Dir(src.Socket), "proxy.sock")
	stop, err := rec.Listen(proxy)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	a, err := docker.New("unix://" + proxy)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	want, err := a.ListInstances(ctx, engine.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range want {
		if _, err := a.Inspect(ctx, in.ID); err != nil {
			t.Fatal(err)
		}
	}
	ectx, ecancel := context.WithCancel(ctx)
	feed, err := a.Events(ectx)
	if err != nil {
		t.Fatal(err)
	}
	src.Emit()
	for range 4 {
		<-feed.C()
	}
	ecancel()
	for range feed.C() {
	}

	out := t.TempDir()
	if err := rec.Write(out, fixtures.Index{APIVersion: "1.52", ServerVersion: "29.1.2"}); err != nil {
		t.Fatal(err)
	}
	ev, err := os.ReadFile(filepath.Join(out, "events.jsonl"))
	if err != nil || strings.Count(string(ev), "\n") < 4 {
		t.Fatalf("events not recorded: %v %q", err, ev)
	}

	replay := fixtures.Serve(t, out)
	b, _ := docker.New("unix://" + replay.Socket)
	defer b.Close()
	got, err := b.ListInstances(ctx, engine.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("replayed %d instances, recorded %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Key() != want[i].Key() || got[i].State != want[i].State || !got[i].Started.Equal(want[i].Started) {
			t.Errorf("replay %d: %+v\nwant %+v", i, got[i], want[i])
		}
	}
}
