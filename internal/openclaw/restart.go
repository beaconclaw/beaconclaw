package openclaw

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

var _ adapter.Restarter = (*Adapter)(nil)

// restartTimeout bounds the runtime's own restart command.
const restartTimeout = 90 * time.Second

// restartArgs is everything passed to the runtime's command. It is fixed:
// nothing from the manager, or from anywhere else, is ever added to it.
var restartArgs = []string{"gateway", "restart"}

// RestartGateway runs the runtime's own "gateway restart", the command an
// operator runs by hand. Its output is discarded: it is not sent anywhere.
func (a *Adapter) RestartGateway(ctx context.Context) error {
	var lastErr error = exec.ErrNotFound
	for _, candidate := range commandCandidates() {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		runCtx, cancel := context.WithTimeout(ctx, restartTimeout)
		cmd := exec.CommandContext(runCtx, candidate, restartArgs...)
		cmd.Env = commandEnv()
		err := cmd.Run()
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("the runtime's restart command failed: %w", lastErr)
}
