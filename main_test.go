package main

import (
	"path/filepath"
	"testing"
)

func TestCleanRelative(t *testing.T) {
	tests := []struct {
		input string
		want  string
		bad   bool
	}{
		{"", "", false},
		{".", "", false},
		{"docs/readme.txt", "docs/readme.txt", false},
		{"folder/../readme.txt", "readme.txt", false},
		{"../secret.txt", "", true},
		{"../../etc/passwd", "", true},
		{"/etc/passwd", "", true},
		{"C:\\Windows\\win.ini", "", true},
	}
	for _, tt := range tests {
		got, err := cleanRelative(tt.input)
		if tt.bad {
			if err == nil { t.Errorf("cleanRelative(%q) expected error, got %q", tt.input, got) }
			continue
		}
		if err != nil { t.Errorf("cleanRelative(%q) unexpected error: %v", tt.input, err); continue }
		want := filepath.FromSlash(tt.want)
		if got != want { t.Errorf("cleanRelative(%q) = %q, want %q", tt.input, got, want) }
	}
}
