package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	fw  = LabMember{Engine: "incus", Project: "lab", Name: "opnsense"}
	srv = LabMember{Engine: "incus", Project: "lab", Name: "ubuntu-srv"}
	web = LabMember{Engine: "docker", Name: "web-api"}
)

func TestLabsEditing(t *testing.T) {
	var ls Labs
	id, err := ls.Add("  perimeter ")
	if err != nil || id != 1 || ls.Labs[0].Name != "perimeter" {
		t.Fatalf("Add: %d %v %+v", id, err, ls)
	}
	if _, err := ls.Add(""); err == nil {
		t.Fatal("an empty name was accepted")
	}
	if added, _ := ls.AddMember(id, fw); !added {
		t.Fatal("AddMember")
	}
	if added, _ := ls.AddMember(id, fw); added {
		t.Fatal("a member was added twice")
	}
	if added, err := ls.Link(id, fw, srv); !added || err != nil {
		t.Fatalf("Link: %v", err)
	}
	if added, _ := ls.Link(id, srv, fw); added {
		t.Fatal("the reverse link was added again")
	}
	if _, err := ls.Link(id, fw, fw); err == nil {
		t.Fatal("a self link was accepted")
	}
	ls.Link(id, fw, web) //nolint:errcheck // checked below
	l, _ := ls.Lab(id)
	if len(l.Members) != 3 || len(l.Links) != 2 || len(l.LinksOf(fw)) != 2 || l.Links[1].Other(fw) != web {
		t.Fatalf("lab %+v", l)
	}

	// A clone is independent.
	c := ls.Clone()
	ls.Unlink(id, web, fw)
	if cl, _ := c.Lab(id); len(cl.Links) != 2 {
		t.Fatal("Clone shares links")
	}
	if l, _ := ls.Lab(id); len(l.Links) != 1 {
		t.Fatalf("Unlink: %+v", l.Links)
	}

	// Removing a member removes its links.
	ls.RemoveMember(id, srv)
	if l, _ := ls.Lab(id); len(l.Links) != 0 || l.HasMember(srv) {
		t.Fatalf("RemoveMember: %+v", l)
	}

	id2, _ := ls.Add("k8s")
	ls.AddMember(id2, fw) //nolint:errcheck // a fresh lab
	if len(ls.Of(fw)) != 2 {
		t.Fatal("a machine can be in several labs")
	}
	if err := ls.Rename(id2, "kube"); err != nil || ls.Labs[1].Name != "kube" {
		t.Fatalf("Rename: %v", err)
	}
	ls.Delete(id)
	if _, ok := ls.Lab(id); ok || len(ls.Labs) != 1 {
		t.Fatal("Delete")
	}
	if id3, _ := ls.Add("again"); id3 != 3 {
		t.Fatalf("IDs are reused: %d", id3)
	}
}

func TestLabsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "labs.json")
	var ls Labs
	id, _ := ls.Add("perimeter")
	ls.Link(id, fw, web) //nolint:errcheck // a fresh lab
	if err := SaveLabs(path, ls); err != nil {
		t.Fatal(err)
	}
	got, warns := LoadLabs(path)
	if len(warns) != 0 || got.Version != LabsVersion || len(got.Labs) != 1 || len(got.Labs[0].Links) != 1 || got.Labs[0].Links[0].B != web {
		t.Fatalf("got %+v %v", got, warns)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", st.Mode(), err)
	}
}

func TestLoadLabsMissingAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	if ls, warns := LoadLabs(filepath.Join(dir, "labs.json")); len(ls.Labs) != 0 || warns != nil {
		t.Fatalf("missing: %+v %v", ls, warns)
	}
	path := filepath.Join(dir, "labs.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, warns := LoadLabs(path)
	if len(warns) != 1 || !strings.Contains(warns[0].Msg, "labs.json.bak") {
		t.Fatalf("warns %v", warns)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatal("no backup")
	}
}

func TestLoadLabsRepairs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "labs.json")
	doc := `{"version": 9, "labs": [
	  {"id": 2, "name": "a\u0007b", "members": [
	    {"engine": "docker", "name": "x"}, {"engine": "docker", "name": "x"}, {"engine": "", "name": "y"},
	    {"engine": "docker", "name": "z"}],
	   "links": [{"a": {"engine": "docker", "name": "x"}, "b": {"engine": "docker", "name": "x"}},
	             {"a": {"engine": "docker", "name": "x"}, "b": {"engine": "docker", "name": "gone"}},
	             {"a": {"engine": "docker", "name": "x"}, "b": {"engine": "docker", "name": "z"}},
	             {"a": {"engine": "docker", "name": "z"}, "b": {"engine": "docker", "name": "x"}}]},
	  {"id": 2, "name": "", "members": []}]}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	ls, warns := LoadLabs(path)
	if len(warns) != 3 { // newer version, two names fixed
		t.Fatalf("warns %v", warns)
	}
	a, b := ls.Labs[0], ls.Labs[1]
	if a.Name != "ab" || len(a.Members) != 2 || len(a.Links) != 1 {
		t.Fatalf("first lab %+v", a)
	}
	if b.ID == a.ID || b.Name != "lab 3" {
		t.Fatalf("second lab %+v", b)
	}
}
