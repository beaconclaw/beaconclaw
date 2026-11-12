package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/actions"
	"github.com/beaconclaw/beaconclaw/internal/adapter"
	"github.com/beaconclaw/beaconclaw/internal/heartbeat"
	"github.com/beaconclaw/beaconclaw/internal/ws"
)

// How long after the runtime's restart command returns the gateway is given
// to answer again, how often it is asked, and the longest any action may take
// in all. A restart takes minutes: the old gateway finishes its work and
// stops, and the new one starts.
const (
	restartSettle = 3 * time.Minute
	restartPoll   = 3 * time.Second
	actionTimeout = 12 * time.Minute
)

// actionResult is the agent's answer to an action. Result holds names and
// states only: never the runtime's output, logs or anything it was told.
type actionResult struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Refused bool   `json:"refused,omitempty"`
	// Started is true on the first of two answers to an action that was
	// accepted: the work has begun, and the result follows when it is done.
	Started bool `json:"started,omitempty"`
	Result  any  `json:"result,omitempty"`
}

// allowedActions is what this copy tells the manager is switched on here.
func (a *Agent) allowedActions() []string {
	if !actions.Enabled {
		return nil
	}
	return actions.Normalize(a.cfg.Actions)
}

// handleAction decides whether to do what the manager asked, records it on
// this machine either way, and answers. The work itself runs on the side, so
// a slow restart never holds up heartbeats.
func (a *Agent) handleAction(m ws.Message) {
	expires, _ := time.Parse(time.RFC3339, m.ExpiresAt)
	command := actions.Command{ID: m.ID, AgentID: m.AgentID, Action: m.Action, ExpiresAt: expires}

	if a.gate == nil {
		a.gate = actions.NewGate(a.cfg.Actions)
	}
	if err := a.gate.Check(command, a.managerAgentID, time.Now()); err != nil {
		a.recordAction(command, "refused: "+err.Error())
		a.answer(actionResult{ID: m.ID, Refused: true, Result: map[string]string{"reason": err.Error()}})
		return
	}

	a.recordAction(command, "started")
	// Say at once that it was accepted, so the manager knows to wait for the result.
	a.answer(actionResult{ID: m.ID, Started: true})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		result := a.perform(ctx, command.Action)
		outcome := "failed"
		if result.OK {
			outcome = "succeeded"
		}
		a.recordAction(command, outcome)
		result.ID = m.ID
		a.answer(result)
	}()
}

func (a *Agent) answer(result actionResult) {
	result.Type = "action_result"
	if err := a.client.SendJSON(result); err != nil {
		log.Printf("could not send the result of action %s: %v", result.ID, err)
	}
}

// perform carries out one action. The switch is the whole list: there is no
// way to reach anything else from here.
func (a *Agent) perform(ctx context.Context, action string) actionResult {
	switch action {
	case actions.Diagnostics:
		checks := a.diagnose(ctx)
		ok := true
		for _, check := range checks {
			ok = ok && check.OK
		}
		return actionResult{OK: ok, Result: map[string]any{"checks": checks}}

	case actions.RestartGateway:
		restarter, can := a.runtime.(adapter.Restarter)
		if !can {
			return actionResult{Result: map[string]string{"reason": "this runtime cannot be restarted by the agent"}}
		}
		err := restarter.RestartGateway(ctx)
		if errors.Is(err, exec.ErrNotFound) {
			// Nothing was run, so there is nothing to wait for.
			return actionResult{Result: map[string]string{"reason": "the runtime's command was not found on this machine"}}
		}
		if errors.Is(err, adapter.ErrNotRestarted) {
			// The runtime said no, or the gateway's process never changed: the
			// gateway may still be answering, but it was not restarted.
			return actionResult{Result: map[string]any{
				"restarted": false,
				"reason":    "the runtime did not restart its gateway, for example because work was in progress",
			}}
		}
		// The runtime's command can report an error although the gateway did
		// restart, for instance when its own wait runs out first. What counts
		// is whether the gateway answers again.
		back, waited := a.waitForGateway(ctx)
		return actionResult{OK: back, Result: map[string]any{
			"gateway_answering":  back,
			"restart_command_ok": err == nil,
			"waited_seconds":     int(waited.Seconds()),
		}}

	default:
		return actionResult{Result: map[string]string{"reason": "unknown action"}}
	}
}

// settle and poll are the times used after a restart. Tests shorten them.
func (a *Agent) settle() time.Duration {
	if a.restartSettle > 0 {
		return a.restartSettle
	}
	return restartSettle
}

func (a *Agent) poll() time.Duration {
	if a.restartPoll > 0 {
		return a.restartPoll
	}
	return restartPoll
}

// waitForGateway asks the runtime whether it is answering until it is, or the
// time allowed runs out.
func (a *Agent) waitForGateway(ctx context.Context) (bool, time.Duration) {
	started := time.Now()
	deadline := started.Add(a.settle())
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		report := a.runtime.Probe(probeCtx)
		cancel()
		if report.Healthy {
			return true, time.Since(started)
		}
		if time.Now().After(deadline) {
			return false, time.Since(started)
		}
		select {
		case <-ctx.Done():
			return false, time.Since(started)
		case <-time.After(a.poll()):
		}
	}
}

// diagnose runs the agent's own checks once more: the same ones a heartbeat
// makes, reported as a list a person can read.
func (a *Agent) diagnose(ctx context.Context) []actions.Check {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	report := a.runtime.Probe(probeCtx)
	cancel()
	system := heartbeat.CollectSystemMetrics()

	checks := []actions.Check{
		{Name: "runtime_present", OK: report.Present},
		{Name: "gateway_answering", OK: report.Healthy},
		{Name: "gateway_ready", OK: report.Ready},
	}
	for _, failing := range report.Failing {
		checks = append(checks, actions.Check{Name: "channel", OK: false, Note: failing})
	}
	return append(checks,
		actions.Check{Name: "runtime_version", OK: report.Version != "", Note: report.Version},
		actions.Check{Name: "agent_version", OK: true, Note: Version},
		actions.Check{Name: "memory", OK: system.MemoryPercent < 95, Note: fmt.Sprintf("%.0f%% used", system.MemoryPercent)},
		actions.Check{Name: "disk", OK: system.DiskPercent < 95, Note: fmt.Sprintf("%.0f%% used", system.DiskPercent)},
		actions.Check{Name: "manager_connection", OK: a.client.IsConnected()},
	)
}

// recordAction writes one line to this machine's own record of remote
// actions, and to the agent's log. The file is the machine owner's copy: it
// is never sent anywhere.
func (a *Agent) recordAction(command actions.Command, what string) {
	line := fmt.Sprintf("%s action=%s id=%s %s", time.Now().UTC().Format(time.RFC3339), command.Action, command.ID, what)
	log.Printf("remote action: %s", line)
	if a.actionsLog == "" {
		return
	}
	file, err := os.OpenFile(a.actionsLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintln(file, line)
}
