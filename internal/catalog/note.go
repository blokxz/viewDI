package catalog

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MaxNote caps the post-create note.
const MaxNote = 600

var placeholderRE = regexp.MustCompile(`\{\{\s*(name|port\s+[0-9]{1,5})\s*\}\}`)

// ValidateNote checks the placeholders of a note: {{name}} and {{port N}}.
// Any other "{{" is an error so a typo is noticed when the file loads, not
// when somebody reads the note.
func ValidateNote(note string) error {
	if len(note) > MaxNote {
		return fmt.Errorf("at most %d characters", MaxNote)
	}
	for _, r := range note {
		if (r < ' ' && r != '\n') || r == 0x7f {
			return fmt.Errorf("control characters are not allowed")
		}
	}
	rest := placeholderRE.ReplaceAllString(note, "")
	if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
		return fmt.Errorf("unknown placeholder (only {{name}} and {{port N}})")
	}
	for _, m := range placeholderRE.FindAllStringSubmatch(note, -1) {
		if f := strings.Fields(m[1]); len(f) == 2 {
			if n, _ := strconv.Atoi(f[1]); n < 1 || n > 65535 {
				return fmt.Errorf("port %s out of range", f[1])
			}
		}
	}
	return nil
}

// RenderNote fills in the placeholders: name is the instance's real name and
// hostPorts maps a container port to the host port it was published on (a
// port that was not published renders as its own number).
func RenderNote(note, name string, hostPorts map[uint16]uint16) string {
	return placeholderRE.ReplaceAllStringFunc(note, func(m string) string {
		inner := strings.Trim(m, "{} \t")
		f := strings.Fields(inner)
		if len(f) == 1 { // name
			return name
		}
		n, _ := strconv.Atoi(f[1])
		if h, ok := hostPorts[uint16(n)]; ok && h != 0 {
			return strconv.Itoa(int(h))
		}
		return strconv.Itoa(n)
	})
}
