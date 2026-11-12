package heartbeat

import (
	"context"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

// ProtocolVersion is the version of the wire format this agent speaks.
//
// Version 2 describes the runtime in terms that fit any runtime, under
// "runtime". The version 1 fields ("instance_id" and "openclaw") are still
// sent beside it, so a manager that only knows version 1 keeps working.
const ProtocolVersion = 2

// Runtime is the runtime's health in terms that fit any runtime.
type Runtime struct {
	// Kind names the runtime, such as "openclaw".
	Kind    string `json:"kind"`
	Present bool   `json:"present"`
	Healthy bool   `json:"healthy"`
	Ready   bool   `json:"ready"`
	Version string `json:"version,omitempty"`
	// Failing names the parts of the runtime that are not working.
	Failing  []string `json:"failing"`
	UptimeMs int64    `json:"uptime_ms,omitempty"`
	// Raw is the adapter's own data, unchanged.
	Raw any `json:"raw,omitempty"`
}

// Payload is the data sent to the BeaconClaw manager every heartbeat interval.
type Payload struct {
	AgentID string `json:"agent_id"`
	// InstanceID is the version 1 name for AgentID.
	InstanceID string        `json:"instance_id"`
	Timestamp  time.Time     `json:"timestamp"`
	Seq        uint64        `json:"seq"`
	System     SystemMetrics `json:"system"`
	Runtime    Runtime       `json:"runtime"`
	// Legacy is the adapter's own data under the name version 1 managers read.
	Legacy any `json:"openclaw"`
	// Inventory is what the runtime has loaded. It is gathered far less often
	// than a heartbeat, and is sent only with the heartbeat that follows.
	Inventory *Inventory `json:"inventory,omitempty"`
	// Usage is how much the runtime was used on recent days, as counts. Like
	// the inventory, it is gathered rarely and sent with the next heartbeat.
	Usage *Usage `json:"usage,omitempty"`
	// Automations are the runtime's scheduled jobs and their recent runs.
	// Like the inventory, they are gathered on the side and sent with the next
	// heartbeat. Left out entirely for a runtime that cannot list them.
	Automations *Automations `json:"automations,omitempty"`
	// Report is the neutral reading of the same probe. It is for the agent's
	// own use and is not sent.
	Report adapter.Report `json:"-"`
}

// Inventory is what the runtime has loaded, by name only. A null list means
// the adapter could not find out; an empty one means there is nothing.
type Inventory struct {
	CollectedAt time.Time            `json:"collected_at"`
	Skills      []adapter.Skill      `json:"skills"`
	Connections []adapter.Connection `json:"connections"`
	// Revision is the commit of the runtime's working folder, or null when
	// that folder is not a git checkout.
	Revision *adapter.Revision `json:"revision"`
	// Systems are the systems the runtime holds a credential for, by the name
	// people call them ("Jira"), worked out from the names of credentials its
	// skills declare. A credential's own name is not sent here.
	Systems []string `json:"systems"`
	// UnrecognisedCredentials counts the declared credentials the agent's
	// catalogue could not put a system to. Only the number is sent.
	UnrecognisedCredentials int `json:"unrecognised_credentials"`
	// CredentialNames are the names of every declared credential in place. It
	// is sent only when the machine's owner has switched that on.
	CredentialNames []string `json:"credential_names,omitempty"`
}

// Usage is the runtime's activity on recent days, as counts only.
type Usage struct {
	CollectedAt time.Time          `json:"collected_at"`
	Days        []adapter.UsageDay `json:"days"`
}

// Collector assembles heartbeat payloads from system metrics and the runtime
// adapter's report.
type Collector struct {
	instanceID string
	runtime    adapter.Adapter
	seq        uint64
}

func NewCollector(instanceID string, runtime adapter.Adapter) *Collector {
	return &Collector{
		instanceID: instanceID,
		runtime:    runtime,
	}
}

// Collect gathers a single heartbeat payload.
func (c *Collector) Collect(ctx context.Context) Payload {
	c.seq++
	return c.CollectAs(ctx, c.seq)
}

// CollectAs gathers a heartbeat with a sequence number chosen by the caller,
// for a caller that keeps the numbering itself.
func (c *Collector) CollectAs(ctx context.Context, seq uint64) Payload {
	c.seq = seq
	report := c.runtime.Probe(ctx)
	failing := report.Failing
	if failing == nil {
		failing = []string{}
	}
	return Payload{
		AgentID:    c.instanceID,
		InstanceID: c.instanceID,
		Timestamp:  time.Now().UTC(),
		Seq:        c.seq,
		System:     CollectSystemMetrics(),
		Runtime: Runtime{
			Kind:     c.runtime.Name(),
			Present:  report.Present,
			Healthy:  report.Healthy,
			Ready:    report.Ready,
			Version:  report.Version,
			Failing:  failing,
			UptimeMs: report.UptimeMs,
			Raw:      report.Raw,
		},
		Legacy: report.Raw,
		Report: report,
	}
}
