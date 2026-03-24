package heartbeat

import (
	"context"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/openclaw"
)

// Payload is the data sent to the BeaconClaw manager every heartbeat interval.
type Payload struct {
	InstanceID string          `json:"instance_id"`
	Timestamp  time.Time       `json:"timestamp"`
	Seq        uint64          `json:"seq"`
	System     SystemMetrics   `json:"system"`
	OpenClaw   openclaw.Status `json:"openclaw"`
}

// Collector assembles heartbeat payloads from system metrics and OpenClaw probes.
type Collector struct {
	instanceID string
	prober     *openclaw.Prober
	seq        uint64
}

func NewCollector(instanceID string, prober *openclaw.Prober) *Collector {
	return &Collector{
		instanceID: instanceID,
		prober:     prober,
	}
}

// Collect gathers a single heartbeat payload.
func (c *Collector) Collect(ctx context.Context) Payload {
	c.seq++
	return Payload{
		InstanceID: c.instanceID,
		Timestamp:  time.Now().UTC(),
		Seq:        c.seq,
		System:     CollectSystemMetrics(),
		OpenClaw:   c.prober.Probe(ctx),
	}
}
