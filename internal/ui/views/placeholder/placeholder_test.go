package placeholder

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/layout"
	"github.com/blokxz/viewDI/internal/ui/theme"
	"github.com/blokxz/viewDI/internal/ui/views"
)

func TestRenderExactSizeInEveryMode(t *testing.T) {
	s := theme.NewStyles(theme.Fallback(), 1)
	for _, v := range All() {
		for _, size := range [][2]int{{80, 20}, {119, 30}, {120, 36}, {300, 76}} {
			w, h := size[0], size[1]
			out := v.Render(views.Context{Styles: s, Keys: keys.MustDefault(), Width: w, Height: h, Mode: layout.ModeFor(w, h+4)})
			lines := strings.Split(out, "\n")
			if len(lines) != h {
				t.Fatalf("%s %dx%d: %d lines", v.Title(), w, h, len(lines))
			}
			for i, l := range lines {
				if ansi.StringWidth(l) != w {
					t.Fatalf("%s %dx%d: line %d width %d", v.Title(), w, h, i, ansi.StringWidth(l))
				}
			}
		}
	}
}

func TestSixViewsInOrder(t *testing.T) {
	want := []string{"Admin", "Resources", "Table", "Tree", "Jobs", "Updates"}
	all := All()
	if len(all) != len(want) {
		t.Fatalf("got %d views", len(all))
	}
	for i, v := range all {
		if v.Title() != want[i] {
			t.Errorf("view %d = %s, want %s", i+1, v.Title(), want[i])
		}
	}
}
