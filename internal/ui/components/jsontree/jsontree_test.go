package jsontree

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/theme"
)

var styles = theme.NewStyles(theme.Fallback(), 1)

const doc = `{"Id":"abc","State":{"Status":"running","Health":{"Status":"healthy"}},
"Config":{"Env":["PORT=8080","DB_PASSWORD=hunter2"],"Labels":{"image.description":"API"}},
"Secret":{"api_token":"t0k"},"Ports":null,"Count":3,"On":true,"Empty":{}}`

func TestParseOrderAndPaths(t *testing.T) {
	m, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, c := range m.root.Children {
		keys = append(keys, c.Key)
	}
	if strings.Join(keys, ",") != "Id,State,Config,Secret,Ports,Count,On,Empty" {
		t.Fatalf("order %v", keys)
	}
	env := m.root.Children[2].Children[0]
	if env.Children[1].Path != ".Config.Env[1]" || env.Children[1].Value != `"DB_PASSWORD=••••••"` {
		t.Fatalf("env item %+v", env.Children[1])
	}
	lbl := m.root.Children[2].Children[1].Children[0]
	if lbl.Path != `.Config.Labels["image.description"]` {
		t.Fatalf("label path %q", lbl.Path)
	}
	if v := m.root.Children[3].Children[0].Value; v != `"••••••"` {
		t.Fatalf("secret key value %q", v)
	}
}

func TestNavigation(t *testing.T) {
	m, _ := Parse([]byte(doc))
	n := len(m.Rows())
	m.Move(1) // Id
	m.Move(1) // State (expanded: 2 children)
	if m.Cursor().Key != "State" || !m.Cursor().Expanded {
		t.Fatalf("cursor %q expanded %v", m.Cursor().Key, m.Cursor().Expanded)
	}
	m.Collapse()
	if m.Cursor().Expanded || len(m.Rows()) != n-2 {
		t.Fatalf("collapse: %d rows", len(m.Rows()))
	}
	m.Expand()
	m.Expand() // into the first child
	if m.Cursor().Key != "Status" {
		t.Fatalf("expand into child: %q", m.Cursor().Key)
	}
	m.Collapse() // leaf: to parent
	if m.Cursor().Key != "State" {
		t.Fatalf("collapse to parent: %q", m.Cursor().Key)
	}
	m.ExpandAll()
	if !m.Cursor().Children[1].Expanded {
		t.Fatal("expand all")
	}
}

func TestSearch(t *testing.T) {
	m, _ := Parse([]byte(doc))
	m.SetQuery("healthy")
	if total, cur := m.Matches(); total != 1 || cur != 1 || m.Cursor().Path != ".State.Health.Status" {
		t.Fatalf("matches %d/%d at %q", cur, total, m.Cursor().Path)
	}
	m.SetQuery("status")
	if total, _ := m.Matches(); total != 2 {
		t.Fatalf("%d matches for status", total)
	}
	m.NextMatch(true)
	m.NextMatch(true) // wraps
	if _, cur := m.Matches(); cur != 1 {
		t.Fatalf("wrap: %d", cur)
	}
	if m.SetQuery("hunter2"); func() int { n, _ := m.Matches(); return n }() != 0 {
		t.Fatal("search found a masked secret")
	}
}

func TestRenderFits(t *testing.T) {
	m, _ := Parse([]byte(doc))
	out := m.Render(styles, 40, 8)
	rows := strings.Split(out, "\n")
	if len(rows) != 8 {
		t.Fatalf("%d rows", len(rows))
	}
	for _, r := range rows {
		if ansi.StringWidth(r) != 40 {
			t.Fatalf("row %q", ansi.Strip(r))
		}
	}
	if !strings.Contains(ansi.Strip(out), "▾ State: {") {
		t.Fatalf("render %q", ansi.Strip(out))
	}
}

func TestLimits(t *testing.T) {
	if _, err := Parse([]byte(`{"a":`)); err == nil {
		t.Fatal("truncated JSON accepted")
	}
	deep := strings.Repeat("[", 100) + strings.Repeat("]", 100)
	if _, err := Parse([]byte(deep)); err == nil {
		t.Fatal("deep nesting accepted")
	}
	big := "[" + strings.Repeat("1,", MaxNodes+10) + "1]"
	m, err := Parse([]byte(big))
	if err != ErrTooLarge || !m.Truncated() {
		t.Fatalf("big doc: %v", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(doc))
	f.Add([]byte(`[1,"a",{"b":null}]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := Parse(data)
		if err != nil && m == nil {
			return
		}
		_ = m.Render(styles, 50, 10)
		m.SetQuery("a")
		m.NextMatch(true)
		m.ExpandAll()
		m.End()
		m.Collapse()
	})
}
