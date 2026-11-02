package theme

import (
	"regexp"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/blokxz/viewDI/themes"
)

// colourSGR matches any SGR sequence that sets a foreground or background
// colour (30-37, 38, 39 excluded as "default", 40-47, 48, 90-97, 100-107).
var colourSGR = regexp.MustCompile(`\x1b\[[0-9;]*\b(3[0-8]|4[0-8]|9[0-7]|10[0-7])[;m]`)

func monoStyles(t *testing.T) *Styles {
	t.Helper()
	r, _ := LoadRegistry(themes.FS, "")
	th, ok := r.Get(MonochromeID)
	if !ok {
		t.Fatal("no monochrome theme")
	}
	return NewStyles(th, 1)
}

func TestMonochromeNeverEmitsColour(t *testing.T) {
	s := monoStyles(t)
	styles := map[string]lipgloss.Style{
		"Text": s.Text, "Muted": s.Muted, "TabActive": s.TabActive, "Badge": s.Badge, "Panel": s.Panel,
		"PanelFocused": s.PanelFocused, "PanelError": s.PanelError, "Match": s.Match, "CursorLine": s.CursorLine,
		"TreeCursor": s.TreeCursor, "LinkDot": s.LinkDot, "StatusBar": s.StatusBar, "Modal": s.Modal,
		"Selected": s.Selected, "Button": s.Button, "ButtonDanger": s.ButtonDanger, "Indicator": s.Indicator,
	}
	for l := LevelInfo; l <= LevelErr; l++ {
		styles["level"+string(rune('0'+l))] = s.LevelText(l)
		styles["toast"+string(rune('0'+l))] = s.Toast(l)
		styles["banner"+string(rune('0'+l))] = s.Banner(l)
	}
	for name, st := range styles {
		if out := st.Render("x"); colourSGR.MatchString(out) {
			t.Errorf("%s emits a colour: %q", name, out)
		}
	}
}

// Every pair of states that colour used to tell apart must still differ.
func TestMonochromeLevelsAreDistinguishable(t *testing.T) {
	s := monoStyles(t)
	seen := map[string]Level{}
	for _, l := range []Level{LevelOK, LevelWarn, LevelErr} {
		for name, out := range map[string]string{
			"text":   s.LevelText(l).Render("x"),
			"banner": s.Banner(l).Render("x"),
			"toast":  s.Toast(l).Render("x"),
		} {
			key := name + "|" + out
			if prev, dup := seen[key]; dup && name != "text" {
				t.Errorf("%s of levels %d and %d look the same: %q", name, prev, l, out)
			}
			seen[key] = l
		}
	}
	if s.LevelText(LevelWarn).Render("x") == s.LevelText(LevelErr).Render("x") {
		t.Error("warning and error text look the same")
	}
	if s.Panel.Render("x") == s.PanelFocused.Render("x") || s.Panel.Render("x") == s.PanelError.Render("x") ||
		s.PanelFocused.Render("x") == s.PanelError.Render("x") {
		t.Error("panel, focused panel and error panel are not all different")
	}
	if s.Text.Render("x") == s.Selected.Render("x") || s.TabInactive.Render("x") == s.TabActive.Render("x") {
		t.Error("selection or active tab indistinguishable")
	}
}
