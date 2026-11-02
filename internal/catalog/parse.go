package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// file is the shape of a library file: any number of [[entry]] tables.
type file struct {
	Entry []Entry `toml:"entry"`
}

// Parse reads one library file. Unknown keys, an oversized file and any
// invalid entry are errors; the message names the entry. src is stamped on
// every entry.
func Parse(data []byte, src Source) ([]Entry, error) {
	if len(data) > MaxFileSize {
		return nil, fmt.Errorf("file is larger than %d KiB", MaxFileSize>>10)
	}
	var f file
	dec := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		var sme *toml.StrictMissingError
		if errors.As(err, &sme) {
			return nil, fmt.Errorf("%s", strings.TrimSpace(sme.String()))
		}
		var de *toml.DecodeError
		if errors.As(err, &de) {
			row, col := de.Position()
			return nil, fmt.Errorf("line %d, column %d: %s", row, col, de.Error())
		}
		return nil, err
	}
	if len(f.Entry) > MaxEntries {
		return nil, fmt.Errorf("more than %d entries", MaxEntries)
	}
	seen := map[string]bool{}
	var errs []error
	for i := range f.Entry {
		e := &f.Entry[i]
		e.Source = src
		if err := e.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("entry %q: %w", label(*e, i), err))
		}
		if seen[e.ID] {
			errs = append(errs, fmt.Errorf("entry %q: id listed twice in the file", e.ID))
		}
		seen[e.ID] = true
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return f.Entry, nil
}

func label(e Entry, i int) string {
	if e.ID != "" && len(e.ID) <= 40 && !hasControl(e.ID) {
		return e.ID
	}
	return fmt.Sprintf("#%d", i+1)
}

// ParseFS reads every *.toml of fsys (the embedded built-in library) and
// returns the entries in file-name order. Any error is fatal to the caller:
// a built-in file that does not parse is a bug caught by the tests.
func ParseFS(fsys fs.FS, src Source) ([]Entry, error) {
	names, err := fs.Glob(fsys, "*.toml")
	if err != nil {
		return nil, err
	}
	var out []Entry
	ids := map[string]string{}
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		entries, err := Parse(data, src)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, e := range entries {
			if prev, dup := ids[e.ID]; dup {
				return nil, fmt.Errorf("%s: entry %q is also in %s", name, e.ID, prev)
			}
			ids[e.ID] = name
		}
		out = append(out, entries...)
	}
	return out, nil
}
