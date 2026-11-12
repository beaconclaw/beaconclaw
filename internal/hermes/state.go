package hermes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// stateFile is where the Hermes gateway records its own state. Hermes rewrites
// it at least once a minute while the gateway is alive.
const stateFile = "gateway_state.json"

// staleAfter is how old the state may be before the gateway counts as stuck.
// Hermes uses the same limit for its own "heartbeat stale" warning.
const staleAfter = 120 * time.Second

// Platform states that mean a platform is not doing its job. "disabled" and
// "paused" are a choice, not a fault; "connecting" is normal at start-up.
var failingPlatformStates = map[string]bool{
	"retrying":     true,
	"disconnected": true,
	"fatal":        true,
}

// Gateway states in which the gateway is up.
var upGatewayStates = map[string]bool{"running": true, "degraded": true, "draining": true}

// GatewayState is the part of Hermes's state file this adapter reads.
type GatewayState struct {
	GatewayState string                   `json:"gateway_state"`
	UpdatedAt    string                   `json:"updated_at"`
	PID          int32                    `json:"pid"`
	StartTime    int64                    `json:"start_time"`
	CodeVersion  string                   `json:"code_version"`
	ExitReason   *string                  `json:"exit_reason"`
	Platforms    map[string]PlatformState `json:"platforms"`
}

// PlatformState is one messaging platform's state, as Hermes records it.
type PlatformState struct {
	State          string `json:"state"`
	ErrorCode      string `json:"error_code"`
	NeedsAttention bool   `json:"needs_attention"`
}

// Status is what the adapter concluded, for the status command.
type Status struct {
	Present bool
	Healthy bool
	Ready   bool
	// State is the gateway's own word for its state, or "not found".
	State   string
	Version string
	// Failing names the platforms that are not working, and "gateway" when the
	// state has stopped being refreshed although the process is alive: the
	// "connected but not answering" case.
	Failing  []string
	UptimeMs int64
	// StaleFor is how long ago the state was last refreshed.
	StaleFor time.Duration
}

func nowMs() int64 { return time.Now().UnixMilli() }

// processAlive reports whether pid is running, and for how long in ms.
func processAlive(pid int32, _ int64) (bool, int64) {
	if pid <= 0 {
		return false, 0
	}
	p, err := process.NewProcess(pid)
	if err != nil {
		return false, 0
	}
	if running, err := p.IsRunning(); err != nil || !running {
		return false, 0
	}
	created, err := p.CreateTime()
	if err != nil {
		return true, 0
	}
	return true, nowMs() - created
}

func (a *Adapter) readStatus() Status {
	data, err := os.ReadFile(filepath.Join(a.home, stateFile))
	if err != nil {
		return Status{Present: Detected(a.home), State: "not found"}
	}
	var state GatewayState
	if err := json.Unmarshal(data, &state); err != nil {
		return Status{Present: true, State: "unreadable"}
	}
	return a.judge(state)
}

// judge turns the gateway's recorded state into a report.
func (a *Adapter) judge(state GatewayState) Status {
	status := Status{Present: true, State: state.GatewayState, Version: state.CodeVersion}

	alive, uptime := a.alive(state.PID, state.StartTime)
	if !alive {
		if status.State == "" || upGatewayStates[status.State] {
			status.State = "stopped"
		}
		return status
	}
	status.UptimeMs = uptime

	if updated, err := time.Parse(time.RFC3339Nano, state.UpdatedAt); err == nil {
		status.StaleFor = time.Duration(a.now()-updated.UnixMilli()) * time.Millisecond
	}

	var failing []string
	for name, platform := range state.Platforms {
		if failingPlatformStates[platform.State] || platform.NeedsAttention {
			failing = append(failing, name)
		}
	}
	sort.Strings(failing)
	stale := status.StaleFor > staleAfter
	if stale {
		failing = append([]string{"gateway"}, failing...)
	}
	status.Failing = failing

	status.Healthy = upGatewayStates[state.GatewayState] && !stale
	status.Ready = status.Healthy && state.GatewayState == "running" && len(failing) == 0
	return status
}
