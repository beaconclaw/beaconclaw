package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
	"github.com/beaconclaw/beaconclaw/internal/config"
	"github.com/beaconclaw/beaconclaw/internal/heartbeat"
)

// signalRuntime is a runtime with signals. The first two collections find two
// approvals waiting; every later one finds three.
type signalRuntime struct {
	fakeRuntime
	mu    sync.Mutex
	calls int
}

func (*signalRuntime) Capabilities() []adapter.Capability {
	return []adapter.Capability{adapter.CapHealth, adapter.CapSignals, adapter.CapAutomations}
}

func (s *signalRuntime) Signals(context.Context) (adapter.SignalReport, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	pending := 2
	if s.calls > 2 {
		pending = 3
	}
	return adapter.SignalReport{
		Approvals: &adapter.ApprovalsSignal{Pending: pending},
		Pairing:   &[]adapter.PairingSignal{},
	}, true
}

func (s *signalRuntime) Automations(context.Context, []byte) (adapter.AutomationReport, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls += 1000
	return adapter.AutomationReport{}, true
}

func (s *signalRuntime) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (m *manager) sentSignals(t *testing.T) []heartbeat.Signals {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []heartbeat.Signals{}
	for _, raw := range m.signals {
		var s heartbeat.Signals
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestSignalsGoOutOnlyWhenTheyChange(t *testing.T) {
	m := newManager(t)
	runtime := &signalRuntime{}
	startWithConfig(t, m, filepath.Join(t.TempDir(), "outbox.json"), runtime, func(cfg *config.Config) {
		cfg.Signals = true
	})

	eventually(t, "several collections and both versions sent", func() bool {
		return runtime.count() >= 5 && len(m.sentSignals(t)) >= 2
	})
	sent := m.sentSignals(t)
	if len(sent) != 2 {
		t.Fatalf("signals sent %d times over %d collections, want 2 (once, then on the change)", len(sent), runtime.count())
	}
	if sent[0].Approvals.Pending != 2 || sent[1].Approvals.Pending != 3 || sent[0].CollectedAt.IsZero() {
		t.Errorf("sent = %+v", sent)
	}
	if sent[0].Pairing == nil || len(*sent[0].Pairing) != 0 {
		t.Errorf("an empty pairing list was not sent as one: %+v", sent[0].Pairing)
	}

	// After a reconnection the newest signals go out again, unchanged.
	_, received := m.snapshot()
	m.set(func(m *manager) { m.hangUpAfter = len(received) + 1 })
	eventually(t, "the signals sent again after reconnecting", func() bool {
		connections, _ := m.snapshot()
		return connections >= 2 && len(m.sentSignals(t)) >= 3
	})
	if again := m.sentSignals(t)[2]; again.Approvals.Pending != 3 {
		t.Errorf("after reconnecting sent %+v", again)
	}
}

func TestSignalsAndAutomationsSwitchedOff(t *testing.T) {
	m := newManager(t)
	runtime := &signalRuntime{}
	startWithConfig(t, m, filepath.Join(t.TempDir(), "outbox.json"), runtime, func(cfg *config.Config) {
		cfg.Signals = false
		cfg.Automations = false
	})
	eventually(t, "several heartbeats", func() bool {
		_, received := m.snapshot()
		return len(received) >= 8
	})
	if calls := runtime.count(); calls != 0 {
		t.Errorf("the runtime was asked %d times with signals and automations off", calls)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.signals) != 0 || len(m.automations) != 0 {
		t.Errorf("sent signals %d times and automations %d times", len(m.signals), len(m.automations))
	}
}

func TestRegistrationNamesOnlyWhatIsReported(t *testing.T) {
	capabilities := func(cfg config.Config) map[string]bool {
		a := &Agent{cfg: &cfg, runtime: &signalRuntime{}}
		out := map[string]bool{}
		for _, c := range a.registration().Runtime.Capabilities {
			out[c] = true
		}
		return out
	}
	on := capabilities(config.Config{Signals: true, Automations: true})
	if !on["signals"] || !on["automations"] {
		t.Errorf("switched on: %v", on)
	}
	off := capabilities(config.Config{})
	if off["signals"] || off["automations"] || !off["health"] {
		t.Errorf("switched off: %v", off)
	}
}
