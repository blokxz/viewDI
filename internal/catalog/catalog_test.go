package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/library"
)

func builtin(t *testing.T) []Entry {
	t.Helper()
	entries, err := ParseFS(library.FS, Builtin)
	if err != nil {
		t.Fatalf("built-in library: %v", err)
	}
	return entries
}

func TestBuiltinLibraryIsValid(t *testing.T) {
	entries := builtin(t)
	var docker, incus int
	for _, e := range entries {
		switch e.Engine {
		case engine.Docker:
			docker++
		case engine.Incus:
			incus++
		}
		if e.Note == "" {
			t.Errorf("%s has no note", e.ID)
		}
	}
	if docker < 25 || incus < 8 {
		t.Fatalf("library has %d Docker and %d Incus entries, want at least 25 and 8", docker, incus)
	}
	c := New(entries...)
	if len(c.Categories()) < 6 {
		t.Errorf("categories %v", c.Categories())
	}
	if _, ok := c.Get("postgres"); !ok {
		t.Error("postgres is missing")
	}
}

func TestBuiltinDockerEntriesRunWithSecrets(t *testing.T) {
	for _, e := range builtin(t) {
		if e.Engine != engine.Docker {
			continue
		}
		for _, v := range e.Env {
			if v.Generate == "password" && v.Value != "" {
				t.Errorf("%s: %s has both", e.ID, v.Name)
			}
			if strings.Contains(strings.ToLower(v.Name), "password") && v.Generate == "" && v.Value != "" {
				t.Errorf("%s: %s must not carry a fixed password", e.ID, v.Name)
			}
		}
	}
}

func TestParseRejects(t *testing.T) {
	const ok = `[[entry]]
id = "x"
name = "X"
category = "C"
description = "d"
engine = "docker"
image = "alpine:3.21"
`
	if _, err := Parse([]byte(ok), User); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	for name, doc := range map[string]string{
		"unknown key":       ok + "colour = \"red\"\n",
		"latest":            strings.Replace(ok, "alpine:3.21", "alpine:latest", 1),
		"no tag":            strings.Replace(ok, "alpine:3.21", "alpine", 1),
		"bad id":            strings.Replace(ok, `id = "x"`, `id = "X Y"`, 1),
		"bad engine":        strings.Replace(ok, `"docker"`, `"podman"`, 1),
		"incus field":       ok + "cpu = 2\n",
		"bad port":          ok + "[[entry.ports]]\ncontainer = 0\n",
		"bad volume path":   ok + "[[entry.volumes]]\nname = \"d\"\npath = \"data\"\n",
		"secret and value":  ok + "[[entry.env]]\nname = \"P\"\nvalue = \"a\"\ngenerate = \"password\"\n",
		"unknown generator": ok + "[[entry.env]]\nname = \"P\"\ngenerate = \"uuid\"\n",
		"bad placeholder":   ok + "note = \"see {{secret}}\"\n",
		"http docs":         ok + "docs = \"http://example.com\"\n",
		"duplicate id":      ok + "\n" + ok,
		"not toml":          "[[entry",
		"cloud-init no img": strings.Replace(ok, `"docker"`, `"incus"`, 1) + "kinds = [\"vm\"]\ncloud_init = \"#cloud-config\"\n",
		"incus local image": strings.Replace(strings.Replace(ok, `"docker"`, `"incus"`, 1), "alpine:3.21", "alpine/3.21", 1) + "kinds = [\"vm\"]\n",
		"incus config key":  strings.Replace(strings.Replace(ok, `"docker"`, `"incus"`, 1), "alpine:3.21", "images:alpine/3.21", 1) + "kinds = [\"vm\"]\n[entry.config]\n\"limits.cpu\" = \"9\"\n",
		"kind not listed":   strings.Replace(strings.Replace(ok, `"docker"`, `"incus"`, 1), "alpine:3.21", "images:alpine/3.21", 1) + "kinds = [\"vm\"]\ndefault_kind = \"container\"\n",
		"oversized file":    ok + strings.Repeat("# padding\n", MaxFileSize/10),
	} {
		if _, err := Parse([]byte(doc), User); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseFSRejectsDuplicateIDsAcrossFiles(t *testing.T) {
	one := "[[entry]]\nid = \"a\"\nname = \"A\"\ncategory = \"C\"\nengine = \"docker\"\nimage = \"alpine:3.21\"\n"
	_, err := ParseFS(fstest.MapFS{"1.toml": {Data: []byte(one)}, "2.toml": {Data: []byte(one)}}, Builtin)
	if err == nil || !strings.Contains(err.Error(), "also in") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewLetsALaterEntryReplaceAnEarlierOne(t *testing.T) {
	a := Entry{ID: "pg", Name: "Built-in", Category: "Databases", Engine: engine.Docker, Source: Builtin}
	b := Entry{ID: "pg", Name: "Mine", Category: "Databases", Engine: engine.Docker, Source: User}
	c := New(a, Entry{ID: "z", Name: "Z", Category: "Mine", Engine: engine.Docker, Source: User}, b)
	got, _ := c.Get("pg")
	if got.Name != "Mine" || len(c.Entries()) != 2 {
		t.Fatalf("entries %+v", c.Entries())
	}
}

func TestRenderNote(t *testing.T) {
	note := "Open :{{port 80}} for {{name}}; {{ port 5432 }}; {{port 99}}"
	got := RenderNote(note, "web-1", map[uint16]uint16{80: 8081, 5432: 5433})
	if got != "Open :8081 for web-1; 5433; 99" {
		t.Fatalf("got %q", got)
	}
	for _, bad := range []string{"{{secret}}", "{{port}}", "{{port 70000}}", "{{ name", "x}}", "bell\a"} {
		if ValidateNote(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"", "[[entry]]", "[[entry]]\nid = \"a\"\nname = \"A\"\ncategory = \"C\"\nengine = \"docker\"\nimage = \"alpine:3.21\"\nnote = \"{{port 80}} {{name}}\"\n", "\x00", "[[entry]]\nengine = 5\n"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		entries, err := Parse(data, User)
		if err != nil {
			return
		}
		for _, e := range entries { // whatever parses must be valid and renderable
			if e.Validate() != nil {
				t.Fatalf("parsed an invalid entry %+v", e)
			}
			_ = RenderNote(e.Note, "n", nil)
		}
	})
}

func TestLoadUserEntries(t *testing.T) {
	dir := t.TempDir()
	write := func(name, doc string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("mine.toml", `[[entry]]
id = "postgres"
name = "My Postgres"
category = "Mine"
engine = "docker"
image = "postgres:16"
[[entry]]
id = "extra"
name = "Extra"
category = "Mine"
engine = "docker"
image = "alpine:3.21"
`)
	write("broken.toml", "[[entry]]\nid = \"BAD ID\"\n")
	write("ignored.txt", "not a library file")

	c, warns := Load(library.FS, dir)
	if got, _ := c.Get("postgres"); got.Name != "My Postgres" || got.Source != User {
		t.Errorf("user entry did not replace the built-in one: %+v", got)
	}
	if _, ok := c.Get("extra"); !ok {
		t.Error("extra is missing")
	}
	if _, ok := c.Get("redis"); !ok {
		t.Error("built-in entries must survive a broken user file")
	}
	if len(warns) != 1 || !strings.Contains(warns[0].Source, "broken.toml") {
		t.Errorf("warnings = %v", warns)
	}
	if cats := c.Categories(); cats[len(cats)-1] != "Mine" {
		t.Errorf("the user's category must come last: %v", cats)
	}
}

func TestLoadMissingAndHugeUserFiles(t *testing.T) {
	if _, warns := Load(library.FS, filepath.Join(t.TempDir(), "nope")); len(warns) != 0 {
		t.Errorf("a missing directory is not a problem: %v", warns)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "huge.toml"), []byte(strings.Repeat("#", MaxFileSize+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, warns := Load(library.FS, dir); len(warns) != 1 {
		t.Errorf("warnings = %v", warns)
	}
}
