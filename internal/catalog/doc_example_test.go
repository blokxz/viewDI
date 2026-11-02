package catalog

import (
	"os"
	"strings"
	"testing"
)

// TestUserGuideExampleParses keeps the user guide's example entry valid.
func TestUserGuideExampleParses(t *testing.T) {
	data, err := os.ReadFile("../../docs/08-user-guide.md")
	if err != nil {
		t.Skip("docs not available")
	}
	const open, closeFence = "```toml\n", "```"
	s := string(data)
	i := strings.Index(s, open)
	if i < 0 {
		t.Fatal("no toml example in the user guide")
	}
	s = s[i+len(open):]
	body, _, _ := strings.Cut(s, closeFence)
	entries, err := Parse([]byte(body), User)
	if err != nil || len(entries) != 1 || entries[0].ID != "my-app" {
		t.Fatalf("example: %v %+v", err, entries)
	}
}
