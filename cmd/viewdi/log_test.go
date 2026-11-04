package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenLogLevels(t *testing.T) {
	tests := []struct {
		name        string
		debug, mock bool
		wantFile    bool
		wantInfo    bool
	}{
		{"default writes warnings only", false, false, true, false},
		{"debug adds the info level", true, false, true, true},
		{"mock writes nothing", false, true, false, false},
		{"mock with debug writes", true, true, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state", "viewdi.log")
			var stderr bytes.Buffer
			l, closeLog := openLog(tt.debug, tt.mock, path, &stderr)
			l.Info("an info line")
			l.Error("an error line", "err", "boom")
			closeLog()
			data, err := os.ReadFile(path)
			if (err == nil) != tt.wantFile {
				t.Fatalf("file: %v, want file %v", err, tt.wantFile)
			}
			s := string(data)
			if tt.wantFile && (!strings.Contains(s, "an error line") || !strings.Contains(s, "version=")) {
				t.Errorf("log lacks the error or the version:\n%s", s)
			}
			if got := strings.Contains(s, "an info line"); got != tt.wantInfo {
				t.Errorf("info line written = %v, want %v", got, tt.wantInfo)
			}
			if fi, err := os.Stat(path); err == nil && fi.Mode().Perm() != 0o600 {
				t.Errorf("mode %v", fi.Mode().Perm())
			}
		})
	}
}
