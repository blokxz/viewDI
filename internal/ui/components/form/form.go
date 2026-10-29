// Package form is a modal form: text fields with validation and
// suggestions, option selects and checkboxes, navigated with tab/↑↓.
// enter submits (or presses the focused button), esc cancels. Text fields
// get printable keys first, so space types a space there and ticks a box
// elsewhere.
package form

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/secret"
	"github.com/blokxz/viewDI/internal/ui/keys"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// Kind is a field kind.
type Kind int

// Field kinds.
const (
	Text Kind = iota
	Select
	Check
)

// Field is one form field.
type Field struct {
	ID    string
	Label string
	Kind  Kind
	// Value is a Text field's content or a Select field's chosen option.
	Value string
	// Options are a Select field's choices; Labels (optional) their display.
	Options []string
	Labels  []string
	// On is a Check field's state.
	On bool
	// Placeholder is shown in an empty Text field; Hint under the focused
	// field.
	Placeholder string
	Hint        string
	Required    bool
	// Validate checks a Text field's value (after trimming).
	Validate func(string) error
	// Suggestions are offered for a Text field by substring match.
	Suggestions []string
	// Searchable fields ask the app for remote suggestions with ctrl+f.
	Searchable bool
	// MaskSecrets masks secret-looking "KEY=value" entries while the field
	// is not focused.
	MaskSecrets bool
}

// Values are the submitted values by field ID: text (trimmed), the chosen
// option, or "true"/"" for checkboxes.
type Values map[string]string

// Bool reports a checkbox.
func (v Values) Bool(id string) bool { return v[id] == "true" }

// SubmittedMsg carries a valid form's values; Kind tells forms apart and
// Context is the opener's data, handed back untouched.
type SubmittedMsg struct {
	Kind    string
	Context any
	Values  Values
}

// SearchMsg asks the app for remote suggestions for a field.
type SearchMsg struct {
	Kind, Field, Query string
}

// SuggestionsMsg delivers suggestions for a field of the open form of
// Kind. Remote ones replace the previous remote ones and are offered
// first; Note (or Err) is shown under the field.
type SuggestionsMsg struct {
	Kind, Field string
	Items       []string
	Remote      bool
	Note        string
	Err         error
}

const (
	maxSuggestions = 6
	maxValue       = 1024 // runes per text field
	labelWidth     = 13
)

type field struct {
	Field
	pos    int      // cursor, in runes
	remote []string // search results
	sug    int      // highlighted suggestion; -1 none
	query  string   // the typed text the suggestions match while cycling
	err    string
	note   string
}

// Model is a form.
type Model struct {
	keys   *keys.Map
	kind   string
	ctx    any
	title  string
	intro  string
	submit string
	fields []*field
	focus  int // 0..len(fields)-1, then Cancel, Submit
}

var _ modal.Modal = (*Model)(nil)

// New builds a form. submit labels the submit button ("Create").
func New(km *keys.Map, kind, title, intro, submit string, fields []Field) *Model {
	m := &Model{keys: km, kind: kind, title: title, intro: intro, submit: submit}
	for _, f := range fields {
		if f.Kind == Select && f.Value == "" && len(f.Options) > 0 {
			f.Value = f.Options[0]
		}
		m.fields = append(m.fields, &field{Field: f, pos: utf8.RuneCountInString(f.Value), sug: -1})
	}
	return m
}

// WithContext attaches opener data returned in SubmittedMsg.
func (m *Model) WithContext(ctx any) *Model {
	m.ctx = ctx
	return m
}

// Kind returns the form kind.
func (m *Model) Kind() string { return m.kind }

// Scope implements modal.Modal.
func (m *Model) Scope() keys.Scope { return keys.Form }

// Value returns a field's current value.
func (m *Model) Value(id string) string {
	if f := m.field(id); f != nil {
		if f.Kind == Check {
			return map[bool]string{true: "true"}[f.On]
		}
		return strings.TrimSpace(f.Value)
	}
	return ""
}

// Error returns a field's validation error.
func (m *Model) Error(id string) string {
	if f := m.field(id); f != nil {
		return f.err
	}
	return ""
}

// Focused returns the focused field ID ("" on a button).
func (m *Model) Focused() string {
	if m.focus < len(m.fields) {
		return m.fields[m.focus].ID
	}
	return ""
}

func (m *Model) field(id string) *field {
	for _, f := range m.fields {
		if f.ID == id {
			return f
		}
	}
	return nil
}

func (m *Model) cancelIndex() int { return len(m.fields) }
func (m *Model) count() int       { return len(m.fields) + 2 }

// Update implements modal.Modal.
func (m *Model) Update(msg tea.Msg) (modal.Modal, tea.Cmd) {
	switch msg := msg.(type) {
	case SuggestionsMsg:
		m.suggestions(msg)
		return m, nil
	case tea.PasteMsg:
		if f := m.focusedText(); f != nil {
			f.insert(msg.Content)
		}
		return m, nil
	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *Model) suggestions(msg SuggestionsMsg) {
	f := m.field(msg.Field)
	if msg.Kind != m.kind || f == nil {
		return
	}
	switch {
	case msg.Err != nil:
		f.note = "✖ " + msg.Err.Error()
	case msg.Remote:
		f.remote = cleanAll(msg.Items)
		f.note = msg.Note
	default:
		f.Suggestions = cleanAll(msg.Items)
		if msg.Note != "" {
			f.note = msg.Note
		}
	}
	f.sug = -1
}

func cleanAll(items []string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it = textsafe.Clean(it); it != "" {
			out = append(out, it)
		}
	}
	return out
}

func (m *Model) focusedText() *field {
	if m.focus < len(m.fields) && m.fields[m.focus].Kind == Text {
		return m.fields[m.focus]
	}
	return nil
}

func (m *Model) key(kp tea.KeyPressMsg) tea.Cmd {
	action := m.keys.Action(kp, keys.Form)
	switch action {
	case "next", "prev":
		m.leave()
		step := 1
		if action == "prev" {
			step = m.count() - 1
		}
		m.focus = (m.focus + step) % m.count()
		return nil
	case keys.ActCancel:
		return modal.Close
	case keys.ActActivate:
		if m.focus == m.cancelIndex() {
			return modal.Close
		}
		return m.submitCmd()
	case "suggest_next", "suggest_prev":
		if f := m.focusedText(); f != nil {
			f.cycle(action == "suggest_next")
		}
		return nil
	case "search":
		if f := m.focusedText(); f != nil && f.Searchable {
			q := strings.TrimSpace(f.query)
			if f.sug < 0 {
				q = strings.TrimSpace(f.Value)
			}
			f.note = "searching " + q + "…"
			kind, id := m.kind, f.ID
			return func() tea.Msg { return SearchMsg{Kind: kind, Field: id, Query: q} }
		}
		return nil
	}
	if f := m.focusedText(); f != nil {
		f.edit(kp)
		return nil
	}
	if m.focus >= len(m.fields) {
		if action == "left" || action == "right" {
			m.focus = m.cancelIndex() + map[string]int{"left": 0, "right": 1}[action]
		}
		return nil
	}
	f := m.fields[m.focus]
	switch {
	case f.Kind == Select && (action == "left" || action == "right") && len(f.Options) > 0:
		i := index(f.Options, f.Value)
		if action == "left" {
			i += len(f.Options) - 1
		} else {
			i++
		}
		f.Value = f.Options[i%len(f.Options)]
	case f.Kind == Check && action == "toggle":
		f.On = !f.On
	}
	return nil
}

func index(opts []string, v string) int {
	for i, o := range opts {
		if o == v {
			return i
		}
	}
	return 0
}

// leave validates the field being left, so errors show early.
func (m *Model) leave() {
	if m.focus < len(m.fields) {
		f := m.fields[m.focus]
		f.sug = -1
		if strings.TrimSpace(f.Value) != "" {
			f.check()
		}
	}
}

func (f *field) check() bool {
	f.err = ""
	if f.Kind != Text {
		return true
	}
	v := strings.TrimSpace(f.Value)
	switch {
	case v == "" && f.Required:
		f.err = "required"
	case v != "" && f.Validate != nil:
		if err := f.Validate(v); err != nil {
			f.err = err.Error()
		}
	}
	return f.err == ""
}

func (m *Model) submitCmd() tea.Cmd {
	first := -1
	for i, f := range m.fields {
		if !f.check() && first < 0 {
			first = i
		}
	}
	if first >= 0 {
		m.focus = first
		return nil
	}
	vals := Values{}
	for _, f := range m.fields {
		vals[f.ID] = m.Value(f.ID)
	}
	msg := SubmittedMsg{Kind: m.kind, Context: m.ctx, Values: vals}
	return tea.Sequence(modal.Close, func() tea.Msg { return msg })
}

// ----- text editing -----

func (f *field) runes() []rune { return []rune(f.Value) }

func (f *field) set(r []rune, pos int) {
	f.Value, f.pos = string(r), max(0, min(pos, len(r)))
	f.sug, f.err = -1, ""
}

func (f *field) insert(s string) {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, textsafe.Clean(s))
	r := f.runes()
	add := []rune(s)
	add = add[:min(len(add), max(0, maxValue-len(r)))]
	if len(add) == 0 {
		return
	}
	out := append(append(append([]rune{}, r[:f.pos]...), add...), r[f.pos:]...)
	f.set(out, f.pos+len(add))
}

func (f *field) edit(kp tea.KeyPressMsg) {
	r := f.runes()
	switch kp.String() {
	case "left":
		f.pos = max(0, f.pos-1)
	case "right":
		f.pos = min(len(r), f.pos+1)
	case "home", "ctrl+a":
		f.pos = 0
	case "end", "ctrl+e":
		f.pos = len(r)
	case "backspace":
		if f.pos > 0 {
			f.set(append(r[:f.pos-1:f.pos-1], r[f.pos:]...), f.pos-1)
		}
	case "delete", "ctrl+d":
		if f.pos < len(r) {
			f.set(append(r[:f.pos:f.pos], r[f.pos+1:]...), f.pos)
		}
	case "ctrl+u":
		f.set(r[f.pos:], 0)
	case "ctrl+k":
		f.set(r[:f.pos], f.pos)
	case "ctrl+w":
		i := f.pos
		for i > 0 && r[i-1] == ' ' {
			i--
		}
		for i > 0 && r[i-1] != ' ' && r[i-1] != ',' {
			i--
		}
		f.set(append(r[:i:i], r[f.pos:]...), i)
	default:
		if kp.Text != "" {
			f.insert(kp.Text)
		}
	}
}

// matches returns the suggestions for the typed text: remote results
// first, then local ones, by substring.
func (f *field) matches() []string {
	q := f.Value
	if f.sug >= 0 {
		q = f.query
	}
	q = strings.ToLower(strings.TrimSpace(q))
	var out []string
	seen := map[string]bool{}
	for _, list := range [][]string{f.remote, f.Suggestions} {
		for _, s := range list {
			if !seen[s] && strings.Contains(strings.ToLower(s), q) {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

func (f *field) cycle(forward bool) {
	if f.sug < 0 {
		f.query = f.Value
	}
	all := f.matches()
	if len(all) == 0 {
		return
	}
	switch {
	case f.sug < 0 && forward:
		f.sug = 0
	case f.sug < 0:
		f.sug = len(all) - 1
	case forward:
		f.sug = (f.sug + 1) % len(all)
	default:
		f.sug = (f.sug + len(all) - 1) % len(all)
	}
	f.Value, f.err = all[f.sug], ""
	f.pos = utf8.RuneCountInString(f.Value)
}

// ----- rendering -----

// View implements modal.Modal.
func (m *Model) View(s *theme.Styles, width, height int) string {
	inputW := max(16, min(52, width-labelWidth-14))
	var lines []string
	focusLine := 0
	if m.intro != "" {
		for _, l := range strings.Split(m.intro, "\n") {
			lines = append(lines, s.Muted.Render(ansi.Truncate(l, labelWidth+inputW+2, "…")))
		}
		lines = append(lines, "")
	}
	for i, f := range m.fields {
		focused := i == m.focus
		if focused {
			focusLine = len(lines)
		}
		lines = append(lines, m.fieldLines(s, f, focused, inputW)...)
	}
	if m.focus >= len(m.fields) {
		focusLine = len(lines) + 1
	}
	cancel, submit := s.Muted.Padding(0, 1), s.Button.Faint(true)
	switch m.focus {
	case m.cancelIndex():
		cancel = s.Selected.Padding(0, 1)
	case m.cancelIndex() + 1:
		submit = s.Button
	}
	lines = append(lines, "", strings.Repeat(" ", labelWidth)+lipgloss.JoinHorizontal(lipgloss.Center, cancel.Render("Cancel"), "  ", submit.Render(m.submit)))
	hint := "tab/↑↓ move · ←/→ choose · space tick · ctrl+n/p suggestions"
	if f := m.focusedText(); f != nil && f.Searchable {
		hint += " · ctrl+f search"
	}
	hint += " · enter " + strings.ToLower(m.submit) + " · esc cancel"
	lines = append(lines, "", s.Muted.Render(ansi.Truncate(hint, labelWidth+inputW+2, "…")))

	// Tall forms scroll to keep the focused field visible.
	room := max(6, height-8)
	if len(lines) > room {
		top := max(0, min(focusLine-room/3, len(lines)-room))
		lines = lines[top : top+room]
	}
	return modal.Frame(s, m.title, strings.Join(lines, "\n"), width)
}

func (m *Model) fieldLines(s *theme.Styles, f *field, focused bool, inputW int) []string {
	label := s.Muted.Render(fmt.Sprintf("%-*s", labelWidth, ansi.Truncate(f.Label, labelWidth-1, "…")))
	if focused {
		label = s.Bold.Render(fmt.Sprintf("%-*s", labelWidth, ansi.Truncate(f.Label, labelWidth-1, "…")))
	}
	var input string
	switch f.Kind {
	case Text:
		input = textBox(s, f, focused, inputW)
	case Select:
		text := f.Value
		if i := index(f.Options, f.Value); i < len(f.Labels) {
			text = f.Labels[i]
		}
		box := "◂ " + ansi.Truncate(text, inputW-4, "…") + " ▸"
		if focused {
			input = s.Selected.Render(box)
		} else {
			input = s.Text.Render(box)
		}
	case Check:
		box := "[ ]"
		if f.On {
			box = "[x]"
		}
		input = s.Text.Render(box)
		if focused {
			input = s.Selected.Render(box)
		}
		if f.Hint != "" {
			input += " " + s.Muted.Render(ansi.Truncate(f.Hint, inputW-4, "…"))
		}
	}
	lines := []string{label + input}
	indent := strings.Repeat(" ", labelWidth)
	if f.err != "" {
		lines = append(lines, indent+s.LevelText(theme.LevelErr).Render(ansi.Truncate("✖ "+f.err, inputW, "…")))
	}
	if !focused {
		return lines
	}
	if f.Kind != Check && f.Hint != "" {
		lines = append(lines, indent+s.Muted.Render(ansi.Truncate(f.Hint, inputW, "…")))
	}
	if f.note != "" {
		lines = append(lines, indent+s.Muted.Render(ansi.Truncate(f.note, inputW, "…")))
	}
	if f.Kind == Text {
		all := f.matches()
		if len(all) == 1 && all[0] == strings.TrimSpace(f.Value) && f.sug < 0 {
			all = nil // the value is the only match: nothing to offer
		}
		start := 0
		if f.sug >= maxSuggestions {
			start = f.sug - maxSuggestions + 1
		}
		for i := start; i < len(all) && i < start+maxSuggestions; i++ {
			text := ansi.Truncate(all[i], inputW-2, "…")
			if i == f.sug {
				lines = append(lines, indent+s.Selected.Render("▸ "+text))
			} else {
				lines = append(lines, indent+s.Muted.Render("  "+text))
			}
		}
		if more := len(all) - start - maxSuggestions; more > 0 {
			lines = append(lines, indent+s.Muted.Render(fmt.Sprintf("  … %d more (ctrl+n)", more)))
		}
	}
	return lines
}

// textBox renders a text field: the value with a cursor when focused, the
// placeholder when empty, masked secrets when not focused.
func textBox(s *theme.Styles, f *field, focused bool, w int) string {
	r := f.runes()
	if !focused {
		v := f.Value
		if f.MaskSecrets {
			v = maskEnv(v)
		}
		if v == "" {
			return s.Muted.Render(pad(ansi.Truncate(f.Placeholder, w, "…"), w))
		}
		return s.Text.Render(pad(ansi.Truncate(v, w, "…"), w))
	}
	if len(r) == 0 && f.Placeholder != "" {
		return s.Selected.Render("_") + s.Muted.Render(pad(ansi.Truncate(f.Placeholder, w-1, "…"), w-1))
	}
	// Keep the cursor visible in long values.
	start := max(0, f.pos-(w-2))
	end := min(len(r), start+w-1)
	before, at, after := string(r[start:f.pos]), " ", ""
	if f.pos < len(r) {
		at = string(r[f.pos])
		after = string(r[f.pos+1 : max(f.pos+1, end)])
	}
	return s.Selected.Render(before) + s.Text.Reverse(true).Render(at) + s.Selected.Render(pad(after, w-1-ansi.StringWidth(before)))
}

func maskEnv(v string) string {
	parts := strings.Split(v, ",")
	for i, p := range parts {
		lead := len(p) - len(strings.TrimLeft(p, " "))
		masked, _ := secret.Env(strings.TrimSpace(p))
		parts[i] = p[:lead] + masked
	}
	return strings.Join(parts, ",")
}

func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}
