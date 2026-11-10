package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWritePrivateTightensExistingModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced on Windows")
	}
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := writePrivate(path, []byte("new")); err != nil {
		t.Fatal(err)
	}

	for p, want := range map[string]os.FileMode{dir: 0700, path: 0600} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s has mode %o, want %o", p, got, want)
		}
	}
}
