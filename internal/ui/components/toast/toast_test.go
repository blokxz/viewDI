package toast

import (
	"testing"

	"github.com/blokxz/viewDI/internal/ui/theme"
)

func TestPushDedupAndCap(t *testing.T) {
	q := New(DefaultTTL, 2)
	q, cmd := q.Push(theme.LevelInfo, "a")
	if cmd == nil {
		t.Fatal("push must schedule expiry")
	}
	q, _ = q.Push(theme.LevelInfo, "b")
	q, _ = q.Push(theme.LevelWarn, "a") // moves "a" to the top
	if got := q.Items(); len(got) != 2 || got[0].Text != "b" || got[1].Text != "a" || got[1].Level != theme.LevelWarn {
		t.Fatalf("items = %+v", got)
	}
	q, _ = q.Push(theme.LevelInfo, "c") // cap drops the oldest
	if got := q.Items(); len(got) != 2 || got[0].Text != "a" {
		t.Fatalf("items = %+v", got)
	}
	q, _ = q.Push(theme.LevelInfo, "   ")
	if len(q.Items()) != 2 {
		t.Fatal("blank toast must be ignored")
	}
}

func TestExpiry(t *testing.T) {
	q := New(DefaultTTL, 3)
	q, _ = q.Update(ShowMsg{Level: theme.LevelOK, Text: "one"})
	q, _ = q.Update(ShowMsg{Level: theme.LevelOK, Text: "two"})
	first := q.Items()[0].ID
	q, _ = q.Update(ExpiredMsg{ID: first})
	if got := q.Items(); len(got) != 1 || got[0].Text != "two" {
		t.Fatalf("items = %+v", got)
	}
	q, _ = q.Update(ExpiredMsg{ID: 999}) // unknown ID is a no-op
	if len(q.Items()) != 1 {
		t.Fatal("unknown expiry removed a toast")
	}
	if q.View(theme.NewStyles(theme.Fallback(), 1), 40) == "" {
		t.Fatal("non-empty queue must render")
	}
}
