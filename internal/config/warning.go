package config

import "fmt"

// Warning is a non-fatal problem found while loading a file. The UI shows
// warnings as toasts; the app keeps running with defaults.
type Warning struct {
	Source string // file path or logical source ("keys.toml", "theme dracula")
	Msg    string
}

func (w Warning) String() string {
	if w.Source == "" {
		return w.Msg
	}
	return fmt.Sprintf("%s: %s", w.Source, w.Msg)
}
