package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

// restartTimeout bounds Hermes's own restart command. Without --yes it does not
// interrupt live work, so it returns quickly: it either restarts the gateway or
// refuses because a conversation or job is running.
const restartTimeout = 8 * time.Minute

// restartArgs is everything passed to Hermes. It is fixed: nothing from the
// manager, or from anywhere else, is ever added to it. It deliberately leaves
// out --yes, so a restart never cuts off a conversation in progress; Hermes
// refuses instead, and the gateway is judged by whether it answers.
var restartArgs = []string{"gateway", "restart"}

// RestartGateway runs `hermes gateway restart`, the command an operator runs by
// hand. Its output is discarded: it is not sent anywhere.
func (a *Adapter) RestartGateway(ctx context.Context) error {
	command := hermesCommand()
	if command == "" {
		return fmt.Errorf("the runtime's command was not found: %w", exec.ErrNotFound)
	}
	before := a.gatewayProcess()
	runCtx, cancel := context.WithTimeout(ctx, restartTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, command, restartArgs...)
	cmd.Env = append(os.Environ(), "HERMES_HOME="+a.home)
	runErr := cmd.Run()

	// Hermes restarts by letting the old gateway finish and starting a new
	// process. Only a new process proves it happened: a refusal leaves the old
	// one answering.
	if a.waitForNewProcess(ctx, before) {
		return nil
	}
	if runErr != nil {
		return fmt.Errorf("%w: the runtime's restart command failed: %v", adapter.ErrNotRestarted, runErr)
	}
	return fmt.Errorf("%w: the gateway's process did not change", adapter.ErrNotRestarted)
}

// processWait is how long a new gateway process is waited for after the
// command returns. A Hermes gateway with no work in progress is back in
// seconds. Tests shorten it.
var processWait = 2 * time.Minute

// gatewayProcess identifies the running gateway by process ID and start time.
func (a *Adapter) gatewayProcess() [2]int64 {
	data, err := os.ReadFile(filepath.Join(a.home, stateFile))
	if err != nil {
		return [2]int64{}
	}
	var state GatewayState
	if json.Unmarshal(data, &state) != nil {
		return [2]int64{}
	}
	return [2]int64{int64(state.PID), state.StartTime}
}

// waitForNewProcess reports whether a different gateway process appears.
func (a *Adapter) waitForNewProcess(ctx context.Context, before [2]int64) bool {
	deadline := time.Now().Add(processWait)
	for {
		now := a.gatewayProcess()
		if now != before && now != ([2]int64{}) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}

// hermesCommand finds Hermes's launcher: on the PATH, or where its installer
// puts it.
func hermesCommand() string {
	if path, err := exec.LookPath("hermes"); err == nil {
		return path
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".local", "bin", "hermes")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}
