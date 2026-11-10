package profile

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"default", "", false},
		{"dev", "dev", false},
		{"dev-2", "dev-2", false},
		{"Dev", "", true},
		{"-dev", "", true},
		{"a/b", "", true},
		{"../x", "", true},
		{"linux-amd64", "", true},
	}
	for _, tt := range tests {
		got, err := Normalize(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("Normalize(%q) = %q, %v; want %q, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestFromExecutable(t *testing.T) {
	tests := map[string]string{
		"/home/u/.local/bin/beaconclaw":         "",
		"/home/u/.local/bin/beaconclaw-dev":     "dev",
		"/tmp/beaconclaw-linux-amd64":           "",
		"/tmp/beaconclaw-darwin-arm64":          "",
		`beaconclaw-windows-amd64.exe`:          "",
		"/home/u/.local/bin/beaconclaw-dev.exe": "dev",
		"/usr/bin/bclaw":                        "",
		"/tmp/beaconclaw-BAD":                   "",
	}
	for path, want := range tests {
		if got := FromExecutable(path); got != want {
			t.Errorf("FromExecutable(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestNamesPerProfile(t *testing.T) {
	t.Cleanup(func() { current = "" })
	t.Setenv("HOME", "/home/u")

	if err := Set(""); err != nil {
		t.Fatal(err)
	}
	if got := StateDir(); got != filepath.Join("/home/u", ".beaconclaw") {
		t.Errorf("default StateDir = %q", got)
	}
	if BinaryName() != "beaconclaw" || Suffix() != "" || DisplayName() != "default" {
		t.Errorf("default names wrong: %q %q %q", BinaryName(), Suffix(), DisplayName())
	}

	if err := Set("dev"); err != nil {
		t.Fatal(err)
	}
	if got := StateDir(); !strings.HasSuffix(got, ".beaconclaw-dev") {
		t.Errorf("dev StateDir = %q", got)
	}
	if BinaryName() != "beaconclaw-dev" || DisplayName() != "dev" {
		t.Errorf("dev names wrong: %q %q", BinaryName(), DisplayName())
	}
}

func TestResolveOrder(t *testing.T) {
	t.Cleanup(func() { current = "" })

	t.Setenv(EnvVar, "fromenv")
	if err := Resolve("flag", true, "/x/beaconclaw-exe"); err != nil || Name() != "flag" {
		t.Errorf("flag should win: %q %v", Name(), err)
	}
	if err := Resolve("", false, "/x/beaconclaw-exe"); err != nil || Name() != "fromenv" {
		t.Errorf("env should beat binary name: %q %v", Name(), err)
	}
	t.Setenv(EnvVar, "")
	if err := Resolve("", false, "/x/beaconclaw-exe"); err != nil || Name() != "exe" {
		t.Errorf("binary name should apply: %q %v", Name(), err)
	}
	// An explicit "default" overrides the binary name.
	if err := Resolve("default", true, "/x/beaconclaw-exe"); err != nil || Name() != "" {
		t.Errorf("explicit default should win: %q %v", Name(), err)
	}
}
