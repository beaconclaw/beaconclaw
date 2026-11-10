package openclaw

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// runningCommands returns the command line of each process that mentions
// OpenClaw's package folder. Other processes' command lines are not kept.
func runningCommands() [][]string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "/bin/ps", "-axo", "command=").Output()
	if err != nil {
		return nil
	}
	var commands [][]string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, packageMarker) {
			// ps joins arguments with spaces. Paths with spaces are rare for
			// an npm install; such a gateway falls back to the other lookups.
			commands = append(commands, strings.Fields(line))
		}
	}
	return commands
}
