// Package files is a file picker modal: it lists a folder (in a command,
// never in Update), shows sub-folders and the files whose names end with
// one of the wanted extensions, and reports the chosen file.
package files

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/metrics"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// MaxEntries bounds a listing (huge folders are cut, with a note).
const MaxEntries = 5000

// Entry is one listed name.
type Entry struct {
	Name string // raw name (display through textsafe)
	Dir  bool
	Size int64
}

// ListedMsg delivers a folder listing to the picker that asked for it.
type ListedMsg struct {
	seq     uint64
	Dir     string
	Entries []Entry
	Cut     bool
	Err     error
}

// ChosenMsg reports the chosen file. Kind and Context come from New.
type ChosenMsg struct {
	Kind    string
	Context any
	Path    string
	Size    int64
}

// Model is the picker.
type Model struct {
	keys    *keys.Map
	kind    string
	ctx     any
	title   string
	home    string
	exts    []string
	dir     string
	all     bool // show every file, not only matching ones
	hidden  bool
	entries []Entry // the listing
	shown   []Entry // after filters, with ".." first
	cut     bool
	err     string
	loading bool
	seq     uint64
	cursor  int
	offset  int
	rows    int    // list rows, from the screen height
	want    string // name to put the cursor on after a listing (parent)
}

func rowsFor(height int) int { return max(3, min(16, height-14)) }

var _ modal.Modal = (*Model)(nil)

// New opens a picker in dir showing files ending in exts (all files when
// empty) and returns the command that lists dir.
func New(km *keys.Map, kind, title, dir, home string, exts []string) (*Model, tea.Cmd) {
	m := &Model{keys: km, kind: kind, title: title, home: home, exts: exts, all: len(exts) == 0, rows: 16}
	return m, m.open(dir)
}

// WithContext attaches opener data returned in ChosenMsg.
func (m *Model) WithContext(ctx any) *Model {
	m.ctx = ctx
	return m
}

// Dir returns the folder shown.
func (m *Model) Dir() string { return m.dir }

// Scope implements modal.Modal.
func (m *Model) Scope() keys.Scope { return keys.Files }

func (m *Model) open(dir string) tea.Cmd {
	dir = filepath.Clean(dir)
	m.seq++
	m.loading, m.err = true, ""
	seq := m.seq
	return func() tea.Msg { return list(seq, dir) }
}

// list reads a folder: sub-folders (following links) and regular files.
func list(seq uint64, dir string) ListedMsg {
	msg := ListedMsg{seq: seq, Dir: dir}
	f, err := os.Open(dir)
	if err != nil {
		msg.Err = err
		return msg
	}
	defer f.Close()
	des, err := f.ReadDir(MaxEntries + 1)
	if err != nil && len(des) == 0 {
		msg.Err = err
		return msg
	}
	if len(des) > MaxEntries {
		des, msg.Cut = des[:MaxEntries], true
	}
	for _, de := range des {
		e := Entry{Name: de.Name(), Dir: de.IsDir()}
		if de.Type()&os.ModeSymlink != 0 || !e.Dir {
			fi, err := os.Stat(filepath.Join(dir, de.Name()))
			if err != nil {
				continue // dangling link
			}
			if !fi.IsDir() && !fi.Mode().IsRegular() {
				continue // sockets, devices, pipes
			}
			e.Dir, e.Size = fi.IsDir(), fi.Size()
		}
		msg.Entries = append(msg.Entries, e)
	}
	slices.SortFunc(msg.Entries, func(a, b Entry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return msg
}

// Matches reports whether a file name has one of the wanted extensions.
func Matches(name string, exts []string) bool {
	lower := strings.ToLower(name)
	for _, e := range exts {
		if strings.HasSuffix(lower, e) {
			return true
		}
	}
	return len(exts) == 0
}

func (m *Model) filter() {
	m.shown = m.shown[:0]
	if m.dir != filepath.Dir(m.dir) {
		m.shown = append(m.shown, Entry{Name: "..", Dir: true})
	}
	for _, e := range m.entries {
		switch {
		case !m.hidden && strings.HasPrefix(e.Name, "."):
		case e.Dir, m.all, Matches(e.Name, m.exts):
			m.shown = append(m.shown, e)
		}
	}
	m.cursor = max(0, min(m.cursor, len(m.shown)-1))
	m.scroll()
}

// scroll keeps the cursor in the visible rows.
func (m *Model) scroll() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.rows {
		m.offset = m.cursor - m.rows + 1
	}
	m.offset = max(0, min(m.offset, len(m.shown)-m.rows))
}

// Update implements modal.Modal.
func (m *Model) Update(msg tea.Msg) (modal.Modal, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.rows = rowsFor(msg.Height)
		m.scroll()
	case ListedMsg:
		if msg.seq != m.seq {
			return m, nil // an older listing
		}
		m.loading = false
		if msg.Err != nil {
			m.err = msg.Err.Error()
			var pe *os.PathError
			if errors.As(msg.Err, &pe) {
				m.err = pe.Err.Error() // the path is on the line above
			}
			if m.dir == "" { // the first folder failed: stay there, empty
				m.dir = msg.Dir
			}
			return m, nil
		}
		m.dir, m.entries, m.cut, m.cursor, m.offset = msg.Dir, msg.Entries, msg.Cut, 0, 0
		m.filter()
		if m.want != "" {
			for i, e := range m.shown {
				if e.Name == m.want {
					m.cursor = i
				}
			}
			m.want = ""
		}
		m.scroll()
		return m, nil
	case tea.KeyPressMsg:
		cmd := m.key(msg)
		m.scroll()
		return m, cmd
	}
	return m, nil
}

func (m *Model) key(kp tea.KeyPressMsg) tea.Cmd {
	switch m.keys.Action(kp, keys.Files) {
	case keys.ActCancel:
		return modal.Close
	case keys.ActUp:
		if len(m.shown) > 0 {
			m.cursor = (m.cursor + len(m.shown) - 1) % len(m.shown)
		}
	case keys.ActDown:
		if len(m.shown) > 0 {
			m.cursor = (m.cursor + 1) % len(m.shown)
		}
	case "parent":
		return m.parent()
	case "home":
		if m.home != "" {
			return m.open(m.home)
		}
	case "all":
		m.all = !m.all || len(m.exts) == 0
		m.filter()
	case "hidden":
		m.hidden = !m.hidden
		m.filter()
	case "open":
		if m.loading || m.cursor >= len(m.shown) {
			return nil
		}
		e := m.shown[m.cursor]
		switch {
		case e.Name == "..":
			return m.parent()
		case e.Dir:
			return m.open(filepath.Join(m.dir, e.Name))
		}
		msg := ChosenMsg{Kind: m.kind, Context: m.ctx, Path: filepath.Join(m.dir, e.Name), Size: e.Size}
		return tea.Sequence(modal.Close, func() tea.Msg { return msg })
	}
	return nil
}

func (m *Model) parent() tea.Cmd {
	up := filepath.Dir(m.dir)
	if up == m.dir {
		return nil
	}
	m.want = filepath.Base(m.dir)
	return m.open(up)
}

// View implements modal.Modal.
func (m *Model) View(s *theme.Styles, width, height int) string {
	w := max(30, min(72, width-10))
	rows := min(m.rows, rowsFor(height))
	var b strings.Builder
	b.WriteString(s.Bold.Render(ansi.TruncateLeft(textsafe.Clean(m.dir), max(0, ansi.StringWidth(textsafe.Clean(m.dir))-w+1), "…")))
	b.WriteString("\n")
	what := "all files"
	if !m.all {
		what = "*" + strings.Join(m.exts, ", *")
	}
	b.WriteString(s.Muted.Render(ansi.Truncate("showing "+what, w, "…")) + "\n\n")
	switch {
	case m.loading && m.entries == nil:
		b.WriteString(s.Muted.Render("Reading…"))
	case m.err != "":
		b.WriteString(s.LevelText(theme.LevelErr).Render(ansi.Truncate("✖ "+m.err, w, "…")))
	case len(m.shown) == 0:
		b.WriteString(s.Muted.Render("Nothing to show here."))
	}
	offset := m.offset
	if m.cursor >= offset+rows { // the screen shrank since the last update
		offset = m.cursor - rows + 1
	}
	if m.err == "" {
		for i := offset; i < len(m.shown) && i < offset+rows; i++ {
			e := m.shown[i]
			name, size := textsafe.Clean(e.Name), ""
			if e.Dir {
				name += "/"
			} else {
				size = metrics.Bytes(uint64(max(e.Size, 0)))
			}
			icon := "  "
			if e.Dir {
				icon = "▸ "
			}
			line := icon + ansi.Truncate(name, w-12, "…")
			line += strings.Repeat(" ", max(1, w-ansi.StringWidth(line)-len(size))) + size
			switch {
			case i == m.cursor:
				b.WriteString(s.Selected.Render(line))
			case e.Dir:
				b.WriteString(s.Text.Render(line))
			default:
				b.WriteString(s.Muted.Render(line))
			}
			if i < len(m.shown)-1 && i < offset+rows-1 {
				b.WriteString("\n")
			}
		}
	}
	footer := fmt.Sprintf("%d of %d", min(m.cursor+1, len(m.shown)), len(m.shown))
	if m.cut {
		footer += fmt.Sprintf(" (first %d entries)", MaxEntries)
	}
	b.WriteString("\n\n" + s.Muted.Render(footer))
	b.WriteString("\n" + s.Muted.Render(ansi.Truncate("enter open/choose · ⌫ parent · ~ home · a all files · . hidden · esc cancel", w, "…")))
	return modal.Frame(s, m.title, b.String(), width)
}
