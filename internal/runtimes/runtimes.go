// Package runtimes picks the adapter for the agent runtime on this machine.
package runtimes

import (
	"github.com/beaconclaw/beaconclaw/internal/adapter"
	"github.com/beaconclaw/beaconclaw/internal/config"
	"github.com/beaconclaw/beaconclaw/internal/hermes"
	"github.com/beaconclaw/beaconclaw/internal/openclaw"
)

// Names of the runtimes this CLI supports, as set in the config's "runtime".
const (
	OpenClaw = openclaw.Name
	Hermes   = hermes.Name
)

// New returns the adapter for the configured runtime. With none configured, it
// uses Hermes when only Hermes is found, and OpenClaw otherwise, which keeps
// every existing machine on OpenClaw.
func New(cfg *config.Config) adapter.Adapter {
	switch Choose(cfg) {
	case Hermes:
		return hermes.NewAdapter(cfg.HermesHome)
	default:
		return openclaw.NewAdapter(cfg.OpenClawPort)
	}
}

// Choose names the runtime New would pick.
func Choose(cfg *config.Config) string {
	switch cfg.Runtime {
	case Hermes, OpenClaw:
		return cfg.Runtime
	}
	if hermes.Detected(cfg.HermesHome) && !openclaw.Detected() {
		return Hermes
	}
	return OpenClaw
}
