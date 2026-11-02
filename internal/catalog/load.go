package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/blokxz/viewDI/internal/config"
)

// maxUserFiles bounds how many files of the user's library directory are read.
const maxUserFiles = 100

// Load builds the catalog: the built-in entries from builtin (a flat
// directory of *.toml; a file that does not parse is a bug caught by the
// tests and is reported as a warning) and then the user's entries from
// userDir, whose IDs replace built-in ones. An invalid or oversized user file
// is skipped whole with a warning; a missing userDir is not an error. It
// reads files only: no network, no daemon.
func Load(builtin fs.FS, userDir string) (*Catalog, []config.Warning) {
	var warns []config.Warning
	entries, err := ParseFS(builtin, Builtin)
	if err != nil {
		warns = append(warns, config.Warning{Source: "built-in library", Msg: err.Error()})
	}
	if userDir != "" {
		user, w := loadUser(userDir)
		entries = append(entries, user...)
		warns = append(warns, w...)
	}
	if len(entries) > MaxEntries {
		warns = append(warns, config.Warning{Source: "library", Msg: fmt.Sprintf("more than %d entries: the rest are ignored", MaxEntries)})
		entries = entries[:MaxEntries]
	}
	return New(entries...), warns
}

func loadUser(dir string) ([]Entry, []config.Warning) {
	des, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []config.Warning{{Source: dir, Msg: err.Error()}}
	}
	slices.SortFunc(des, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	var out []Entry
	var warns []config.Warning
	files := 0
	for _, de := range des {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".toml") {
			continue
		}
		if files++; files > maxUserFiles {
			warns = append(warns, config.Warning{Source: dir, Msg: fmt.Sprintf("more than %d library files: the rest are ignored", maxUserFiles)})
			break
		}
		path := filepath.Join(dir, de.Name())
		data, err := config.ReadCapped(path, MaxFileSize)
		if err != nil {
			warns = append(warns, config.Warning{Source: path, Msg: fmt.Sprintf("library file skipped: %v", err)})
			continue
		}
		entries, err := Parse(data, User)
		if err != nil {
			warns = append(warns, config.Warning{Source: path, Msg: fmt.Sprintf("library file skipped: %v", err)})
			continue
		}
		out = append(out, entries...)
	}
	return out, warns
}
