package openclaw

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// DefaultPort is the port an OpenClaw gateway listens on unless configured.
const DefaultPort = 18789

// configCandidates lists where OpenClaw keeps its config, newest name first.
// The older folder names are from before the project was renamed.
func configCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	candidates := []string{filepath.Join(home, ".openclaw", "openclaw.json")}
	for _, legacy := range []string{".clawdbot", ".moldbot"} {
		candidates = append(candidates, filepath.Join(home, legacy, "openclaw.json"))
		candidates = append(candidates, filepath.Join(home, legacy, "clawdbot.json"))
	}
	return candidates
}

// DetectPort reads the gateway port from OpenClaw's own config. It reads
// nothing else from that file. It returns 0 when no port is configured.
func DetectPort() int {
	for _, path := range configCandidates() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}
		gatewayRaw, ok := raw["gateway"]
		if !ok {
			continue
		}
		var gateway struct {
			Port int `json:"port"`
		}
		if err := json.Unmarshal(gatewayRaw, &gateway); err != nil {
			continue
		}
		if gateway.Port > 0 {
			return gateway.Port
		}
	}
	return 0
}
