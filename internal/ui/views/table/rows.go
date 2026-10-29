package table

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/blokxz/viewDI/internal/engine"
)

// Column is a sortable column.
type Column int

// Sortable columns, in the order "o" cycles through them.
const (
	ColName Column = iota
	ColEngine
	ColKind
	ColState
	ColImage
	ColUptime
	numColumns
)

var columnIDs = [numColumns]string{"name", "engine", "kind", "state", "image", "uptime"}

// ID is the column's persisted identifier.
func (c Column) ID() string { return columnIDs[c] }

func columnByID(id string) (Column, bool) {
	i := slices.Index(columnIDs[:], id)
	return Column(i), i >= 0
}

// stateRank orders states for sorting: live first.
func stateRank(s engine.State) int {
	switch s {
	case engine.StateRunning:
		return 0
	case engine.StateRestarting:
		return 1
	case engine.StatePaused:
		return 2
	case engine.StateCreated:
		return 4
	case engine.StateStopped:
		return 5
	}
	return 3 // error, dead, removing, unknown: between live and stopped
}

func sortRows(rows []engine.Instance, col Column, reverse bool) {
	slices.SortStableFunc(rows, func(a, b engine.Instance) int {
		var c int
		switch col {
		case ColEngine:
			c = cmp.Compare(a.Engine, b.Engine)
		case ColKind:
			c = cmp.Compare(a.Kind, b.Kind)
		case ColState:
			c = cmp.Compare(stateRank(a.State), stateRank(b.State))
		case ColImage:
			c = strings.Compare(strings.ToLower(a.Image), strings.ToLower(b.Image))
		case ColUptime: // longest running first; not running last
			c = cmp.Compare(startKey(a), startKey(b))
		}
		if c == 0 {
			c = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}
		if c == 0 {
			c = cmp.Compare(a.Key(), b.Key())
		}
		if reverse {
			return -c
		}
		return c
	})
}

func startKey(in engine.Instance) int64 {
	if in.State != engine.StateRunning || in.Started.IsZero() {
		return 1<<63 - 1
	}
	return in.Started.UnixNano()
}

// match reports whether an instance matches the lowercase query: a fuzzy
// (subsequence) match on the name, or a substring of the image, project,
// engine or state.
func match(q string, in engine.Instance) bool {
	if fuzzy(q, strings.ToLower(in.Name)) {
		return true
	}
	for _, f := range []string{in.Image, in.Project, string(in.Engine), in.State.String()} {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

// fuzzy reports whether every rune of q appears in s, in order. Spaces in q
// are ignored.
func fuzzy(q, s string) bool {
	for _, r := range q {
		if unicode.IsSpace(r) {
			continue
		}
		i := strings.IndexRune(s, r)
		if i < 0 {
			return false
		}
		s = s[i+utf8.RuneLen(r):]
	}
	return true
}
