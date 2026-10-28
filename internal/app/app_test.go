package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/ui/components/banner"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/uitest"
	"github.com/blokxz/viewDI/themes"
)

// driver feeds messages to the model and runs the resulting commands
// synchronously, feeding their messages back, like the Bubble Tea loop does.
type driver struct {
	t     *testing.T
	m     Model
	saved []config.State
	seen  []tea.Msg
}

func newDriver(t *testing.T, mutate func(*Options)) *driver {
	t.Helper()
	reg, warns := theme.LoadRegistry(themes.FS, "")
	if len(warns) > 0 {
		t.Fatal(warns)
	}
	th, _ := reg.Resolve("everforest-dark")
	d := &driver{t: t}
	opts := Options{
		Themes:   reg,
		Theme:    th,
		Keys:     keys.MustDefault(),
		ToastTTL: time.Millisecond,
		SaveState: func(st config.State) error {
			d.saved = append(d.saved, st)
			return nil
		},
	}
	if mutate != nil {
		mutate(&opts)
	}
	d.m = New(opts)
	d.run(d.m.Init())
	d.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	return d
}

func (d *driver) send(msg tea.Msg) {
	model, cmd := d.m.Update(msg)
	d.m = model.(Model)
	d.run(cmd)
}

func (d *driver) run(cmd tea.Cmd) {
	for _, out := range uitest.Collect(cmd) {
		if out == nil {
			continue
		}
		d.seen = append(d.seen, out)
		switch out.(type) {
		case toast.ExpiredMsg, tea.QuitMsg, tea.SuspendMsg:
			continue // timers are not replayed; quit/suspend are recorded only
		}
		d.send(out)
	}
}

func (d *driver) press(keys ...string) {
	for _, k := range keys {
		d.send(uitest.Key(k))
	}
}

func (d *driver) quitRequested() bool {
	for _, m := range d.seen {
		if _, ok := m.(tea.QuitMsg); ok {
			return true
		}
	}
	return false
}

func (d *driver) lastToast() string {
	ts := d.m.Toasts()
	if len(ts) == 0 {
		return ""
	}
	return ts[len(ts)-1].Text
}

func TestViewSwitchingAndFocus(t *testing.T) {
	d := newDriver(t, nil)
	if d.m.ActiveView() != 1 {
		t.Fatalf("start view = %d", d.m.ActiveView())
	}
	for v := 1; v <= 6; v++ {
		d.press(string(rune('0' + v)))
		if d.m.ActiveView() != v {
			t.Fatalf("after %d: view %d", v, d.m.ActiveView())
		}
		if !strings.Contains(ansi.Strip(d.m.Render()), "Coming in Stage") {
			t.Fatalf("view %d renders no placeholder", v)
		}
	}
	d.press("4", "tab")
	if d.m.Focus() != 1 {
		t.Fatalf("tab: focus %d", d.m.Focus())
	}
	d.press("tab")
	if d.m.Focus() != 0 {
		t.Fatalf("tab wraps: focus %d", d.m.Focus())
	}
	d.press("shift+tab")
	if d.m.Focus() != 1 {
		t.Fatalf("shift+tab: focus %d", d.m.Focus())
	}
	d.press("3")
	if d.m.Focus() != 0 {
		t.Fatal("switching view resets focus")
	}
}

func TestLastViewRestoredAndPersistedOnQuit(t *testing.T) {
	d := newDriver(t, func(o *Options) { o.State = config.State{LastView: 4} })
	if d.m.ActiveView() != 4 {
		t.Fatalf("restored view = %d", d.m.ActiveView())
	}
	d.press("6", "q")
	if !d.quitRequested() {
		t.Fatal("q must quit")
	}
	if len(d.saved) != 1 || d.saved[0].LastView != 6 {
		t.Fatalf("saved = %+v", d.saved)
	}
}

// Regression: quitting must not persist the startup theme. Otherwise one run
// with --theme (or the config.toml theme) would land in state.json, which
// outranks config.toml forever after.
func TestQuitDoesNotPersistStartupTheme(t *testing.T) {
	d := newDriver(t, func(o *Options) {
		o.Theme, _ = o.Themes.Get("nord")
		o.State = config.State{}
	})
	d.press("q")
	if len(d.saved) != 1 || d.saved[0].Theme != "" {
		t.Fatalf("saved = %+v, want empty theme", d.saved)
	}

	d = newDriver(t, func(o *Options) { o.State = config.State{Theme: "dracula"} })
	d.press("q")
	if d.saved[0].Theme != "dracula" {
		t.Fatalf("a previous picker choice must be kept, saved = %+v", d.saved)
	}
}

func TestThemePickerPreviewSelectAndCancel(t *testing.T) {
	d := newDriver(t, nil)
	d.press("T")
	if d.m.ModalDepth() != 1 {
		t.Fatal("T must open the picker")
	}
	d.press("down")
	previewed := d.m.ThemeID()
	if previewed == "everforest-dark" {
		t.Fatal("moving the cursor must preview live")
	}
	if len(d.saved) != 0 {
		t.Fatal("preview must not persist")
	}
	d.press("esc")
	if d.m.ThemeID() != "everforest-dark" || d.m.ModalDepth() != 0 {
		t.Fatalf("esc must restore: theme=%s depth=%d", d.m.ThemeID(), d.m.ModalDepth())
	}

	d.press("T", "down", "enter")
	if d.m.ThemeID() != previewed || d.m.ModalDepth() != 0 {
		t.Fatalf("enter must apply: %s", d.m.ThemeID())
	}
	if len(d.saved) != 1 || d.saved[0].Theme != previewed {
		t.Fatalf("selection must persist to state.json: %+v", d.saved)
	}
}

func TestModalCapturesKeysExceptCtrlC(t *testing.T) {
	d := newDriver(t, nil)
	d.press("?")
	if d.m.ModalDepth() != 1 {
		t.Fatal("? must open help")
	}
	d.press("3") // captured by the modal: no view switch
	if d.m.ActiveView() != 1 {
		t.Fatal("modal must capture view keys")
	}
	d.press("q") // closes help, does not quit
	if d.m.ModalDepth() != 0 || d.quitRequested() {
		t.Fatalf("q in help must close it, not quit (depth=%d)", d.m.ModalDepth())
	}
	d.press("?", "ctrl+c")
	if !d.quitRequested() {
		t.Fatal("ctrl+c must quit even with a modal open")
	}
}

func TestInstanceKeysOnlyInInstanceViews(t *testing.T) {
	d := newDriver(t, nil)
	d.press("3", "d")
	if !strings.Contains(d.lastToast(), "No instance selected") {
		t.Fatalf("Table: toast = %q", d.lastToast())
	}
	d.press("6", "F") // Updates does not show instances: F is unbound there
	if strings.Contains(d.lastToast(), "pause") {
		t.Fatalf("Updates must ignore instance keys, toast = %q", d.lastToast())
	}
}

func TestReadOnly(t *testing.T) {
	d := newDriver(t, func(o *Options) { o.ReadOnly = true })
	d.press("3", "s")
	if !strings.Contains(d.lastToast(), "Read-only") {
		t.Fatalf("toast = %q", d.lastToast())
	}
	d.press("n")
	if !strings.Contains(d.lastToast(), "Read-only") {
		t.Fatalf("toast = %q", d.lastToast())
	}
	if !strings.Contains(ansi.Strip(d.m.Render()), "READ-ONLY") {
		t.Fatal("status bar must show READ-ONLY")
	}
}

func TestStartupWarningsBecomeToasts(t *testing.T) {
	d := newDriver(t, func(o *Options) {
		o.Warnings = []config.Warning{{Source: "themes/x.toml", Msg: "theme skipped"}}
	})
	if !strings.Contains(d.lastToast(), "theme skipped") {
		t.Fatalf("toasts = %+v", d.m.Toasts())
	}
}

func TestStateSaveFailureToastsOnce(t *testing.T) {
	d := newDriver(t, func(o *Options) {
		o.SaveState = func(config.State) error { return errors.New("read-only file system") }
	})
	d.send(StateSavedMsg{Err: errors.New("disk full")})
	d.send(StateSavedMsg{Err: errors.New("disk full")})
	n := 0
	for _, ts := range d.m.Toasts() {
		if strings.Contains(ts.Text, "Cannot save") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one save-failure toast, got %d: %+v", n, d.m.Toasts())
	}
}

func TestDebugOverlayNeedsFlag(t *testing.T) {
	d := newDriver(t, nil)
	d.press("ctrl+d")
	if !strings.Contains(d.lastToast(), "--debug") {
		t.Fatalf("toast = %q", d.lastToast())
	}
	dbg := newDriver(t, func(o *Options) { o.Debug = true })
	model, _ := dbg.m.Update(uitest.Key("ctrl+d")) // don't run the 1 s tick
	dbg.m = model.(Model)
	if !strings.Contains(ansi.Strip(dbg.m.Render()), "frame") {
		t.Fatal("debug overlay not rendered")
	}
}

func TestBanner(t *testing.T) {
	d := newDriver(t, nil)
	d.send(banner.ShowMsg{Level: theme.LevelWarn, Text: "Incus unreachable — retrying in 4s"})
	if !strings.Contains(ansi.Strip(d.m.Render()), "Incus unreachable") {
		t.Fatal("banner not rendered")
	}
	d.send(banner.ShowMsg{})
	if strings.Contains(ansi.Strip(d.m.Render()), "Incus unreachable") {
		t.Fatal("banner not cleared")
	}
}

// Every frame must be exactly width×height, whatever the size and overlays,
// otherwise resizing garbles the screen.
func TestRenderIsExactlyScreenSize(t *testing.T) {
	sizes := [][2]int{{80, 24}, {100, 30}, {119, 40}, {120, 40}, {200, 60}, {300, 80}, {79, 24}, {40, 10}, {120, 23}}
	setups := map[string]func(d *driver){
		"plain":  func(*driver) {},
		"help":   func(d *driver) { d.press("?") },
		"picker": func(d *driver) { d.press("T") },
		"banner": func(d *driver) {
			d.send(banner.ShowMsg{Level: theme.LevelErr, Text: strings.Repeat("long banner ", 40)})
		},
		"toast": func(d *driver) {
			// Push directly: running the expiry command would block on its timer.
			d.m.toasts, _ = d.m.toasts.Push(theme.LevelInfo, strings.Repeat("long toast ", 20))
		},
	}
	for name, setup := range setups {
		for _, sz := range sizes {
			d := newDriver(t, nil)
			setup(d)
			for view := 1; view <= 6; view++ {
				d.send(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
				if d.m.ModalDepth() == 0 {
					d.press(string(rune('0' + view)))
				}
				out := d.m.Render()
				lines := strings.Split(out, "\n")
				if len(lines) != sz[1] {
					t.Fatalf("%s %dx%d view %d: %d lines", name, sz[0], sz[1], view, len(lines))
				}
				for i, l := range lines {
					if w := ansi.StringWidth(l); w != sz[0] {
						t.Fatalf("%s %dx%d view %d: line %d is %d wide: %q", name, sz[0], sz[1], view, i, w, ansi.Strip(l))
					}
				}
			}
		}
	}
}

func TestTooSmall(t *testing.T) {
	d := newDriver(t, nil)
	d.send(tea.WindowSizeMsg{Width: 70, Height: 20})
	if d.m.Layout() != layout.TooSmall || !strings.Contains(ansi.Strip(d.m.Render()), "Terminal too small") {
		t.Fatal("too-small screen not shown")
	}
	d.press("q")
	if !d.quitRequested() {
		t.Fatal("q must still quit on the too-small screen")
	}
}

// Stage 0 acceptance: startup to first frame < 150 ms (docs/05 §4.1). This
// covers everything after flag parsing: theme registry, key map, model, first
// render. Run without -race the real figure is a few milliseconds.
func TestStartupBudget(t *testing.T) {
	start := time.Now()
	reg, _ := theme.LoadRegistry(themes.FS, "")
	th, _ := reg.Resolve()
	km, _ := keys.Apply(keys.Defaults(), nil)
	m := New(Options{Themes: reg, Theme: th, Keys: km})
	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if model.(Model).View().Content == "" {
		t.Fatal("empty first frame")
	}
	if d := time.Since(start); d > 150*time.Millisecond {
		t.Fatalf("startup took %s, budget 150ms", d)
	}
}
