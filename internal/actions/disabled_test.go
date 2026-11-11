//go:build noactions

package actions

import (
	"strings"
	"testing"
	"time"
)

func TestABuildWithoutActionsRefusesEverything(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	gate := NewGate(Known())
	for _, action := range Known() {
		err := gate.Check(Command{ID: "a-" + action, AgentID: "agent-1", Action: action, ExpiresAt: now.Add(time.Minute)}, "agent-1", now)
		if err == nil || !strings.Contains(err.Error(), "not part of this build") {
			t.Errorf("%s: got %v, want a refusal", action, err)
		}
	}
}
