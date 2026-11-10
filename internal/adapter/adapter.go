// Package adapter is the boundary between the agent and the runtime it
// watches. Everything the agent knows about a particular runtime (how to find
// it, how to ask whether it is healthy, how to read its version) lives behind
// the Adapter interface, in that runtime's own package. The rest of the agent
// works only with the neutral Report.
package adapter

import "context"

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

// Supports reports whether an adapter lists a capability.
func Supports(a Adapter, capability Capability) bool {
	for _, c := range a.Capabilities() {
		if c == capability {
			return true
		}
	}
	return false
}
