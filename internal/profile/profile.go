// Package profile lets several independent copies of the agent run on one
// machine. Each named profile has its own config folder, agent identity,
// service and logs. The default profile keeps the original, unsuffixed names.
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// EnvVar selects a profile when no --profile flag is given.
	EnvVar = "BEACONCLAW_PROFILE"

	baseName = "beaconclaw"
)

var (
	current string

	validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)
	// Release assets are named beaconclaw-<os>-<arch>; a binary run under that
	// name is not a profile.
	platformSuffix = regexp.MustCompile(`^(linux|darwin|windows)-(amd64|arm64)$`)
)

// Normalize validates a profile name. "" and "default" both mean the default
// profile and normalize to "".
func Normalize(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "default" {
		return "", nil
	}
	if !validName.MatchString(name) {
		return "", fmt.Errorf("invalid profile name %q: use lowercase letters, digits and hyphens", name)
	}
	if platformSuffix.MatchString(name) {
		return "", fmt.Errorf("invalid profile name %q: reserved", name)
	}
	return name, nil
}

// Set selects the profile for this process.
func Set(name string) error {
	n, err := Normalize(name)
	if err != nil {
		return err
	}
	current = n
	return nil
}

// Name returns the selected profile, or "" for the default profile.
func Name() string { return current }

// DisplayName returns the selected profile, or "default".
func DisplayName() string {
	if current == "" {
		return "default"
	}
	return current
}

// Suffix returns "-<name>" for a named profile and "" for the default one.
func Suffix() string {
	if current == "" {
		return ""
	}
	return "-" + current
}

// BinaryName returns the name this profile's binary is installed under.
func BinaryName() string { return baseName + Suffix() }

// StateDir returns this profile's config folder: ~/.beaconclaw, or
// ~/.beaconclaw-<name> for a named profile.
func StateDir() string {
	dir := "." + baseName + Suffix()
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", dir)
	}
	return filepath.Join(home, dir)
}

// FromExecutable infers a profile from the binary's file name, so a copy
// installed as beaconclaw-dev acts on the dev profile without a flag.
func FromExecutable(path string) string {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, ".exe")
	rest, ok := strings.CutPrefix(name, baseName+"-")
	if !ok {
		return ""
	}
	n, err := Normalize(rest)
	if err != nil {
		return ""
	}
	return n
}

// Resolve picks the profile for this process: an explicit value (the
// --profile flag) wins, then the environment variable, then the binary name.
func Resolve(explicit string, explicitSet bool, execPath string) error {
	if explicitSet {
		return Set(explicit)
	}
	if v, ok := os.LookupEnv(EnvVar); ok && v != "" {
		return Set(v)
	}
	return Set(FromExecutable(execPath))
}
