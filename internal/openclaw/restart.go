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

// restartTimeout bounds the runtime's own restart command. That command
// waits for the old gateway to finish its work and stop, and for the new one
// to come up, which takes minutes on a busy machine.
const restartTimeout = 8 * time.Minute

// restartArgs is everything passed to the runtime's command. It is fixed:
// nothing from the manager, or from anywhere else, is ever added to it.
var restartArgs = []string{"gateway", "restart"}

// RestartGateway runs the runtime's own "gateway restart", the command an
// operator runs by hand. Its output is discarded: it is not sent anywhere.
func (a *Adapter) RestartGateway(ctx context.Context) error {
	return restartOnce(ctx, commandCandidates(), func(ctx context.Context, command string) error {
		cmd := exec.CommandContext(ctx, command, restartArgs...)
		cmd.Env = commandEnv()
		return cmd.Run()
	})
}

// restartOnce runs the restart with the first copy of the runtime's command
// that exists, and with no other. A restart that was started is never started
// again, whether it failed or ran out of time: a second one would cut off the
// gateway that the first is still bringing up.
func restartOnce(ctx context.Context, candidates []string, run func(context.Context, string) error) error {
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		runCtx, cancel := context.WithTimeout(ctx, restartTimeout)
		defer cancel()
		if err := run(runCtx, candidate); err != nil {
			return fmt.Errorf("the runtime's restart command failed: %w", err)
		}
		return nil
	}
	return fmt.Errorf("the runtime's command was not found: %w", exec.ErrNotFound)
}
