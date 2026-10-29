package openmetrics

import (
	"strings"
	"testing"
)

const doc = `# HELP incus_cpu_seconds_total The total number of CPU time used in seconds.
# TYPE incus_cpu_seconds_total counter
incus_cpu_seconds_total{cpu="0",mode="user",name="web-lab",project="default",type="container"} 12.5
incus_cpu_seconds_total{cpu="0",mode="system",name="web-lab",project="default",type="container"} 2.5 1761739200000
incus_memory_MemTotal_bytes{name="dev \"arch\"",project="lab",type="container"} 4.294967296e+09
incus_procs_total 7
bad line here
broken{name="x" 1
# EOF
`

func TestParse(t *testing.T) {
	got, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d samples: %+v", len(got), got)
	}
	if got[0].Value != 12.5 || got[0].Label("mode") != "user" || got[0].Label("name") != "web-lab" {
		t.Errorf("first %+v", got[0])
	}
	if got[1].Value != 2.5 {
		t.Errorf("timestamped value %v", got[1].Value)
	}
	if got[2].Label("name") != `dev "arch"` || got[2].Value != 4294967296 {
		t.Errorf("escaped label %+v", got[2])
	}
	if got[3].Name != "incus_procs_total" || got[3].Labels != nil {
		t.Errorf("unlabelled %+v", got[3])
	}
}

func TestParseCaps(t *testing.T) {
	long := "m{a=\"" + strings.Repeat("x", MaxLine) + "\"} 1\n"
	if _, err := Parse(strings.NewReader(long)); err != ErrTooLarge {
		t.Fatalf("long line: %v", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(doc)
	f.Add(`m{a="\\",b="\n"} 1e3`)
	f.Add(`{} 1`)
	f.Fuzz(func(t *testing.T, in string) {
		samples, _ := Parse(strings.NewReader(in))
		for _, s := range samples {
			if !validName(s.Name) {
				t.Fatalf("invalid name accepted: %q", s.Name)
			}
		}
	})
}
