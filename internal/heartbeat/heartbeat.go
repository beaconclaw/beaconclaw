package heartbeat

import (
	"context"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

// Payload is the data sent to the BeaconClaw manager every heartbeat interval.
type Payload struct {
	InstanceID string        `json:"instance_id"`
	Timestamp  time.Time     `json:"timestamp"`
	Seq        uint64        `json:"seq"`
	System     SystemMetrics `json:"system"`
	// Runtime is the adapter's own data. It is sent under the name installed
	// managers expect.
	Runtime any `json:"openclaw"`
	// Report is the neutral reading of the same probe. It is for the agent's
	// own use and is not sent.
	Report adapter.Report `json:"-"`
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
	report := c.runtime.Probe(ctx)
	return Payload{
		InstanceID: c.instanceID,
		Timestamp:  time.Now().UTC(),
		Seq:        c.seq,
		System:     CollectSystemMetrics(),
		Runtime:    report.Raw,
		Report:     report,
	}
}
