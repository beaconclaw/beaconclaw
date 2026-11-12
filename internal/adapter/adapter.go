// Package adapter is the boundary between the agent and the runtime it
// watches. Everything the agent knows about a particular runtime (how to find
// it, how to ask whether it is healthy, how to read its version) lives behind
// the Adapter interface, in that runtime's own package. The rest of the agent
// works only with the neutral Report.
package adapter

import (
	"context"
	"errors"
)

// Capability names one thing an adapter can report. An adapter lists the ones
// it supports, so a missing value can be told apart from an unsupported one.
type Capability string

const (
	// CapHealth: the adapter can tell whether the runtime is responding.
	CapHealth Capability = "health"
	// CapReadiness: the adapter can tell whether the runtime is fully working.
	CapReadiness Capability = "readiness"
	// CapChannels: the adapter can name the parts of the runtime that are failing.
	CapChannels Capability = "channels"
	// CapUptime: the adapter can report how long the runtime has been up.
	CapUptime Capability = "uptime"
	// CapVersion: the adapter can report the runtime's version.
	CapVersion Capability = "version"
	// CapInventory: the adapter can list what the runtime has loaded. Such an
	// adapter also implements Inventorier.
	CapInventory Capability = "inventory"
	// CapUsage: the adapter can count how much the runtime was used each day.
	// Such an adapter also implements UsageCounter.
	CapUsage Capability = "usage"
)

// Report is what an adapter found, in terms that apply to any runtime.
type Report struct {
	// Present: the runtime exists on this machine, whether or not it is running.
	Present bool
	// Healthy: the runtime is responding.
	Healthy bool
	// Ready: the runtime is responding and fully working. An adapter without
	// CapReadiness reports the same value as Healthy.
	Ready bool
	// Version is the runtime's version, or "" when it is not known.
	Version string
	// Failing names the parts of the runtime that are not working, for an
	// adapter with CapChannels.
	Failing []string
	// UptimeMs is how long the runtime has been up, for an adapter with
	// CapUptime. Zero when it is not known.
	UptimeMs int64
	// Raw is the adapter's own data, passed through unchanged. Its shape
	// belongs to the adapter.
	Raw any
}

// Adapter watches one kind of runtime on this machine.
type Adapter interface {
	// Name identifies the runtime, in lowercase, such as "openclaw".
	Name() string
	// Capabilities lists what this adapter can report.
	Capabilities() []Capability
	// Target says where the adapter looks, for logs and the status command.
	Target() string
	// Probe checks the runtime once. It must return within a few seconds and
	// must never fail: a runtime that cannot be reached is a Report, not an error.
	Probe(ctx context.Context) Report
}

// Inventory is what a runtime has loaded, by name only. A nil list means the
// adapter could not find out; an empty one means there is nothing.
type Inventory struct {
	// Skills are the things the runtime can currently do.
	Skills []Skill
	// Connections are the outside systems the runtime is set up to reach.
	Connections []Connection
	// Revision is the commit of the runtime's working folder, when that folder
	// is a git checkout. Nil otherwise.
	Revision *Revision
	// Credentials are the names of the credentials the runtime's own skills
	// declare and the runtime says are in place. Names only, as the runtime
	// reports them: an adapter never opens an environment file or a secrets
	// store to find them. They stay on the machine; what is sent is worked
	// out from them by the agent.
	Credentials []string
}

// Skill names one thing a runtime can do.
type Skill struct {
	Name string `json:"name"`
	// Source says where the skill comes from, in the runtime's own words, such
	// as whether it ships with the runtime or was added on this machine.
	Source string `json:"source,omitempty"`
}

// Connection names one outside system a runtime is set up to reach.
type Connection struct {
	// Kind groups connections, such as "channel" for a chat service.
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Revision identifies the contents of the runtime's working folder.
type Revision struct {
	Commit string `json:"commit"`
	// Dirty: the folder has changes that are not in that commit.
	Dirty bool `json:"dirty"`
}

// Inventorier is implemented by an adapter with CapInventory. Listing may take
// much longer than a probe, so it is called rarely and never on the heartbeat
// path. Like Probe, it must never fail.
type Inventorier interface {
	Inventory(ctx context.Context) Inventory
}

// UsageDay is how much a runtime was used on one UTC day, as counts only.
type UsageDay struct {
	// Date is the UTC day, as YYYY-MM-DD.
	Date string `json:"date"`
	// Final: the day is over, so these figures will not grow.
	Final bool `json:"final"`
	// Sessions is how many conversations had activity that day.
	Sessions          int64 `json:"sessions"`
	UserMessages      int64 `json:"user_messages"`
	AssistantMessages int64 `json:"assistant_messages"`
	ToolCalls         int64 `json:"tool_calls"`
	Tokens            int64 `json:"tokens"`
	// Cost is what the runtime says the day's model use cost, in US dollars.
	Cost float64 `json:"cost"`
	// Channels splits the day by where the conversations came from, such as a
	// chat service or a schedule.
	Channels []UsageChannel `json:"channels"`
	// Truncated: the runtime returned as many conversations as were asked
	// for, so the per-channel split may be missing some.
	Truncated bool `json:"truncated,omitempty"`
}

// UsageChannel is one day's use from one source.
type UsageChannel struct {
	Channel           string `json:"channel"`
	Sessions          int64  `json:"sessions"`
	UserMessages      int64  `json:"user_messages"`
	AssistantMessages int64  `json:"assistant_messages"`
	ToolCalls         int64  `json:"tool_calls"`
}

// UsageCounter is implemented by an adapter with CapUsage. Counting may take
// a minute or more, so it is called rarely and never on the heartbeat path.
// It returns the days it could count; a day it could not is left out, never
// reported as zero.
type UsageCounter interface {
	Usage(ctx context.Context, days []string, today string) []UsageDay
}

// Supports reports whether an adapter lists a capability.
func Supports(a Adapter, capability Capability) bool {
	for _, c := range a.Capabilities() {
		if c == capability {
			return true
		}
	}
	return false
}

// Restarter is implemented by an adapter that can restart its runtime's
// gateway the way an operator would by hand. It is used only for the remote
// action of that name, which is off unless the machine's owner switches it on.
// ErrNotRestarted is returned by a Restarter that can tell the gateway did not
// restart, for instance because the runtime refused while work was in progress.
// The gateway may well still be answering, so this must not count as success.
var ErrNotRestarted = errors.New("the gateway did not restart")

type Restarter interface {
	// RestartGateway asks the runtime to restart its gateway and returns when
	// the runtime's own command has finished. It takes no arguments: what is
	// run is fixed.
	RestartGateway(ctx context.Context) error
}
