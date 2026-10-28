// Package uitest holds helpers shared by UI tests.
package uitest

import (
	"reflect"

	tea "charm.land/bubbletea/v2"
)

// Collect runs cmd and every command nested in batches and sequences, and
// returns the leaf messages in order. Do not pass commands that contain
// tea.Tick: they block for their full duration.
func Collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, Collect(c)...)
		}
		return out
	}
	// tea.Sequence yields an unexported []tea.Cmd type.
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeFor[tea.Cmd]() {
		var out []tea.Msg
		for i := range v.Len() {
			out = append(out, Collect(v.Index(i).Interface().(tea.Cmd))...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// Key builds a key press from its string form ("q", "enter", "ctrl+c", "shift+tab").
func Key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	}
	if len(s) > 5 && s[:5] == "ctrl+" {
		return tea.KeyPressMsg{Code: []rune(s[5:])[0], Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}
