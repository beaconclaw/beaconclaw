package openclaw

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

// Name is how this runtime is identified.
const Name = "openclaw"

// Adapter watches a local OpenClaw gateway.
type Adapter struct {
	prober *Prober
	port   int
}

var _ adapter.Adapter = (*Adapter)(nil)
var _ adapter.Inventorier = (*Adapter)(nil)
var _ adapter.UsageCounter = (*Adapter)(nil)

// NewAdapter returns an adapter for the gateway on the given local port.
func NewAdapter(port int) *Adapter {
	return &Adapter{prober: NewProber(port), port: port}
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

func (a *Adapter) Target() string {
	return fmt.Sprintf("http://127.0.0.1:%d", a.port)
}

// Probe checks the gateway. The Status it returns as Raw is the same value the
// agent has always sent.
func (a *Adapter) Probe(ctx context.Context) adapter.Report {
	status := a.prober.Probe(ctx)

	version := status.Version
	if version == "unknown" {
		version = ""
	}

	return adapter.Report{
		Present:  status.Healthy || installed(),
		Healthy:  status.Healthy,
		Ready:    status.Healthy && status.Ready,
		Version:  version,
		Failing:  status.FailingChannels,
		UptimeMs: status.UptimeMs,
		Raw:      status,
	}
}

// installed reports whether OpenClaw exists on this machine even though its
// gateway is not answering: it has a config file, or its command can be found.
// Detected reports whether OpenClaw looks installed on this machine.
func Detected() bool { return installed() }

func installed() bool {
	for _, path := range configCandidates() {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	_, err := exec.LookPath("openclaw")
	return err == nil
}
