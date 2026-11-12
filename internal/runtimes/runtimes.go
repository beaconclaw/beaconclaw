// Package runtimes picks the adapter for the agent runtime on this machine.
package runtimes

import (
	"fmt"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
	"github.com/beaconclaw/beaconclaw/internal/config"
	"github.com/beaconclaw/beaconclaw/internal/hermes"
	"github.com/beaconclaw/beaconclaw/internal/openclaw"
	"github.com/beaconclaw/beaconclaw/internal/profile"
)

// Names of the runtimes this CLI supports, as set in the config's "runtime".
const (
	OpenClaw = openclaw.Name
	Hermes   = hermes.Name
)

// Choice is the runtime a profile reports on, and how it was decided.
type Choice struct {
	Name string
	// Configured: the choice was set in the config, not found by looking.
	Configured bool
	// Both: OpenClaw and Hermes are both installed and nothing was set. The
	// profile reports on OpenClaw; Hermes is not watched until a second
	// profile is set up for it.
	Both bool
}

// New returns the adapter for the configured runtime.
func New(cfg *config.Config) adapter.Adapter {
	if Choose(cfg).Name == Hermes {
		return hermes.NewAdapter(cfg.HermesHome)
	}
	return openclaw.NewAdapter(cfg.OpenClawPort)
}

// Choose decides which runtime a profile reports on. It looks at what is
// installed, never at what is running: a runtime that happens to be down when
// the CLI starts must be reported as down, not swapped for another.
func Choose(cfg *config.Config) Choice {
	return choose(cfg.Runtime, openclaw.Detected(), hermes.Detected(cfg.HermesHome))
}

func choose(configured string, hasOpenClaw, hasHermes bool) Choice {
	switch configured {
	case OpenClaw, Hermes:
		return Choice{Name: configured, Configured: true}
	}
	switch {
	case hasHermes && !hasOpenClaw:
		return Choice{Name: Hermes}
	case hasHermes && hasOpenClaw:
		return Choice{Name: OpenClaw, Both: true}
	default:
		// OpenClaw, or nothing found yet: OpenClaw, as every earlier version did.
		return Choice{Name: OpenClaw}
	}
}

// BothAdvice says what to do on a machine with both runtimes, for a person.
func BothAdvice() string {
	return fmt.Sprintf("Found both OpenClaw and Hermes on this machine. This copy reports on OpenClaw.\n"+
		"Each runtime is a separate agent in BeaconClaw. To watch Hermes too, register a second agent and run:\n"+
		"  %[1]s auth --runtime openclaw\n"+
		"  beaconclaw --profile hermes auth --token <the Hermes agent's token> --runtime hermes\n"+
		"  beaconclaw --profile hermes install", profile.BinaryName())
}

// Display is how a runtime's name is written for people.
func Display(name string) string {
	switch name {
	case Hermes:
		return "Hermes"
	case OpenClaw:
		return "OpenClaw"
	}
	return name
}
