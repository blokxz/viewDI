package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	p := PathsUnder(t.TempDir()).StateFile()
	want := State{
		Theme: "nord", LastView: 4, IncusProject: "lab", Table: TableState{Sort: "uptime", Reverse: true, Engine: "incus"},
		DetailTabs: map[string]string{"docker": "Logs"},
	}
	if err := SaveState(p, want); err != nil {
		t.Fatal(err)
	}
	got, warns := LoadState(p)
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	want.Version = StateVersion
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v", fi.Mode().Perm())
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want 1", len(entries))
	}
}

func TestLoadStateMissing(t *testing.T) {
	st, warns := LoadState(filepath.Join(t.TempDir(), "state.json"))
	if len(warns) != 0 || st.Version != StateVersion || st.Theme != "" {
		t.Fatalf("got %+v %v", st, warns)
	}
}

func TestLoadStateCorruptIsBackedUp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, warns := LoadState(p)
	if len(warns) != 1 || st.Theme != "" {
		t.Fatalf("got %+v %v", st, warns)
	}
	if _, err := os.Stat(p + ".bak"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("corrupt file still in place")
	}
}

func TestSaveStateReadOnlyDirFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := SaveState(filepath.Join(dir, "state.json"), State{}); err == nil {
		t.Fatal("expected error writing into read-only dir (F16)")
	}
}
