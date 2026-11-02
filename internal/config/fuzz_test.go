package config

import (
	"os"
	"path/filepath"
	"testing"
)

// state.json and labs.json are app-owned but can be edited, truncated or
// replaced; loading must never panic or hang whatever the bytes are.
func FuzzLoadState(f *testing.F) {
	for _, s := range []string{"", "{", `{"version":1}`, `{"version":99,"theme":"x","toggled":{"a":["b"]}}`, "\x00\xff", `[]`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "state.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Skip()
		}
		st, _ := LoadState(path)
		if st.Version != StateVersion {
			t.Fatalf("version = %d", st.Version)
		}
	})
}

func FuzzLoadLabs(f *testing.F) {
	for _, s := range []string{"", "{", `{"version":1,"labs":[{"id":1,"name":"a","links":[{"from":{},"to":{}}]}]}`,
		`{"labs":[{"id":-1,"name":"` + string(make([]byte, 300)) + `"}]}`, "null", `{"labs":null}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "labs.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Skip()
		}
		ls, _ := LoadLabs(path)
		if ls.Version != LabsVersion {
			t.Fatalf("version = %d", ls.Version)
		}
	})
}
