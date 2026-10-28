// Package toast implements transient notifications stacked in the bottom-right
// corner. Each toast expires on its own timer; identical text is not stacked twice.
package toast

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Defaults.
const (
	DefaultTTL     = 4 * time.Second
	DefaultVisible = 3
	maxTextWidth   = 60
)

// Toast is one notification.
type Toast struct {
	ID    uint64
	Level theme.Level
	Text  string
}

// ShowMsg asks the app to show a toast.
type ShowMsg struct {
	Level theme.Level
	Text  string
}

// Show returns a command emitting ShowMsg.
func Show(level theme.Level, text string) tea.Cmd {
	return func() tea.Msg { return ShowMsg{Level: level, Text: text} }
}

// ExpiredMsg removes a toast when its timer fires.
type ExpiredMsg struct{ ID uint64 }

// Queue holds the visible toasts, oldest first.
type Queue struct {
	items   []Toast
	next    uint64
	ttl     time.Duration
	visible int
}

// New returns an empty queue.
func New(ttl time.Duration, visible int) Queue {
	return Queue{ttl: ttl, visible: visible}
}

// Items returns the visible toasts, oldest first.
func (q Queue) Items() []Toast { return q.items }

// Push adds a toast and returns the command that expires it. A toast whose
// text is already visible is moved to the top instead of duplicated.
func (q Queue) Push(level theme.Level, text string) (Queue, tea.Cmd) {
	text = strings.TrimSpace(text)
	if text == "" {
		return q, nil
	}
	kept := make([]Toast, 0, len(q.items)+1)
	for _, t := range q.items {
		if t.Text != text {
			kept = append(kept, t)
		}
	}
	q.next++
	t := Toast{ID: q.next, Level: level, Text: text}
	kept = append(kept, t)
	if over := len(kept) - q.visible; over > 0 {
		kept = kept[over:]
	}
	q.items = kept
	id := t.ID
	return q, tea.Tick(q.ttl, func(time.Time) tea.Msg { return ExpiredMsg{ID: id} })
}

// Update handles ShowMsg and ExpiredMsg.
func (q Queue) Update(msg tea.Msg) (Queue, tea.Cmd) {
	switch msg := msg.(type) {
	case ShowMsg:
		return q.Push(msg.Level, msg.Text)
	case ExpiredMsg:
		kept := q.items[:0:0]
		for _, t := range q.items {
			if t.ID != msg.ID {
				kept = append(kept, t)
			}
		}
		q.items = kept
	}
	return q, nil
}

var icons = [...]string{theme.LevelInfo: "ℹ", theme.LevelOK: "✓", theme.LevelWarn: "▲", theme.LevelErr: "✗"}

// View renders the toasts stacked vertically, newest at the bottom, each at
// most maxWidth cells wide. Returns "" when empty.
func (q Queue) View(s *theme.Styles, maxWidth int) string {
	if len(q.items) == 0 || maxWidth < 10 {
		return ""
	}
	boxes := make([]string, 0, len(q.items))
	for _, t := range q.items {
		style := s.Toast(t.Level)
		textW := min(maxTextWidth, maxWidth-style.GetHorizontalFrameSize()-2)
		icon := icons[0]
		if int(t.Level) < len(icons) {
			icon = icons[t.Level]
		}
		body := s.LevelText(t.Level).Render(icon) + " " + ansi.Truncate(t.Text, textW, "…")
		boxes = append(boxes, style.Render(body))
	}
	return lipgloss.JoinVertical(lipgloss.Right, boxes...)
}
