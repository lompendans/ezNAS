package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsReleaseNewer(t *testing.T) {
	cases := []struct { latest, current string; want bool }{
		{"v1.2.0", "v1.1.9", true},
		{"v1.2.0", "1.2.0", false},
		{"v1.2.0", "v1.2.1", false},
		{"v2.0.0", "v1.99.99", true},
		{"v1.2.0", "dev", true},
		{"v1.2.0", "unknown", true},
	}
	for _, tc := range cases {
		if got := isReleaseNewer(tc.latest, tc.current); got != tc.want {
			t.Errorf("isReleaseNewer(%q, %q) = %v; want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestExpectedChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checksums.txt")
	sum := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(path, []byte(sum+"  eznas-linux-amd64.tar.gz\n"), 0600); err != nil { t.Fatal(err) }
	got, err := expectedChecksum(path, "eznas-linux-amd64.tar.gz")
	if err != nil { t.Fatal(err) }
	if got != sum { t.Fatalf("got %q, want %q", got, sum) }
	if _, err := expectedChecksum(path, "missing.zip"); err == nil { t.Fatal("expected missing checksum to fail") }
}
