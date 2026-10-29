package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blokxz/viewDI/internal/clock"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/provider/fake"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/views/detail"
)

// detailBatch builds what the stream registry would deliver for one
// homelab instance on the fixed test clock: its inspect document, the last
// 60 log lines and samples second by second.
func detailBatch(t testing.TB, id engine.ID, name string, samples int) detail.BatchMsg {
	t.Helper()
	clk := clock.NewFake(fixedNow)
	w := fake.NewWorld(clk)
	fake.Homelab(w)
	e := w.Engine(id)
	in, ok := e.Instance(name)
	if !ok {
		t.Fatalf("no instance %s", name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	full, err := e.Inspect(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := e.InspectRaw(ctx, in.ID)
	out := detail.BatchMsg{
		Key:     in.Key(),
		Inspect: &detail.Inspected{Instance: full, Raw: raw},
		Logs:    &detail.StreamStatus{State: detail.Live},
		Stats:   &detail.StreamStatus{State: detail.Live},
	}
	// Advance first so the history ends at the fixed clock.
	clk.Advance(time.Duration(samples) * time.Second)
	logs, err := e.Logs(ctx, in.ID, engine.LogOptions{Tail: 60})
	if err != nil {
		t.Fatal(err)
	}
	for l := range logs.C() {
		l.Text = textsafe.Clean(l.Text)
		out.Lines = append(out.Lines, l)
	}
	if in.State == engine.StateRunning && samples > 0 {
		st, err := e.WatchStats(ctx, in.ID, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		for i := range samples {
			if i > 0 {
				clk.WaitPending(1, time.Second)
				clk.Advance(time.Second)
			}
			out.Samples = append(out.Samples, <-st.C())
		}
	}
	return out
}

// openDetailOf selects name in the Table view and presses enter.
func (d *driver) openDetailOf(name string) {
	d.t.Helper()
	d.press("3")
	for i, in := range d.table().Rows() {
		if in.Name == name {
			d.press("home")
			for range i {
				d.press("down")
			}
			d.press("enter")
			return
		}
	}
	d.t.Fatalf("no row %s", name)
}

func TestDetailTabsAndPersistence(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.openDetailOf("web-api")
	d.send(detailBatch(t, engine.Docker, "web-api", 3))
	d.press("tab", "tab") // Inspect
	if got := d.m.Detail().Tab(); got != detail.TabInspect {
		t.Fatalf("tab %v", got)
	}
	if st := d.saved[len(d.saved)-1]; st.DetailTabs["docker"] != "Inspect" {
		t.Fatalf("saved tabs %v", st.DetailTabs)
	}
	d.press("esc")
	// Another Docker container opens on the same tab; Incus keeps its own.
	d.openDetailOf("redis-cache")
	if got := d.m.Detail().Tab(); got != detail.TabInspect {
		t.Fatalf("reopened on %v", got)
	}
	d.press("esc")
	d.openDetailOf("web-lab")
	if got := d.m.Detail().Tab(); got != detail.TabOverview {
		t.Fatalf("incus opened on %v", got)
	}
	d.press("shift+tab")
	if got := d.m.Detail().Tab(); got != detail.TabResources {
		t.Fatalf("shift+tab wrapped to %v", got)
	}
	d.press("1")
	if d.m.Detail() != nil || d.m.ActiveView() != 1 {
		t.Fatal("a view key must close the detail")
	}
}

func TestDetailRevealsOneSecret(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.openDetailOf("web-api")
	if !strings.Contains(d.screen(), "loading") {
		t.Error("env shown as loaded before inspect")
	}
	d.send(detailBatch(t, engine.Docker, "web-api", 3))
	if strings.Contains(d.screen(), "s3cr3t-pw") || !strings.Contains(d.screen(), "DB_PASSWORD=••••••") {
		t.Fatal("secret not masked")
	}
	// Move to the DB_PASSWORD row and reveal it.
	for range 40 {
		if strings.Contains(cursorRow(d), "DB_PASSWORD") {
			break
		}
		d.press("down")
	}
	d.press("v")
	if !strings.Contains(d.screen(), "DB_PASSWORD=s3cr3t-pw") {
		t.Fatalf("v did not reveal:\n%s", d.screen())
	}
	d.press("v")
	if strings.Contains(d.screen(), "s3cr3t-pw") {
		t.Fatal("v did not hide again")
	}
}

// cursorRow returns the highlighted overview row.
func cursorRow(d *driver) string {
	for _, l := range strings.Split(d.m.Render(), "\n") {
		if strings.Contains(l, "48;") && strings.Contains(l, "=") { // the cursor line has a background
			return l
		}
	}
	return ""
}

func TestDetailRemovedInstance(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.openDetailOf("redis-cache")
	d.send(ChangesMsg{Changes: homelab(t, func(w *fake.World) { w.Docker.Remove("redis-cache") })})
	if !d.m.Detail().Removed() || !strings.Contains(d.screen(), "no longer exists") {
		t.Fatalf("removed instance not flagged:\n%s", d.screen())
	}
	d.press("s")
	if !strings.Contains(d.lastToast(), "No instance selected") {
		t.Fatalf("action on a removed instance: %q", d.lastToast())
	}
}

func TestDetailInspectSearchAndCopy(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.openDetailOf("web-api")
	d.send(detailBatch(t, engine.Docker, "web-api", 0))
	d.press("tab", "tab", "/", "h", "e", "a", "l", "t", "h", "enter")
	tree := d.m.Detail().Tree()
	if n, cur := tree.Matches(); n != 2 || cur != 1 {
		t.Fatalf("matches %d/%d", cur, n)
	}
	d.press("ctrl+n")
	path := tree.Cursor().Path
	d.press("y")
	if want := "Copied " + path; d.lastToast() != want || !strings.HasPrefix(path, ".State.Health") {
		t.Fatalf("toast %q for path %q", d.lastToast(), path)
	}
	if !d.sawClipboard(path) {
		t.Fatal("y did not write the clipboard")
	}
	d.press("esc") // clears the search, keeps the detail
	if d.m.Detail() == nil || tree.Query() != "" {
		t.Fatal("esc must clear the search first")
	}
	d.press("esc")
	if d.m.Detail() != nil {
		t.Fatal("second esc must close the detail")
	}
}

func TestDetailLogsFilterKeys(t *testing.T) {
	d := newDriver(t, withEngines(nil))
	d.send(ChangesMsg{Changes: homelab(t, nil)})
	d.openDetailOf("web-db")
	d.send(detailBatch(t, engine.Docker, "web-db", 0))
	d.press("tab", "/", "E", "R", "R", "O", "R")
	if !d.m.Detail().Inputting() {
		t.Fatal("prompt not open")
	}
	d.press("q") // typed, not quit
	if d.quitRequested() {
		t.Fatal("q quit while typing a filter")
	}
	d.press("backspace", "enter")
	logs := d.m.Detail().Logs()
	if f, _ := logs.Filter(); f != "ERROR" || logs.Visible() == 0 || logs.Visible() == logs.Len() {
		t.Fatalf("filter %q: %d of %d lines", f, logs.Visible(), logs.Len())
	}
	d.press("p")
	if !logs.Paused() || !strings.Contains(d.screen(), "PAUSED") {
		t.Fatal("p did not pause")
	}
	d.press("G")
	if logs.Paused() || !logs.Following() {
		t.Fatal("G did not resume")
	}
	d.press("g", "g")
	if logs.Following() {
		t.Fatal("gg did not jump to the top")
	}
}

// sawClipboard reports whether a command wrote text to the clipboard.
func (d *driver) sawClipboard(text string) bool {
	for _, m := range d.seen {
		if fmt.Sprintf("%v", m) == text || strings.Contains(fmt.Sprintf("%#v", m), fmt.Sprintf("%q", text)) {
			return true
		}
	}
	return false
}
