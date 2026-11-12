// Package hermes reports on a Hermes Agent runtime (Nous Research) on this
// machine. It reads only what Hermes itself writes for monitoring: the gateway's
// state file, the names of skills on disk, the names of configured platforms,
// tool servers and model providers, and counts from its session store. It never
// opens Hermes's environment file, and it never reads a credential or the text
// of a conversation.
package hermes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

// Name is how this runtime is named in heartbeats.
const Name = "hermes"

// Adapter reports on the Hermes Agent whose home directory is home.
type Adapter struct {
	home string
	// now and alive are replaced in tests.
	now   func() int64
	alive func(pid int32, startTime int64) (bool, int64)
}

var _ adapter.Adapter = (*Adapter)(nil)
var _ adapter.Inventorier = (*Adapter)(nil)
var _ adapter.UsageCounter = (*Adapter)(nil)
var _ adapter.Restarter = (*Adapter)(nil)

// NewAdapter reports on the Hermes home directory given, or, when it is empty,
// on $HERMES_HOME, falling back to ~/.hermes as Hermes itself does.
func NewAdapter(home string) *Adapter {
	return &Adapter{home: ResolveHome(home), now: nowMs, alive: processAlive}
}

// ResolveHome picks Hermes's home directory the way Hermes does.
func ResolveHome(home string) string {
	if home != "" {
		return home
	}
	if env := os.Getenv("HERMES_HOME"); env != "" {
		return env
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return ".hermes"
	}
	return filepath.Join(userHome, ".hermes")
}

// Detected reports whether Hermes looks installed on this machine.
func Detected(home string) bool {
	if _, err := os.Stat(filepath.Join(ResolveHome(home), stateFile)); err == nil {
		return true
	}
	_, err := exec.LookPath("hermes")
	return err == nil
}

func (a *Adapter) Name() string { return Name }

func (a *Adapter) Capabilities() []adapter.Capability {
	return []adapter.Capability{
		adapter.CapHealth,
		adapter.CapReadiness,
		adapter.CapChannels,
		adapter.CapUptime,
		adapter.CapVersion,
		adapter.CapInventory,
		adapter.CapUsage,
	}
}

// Target is the home directory the adapter reads.
func (a *Adapter) Target() string { return a.home }

// Probe reads the gateway's state once. It never fails: a missing or stale
// state is a report.
func (a *Adapter) Probe(ctx context.Context) adapter.Report {
	status := a.readStatus()
	return adapter.Report{
		Present:  status.Present,
		Healthy:  status.Healthy,
		Ready:    status.Ready,
		Version:  status.Version,
		Failing:  status.Failing,
		UptimeMs: status.UptimeMs,
		Raw:      status,
	}
}
