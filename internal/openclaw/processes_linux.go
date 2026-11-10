package openclaw

import (
	"os"
	"path/filepath"
	"strings"
)

// runningCommands returns the command line of each process that mentions
// OpenClaw's package folder. Other processes' command lines are not kept.
func runningCommands() [][]string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var commands [][]string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name[0] < '0' || name[0] > '9' {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", name, "cmdline"))
		if err != nil || !strings.Contains(string(data), packageMarker) {
			continue
		}
		commands = append(commands, strings.Split(strings.TrimRight(string(data), "\x00"), "\x00"))
	}
	return commands
}
