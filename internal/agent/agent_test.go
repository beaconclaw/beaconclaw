package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
	"github.com/beaconclaw/beaconclaw/internal/config"
	"github.com/beaconclaw/beaconclaw/internal/heartbeat"
	"github.com/beaconclaw/beaconclaw/internal/outbox"
	"github.com/beaconclaw/beaconclaw/internal/ws"
)

type fakeRuntime struct{}

// stockedRuntime is a runtime that can also say what it has loaded.
type stockedRuntime struct {
	fakeRuntime
	asked *atomic.Int32
}

func (stockedRuntime) Capabilities() []adapter.Capability {
	return []adapter.Capability{adapter.CapHealth, adapter.CapInventory, adapter.CapUsage}
}

func (s stockedRuntime) Usage(_ context.Context, days []string, today string) []adapter.UsageDay {
	counted := []adapter.UsageDay{}
	for _, day := range days {
		counted = append(counted, adapter.UsageDay{Date: day, Final: day < today, Sessions: 3, UserMessages: 7})
	}
	return counted
}

func (s stockedRuntime) Inventory(context.Context) adapter.Inventory {
	s.asked.Add(1)
	return adapter.Inventory{
		Skills:      []adapter.Skill{{Name: "summarise", Source: "bundled"}},
		Connections: []adapter.Connection{{Kind: "channel", Name: "chat"}},
		Revision:    &adapter.Revision{Commit: strings.Repeat("a", 40), Dirty: true},
	}
}

func (fakeRuntime) Name() string { return "fake" }
func (fakeRuntime) Capabilities() []adapter.Capability {
	return []adapter.Capability{adapter.CapHealth}
}
func (fakeRuntime) Target() string { return "nowhere" }
func (fakeRuntime) Probe(context.Context) adapter.Report {
	return adapter.Report{Present: true, Healthy: true, Ready: true, Raw: map[string]any{"status": "online"}}
}

// manager is a stand-in for the BeaconClaw manager. Its behaviour can be
// changed while a test runs.
type manager struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	down        bool // refuse connections
	silent      bool // accept heartbeats but never acknowledge them
	hangUpAfter int  // close the connection after this many heartbeats, once
	connections int
	received    []uint64 // heartbeat sequence numbers, in arrival order
	inventories []string // the inventory of each heartbeat that carried one
	usages      []string // the usage of each heartbeat that carried it
	automations []string // the automations of each heartbeat that carried them
	signals     []string // the signals of each heartbeat that carried them
}

func newManager(t *testing.T) *manager {
	m := &manager{t: t}
	upgrader := websocket.Upgrader{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		down := m.down
		m.mu.Unlock()
		if down {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		m.mu.Lock()
		m.connections++
		m.mu.Unlock()

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg struct {
				Type    string `json:"type"`
				Payload struct {
					Seq         uint64          `json:"seq"`
					Inventory   json.RawMessage `json:"inventory"`
					Usage       json.RawMessage `json:"usage"`
					Automations json.RawMessage `json:"automations"`
					Signals     json.RawMessage `json:"signals"`
				} `json:"payload"`
			}
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			switch msg.Type {
			case "register":
				conn.WriteJSON(map[string]any{"type": "register_ack", "protocol": 2})
			case "heartbeat":
				m.mu.Lock()
				m.received = append(m.received, msg.Payload.Seq)
				if len(msg.Payload.Inventory) > 0 {
					m.inventories = append(m.inventories, string(msg.Payload.Inventory))
				}
				if len(msg.Payload.Usage) > 0 {
					m.usages = append(m.usages, string(msg.Payload.Usage))
				}
				if len(msg.Payload.Automations) > 0 {
					m.automations = append(m.automations, string(msg.Payload.Automations))
				}
				if len(msg.Payload.Signals) > 0 {
					m.signals = append(m.signals, string(msg.Payload.Signals))
				}
				silent := m.silent
				hangUp := m.hangUpAfter > 0 && len(m.received) >= m.hangUpAfter
				if hangUp {
					m.hangUpAfter = 0
				}
				m.mu.Unlock()
				if hangUp {
					return
				}
				if !silent {
					conn.WriteJSON(map[string]any{"type": "heartbeat_ack", "seq": msg.Payload.Seq})
				}
			}
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *manager) set(change func(*manager)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	change(m)
}

func (m *manager) snapshot() (connections int, received []uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connections, append([]uint64(nil), m.received...)
}

func (m *manager) url() string { return "ws" + strings.TrimPrefix(m.srv.URL, "http") }

// start runs an agent against the manager with short times, and returns its
// outbox and a function that stops it.
func start(t *testing.T, m *manager, outboxPath string) (*outbox.Outbox, func()) {
	t.Helper()
	return startWith(t, m, outboxPath, fakeRuntime{}, true)
}

func startWith(t *testing.T, m *manager, outboxPath string, runtime adapter.Adapter, inventory bool) (*outbox.Outbox, func()) {
	t.Helper()
	return startWithConfig(t, m, outboxPath, runtime, func(cfg *config.Config) {
		cfg.Inventory, cfg.Usage, cfg.Automations, cfg.Signals = inventory, inventory, inventory, inventory
	})
}

// startWithConfig is startWith with the reporting switches set by change.
func startWithConfig(t *testing.T, m *manager, outboxPath string, runtime adapter.Adapter, change func(*config.Config)) (*outbox.Outbox, func()) {
	t.Helper()
	cfg := &config.Config{InstanceID: "agent-1", Endpoint: m.url()}
	change(cfg)
	cursorPath := filepath.Join(filepath.Dir(outboxPath), "automations.json")
	box := outbox.Open(outboxPath, 50)
	client := ws.NewClient(m.url(), "bc_test")
	client.SetTiming(ws.Timing{PingEvery: 50 * time.Millisecond, SilentAfter: 300 * time.Millisecond})
	a := &Agent{
		cfg:       cfg,
		client:    client,
		collector: heartbeat.NewCollector("agent-1", runtime),
		runtime:   runtime,
		// The first collection comes quickly; the second not within a test.
		inventoryDelay: 60 * time.Millisecond,
		inventoryEvery: time.Hour,
		usageDelay:     80 * time.Millisecond,
		usageEvery:     time.Hour,
		// Scheduled jobs are listed a few times within a test.
		automationsDelay:  50 * time.Millisecond,
		automationsEvery:  120 * time.Millisecond,
		automationsCursor: cursorPath,
		// Signals are collected a few times within a test.
		signalsDelay: 50 * time.Millisecond,
		signalsEvery: 100 * time.Millisecond,
		outbox:       box,
		interval:     40 * time.Millisecond,
		ackTimeout:   200 * time.Millisecond,
		ackCheck:     20 * time.Millisecond,
		sent:         map[uint64]time.Time{},
		attempts:     map[uint64]int{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(done)
	}()
	stop := func() {
		cancel()
		<-done
	}
	t.Cleanup(stop)
	return box, stop
}

func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func inOrderWithoutGaps(seqs []uint64) bool {
	seen := map[uint64]bool{}
	var last uint64
	for _, s := range seqs {
		if seen[s] {
			continue // a resend of one already received
		}
		if s != last+1 {
			return false
		}
		seen[s] = true
		last = s
	}
	return true
}

func TestHeartbeatsLeaveTheOutboxOnlyWhenAcknowledged(t *testing.T) {
	m := newManager(t)
	box, stop := start(t, m, filepath.Join(t.TempDir(), "outbox.json"))

	eventually(t, "three heartbeats received", func() bool {
		_, received := m.snapshot()
		return len(received) >= 3
	})
	stop()

	if waiting := box.Len(); waiting > 1 {
		t.Errorf("%d heartbeats still waiting although the manager acknowledged them", waiting)
	}
}

func TestCollectionCarriesOnThroughAnOutage(t *testing.T) {
	m := newManager(t)
	m.set(func(m *manager) { m.down = true })
	box, _ := start(t, m, filepath.Join(t.TempDir(), "outbox.json"))

	// While the manager cannot be reached, heartbeats keep being collected.
	eventually(t, "five heartbeats collected during the outage", func() bool { return box.Len() >= 5 })

	m.set(func(m *manager) { m.down = false })

	eventually(t, "every outage heartbeat delivered", func() bool {
		_, received := m.snapshot()
		return len(received) >= 5
	})
	_, received := m.snapshot()
	if received[0] != 1 || !inOrderWithoutGaps(received) {
		t.Errorf("heartbeats from the outage arrived out of order or with gaps: %v", received)
	}
}

func TestAClosedConnectionIsNoticedAndTheHeartbeatResent(t *testing.T) {
	m := newManager(t)
	// The manager hangs up on the second heartbeat without acknowledging it.
	m.set(func(m *manager) { m.hangUpAfter = 2 })
	start(t, m, filepath.Join(t.TempDir(), "outbox.json"))

	eventually(t, "a second connection and heartbeat 2 received twice", func() bool {
		connections, received := m.snapshot()
		twos := 0
		for _, s := range received {
			if s == 2 {
				twos++
			}
		}
		return connections >= 2 && twos >= 2
	})
	_, received := m.snapshot()
	if !inOrderWithoutGaps(received) {
		t.Errorf("a heartbeat was lost across the reconnect: %v", received)
	}
}

func TestAManagerThatStopsAcknowledgingIsReconnectedTo(t *testing.T) {
	m := newManager(t)
	m.set(func(m *manager) { m.silent = true })
	box, _ := start(t, m, filepath.Join(t.TempDir(), "outbox.json"))

	eventually(t, "a reconnect after no acknowledgement", func() bool {
		connections, _ := m.snapshot()
		return connections >= 2
	})

	// A heartbeat that is never acknowledged is given up on in the end, so it
	// cannot hold the rest back for ever.
	eventually(t, "heartbeat 1 given up on", func() bool {
		for _, e := range box.Pending() {
			if e.Seq == 1 {
				return false
			}
		}
		return true
	})

	m.set(func(m *manager) { m.silent = false })
	eventually(t, "deliveries resume once the manager acknowledges again", func() bool { return box.Len() <= 1 })
}

func TestHeartbeatsFromBeforeARestartAreSent(t *testing.T) {
	m := newManager(t)
	path := filepath.Join(t.TempDir(), "outbox.json")

	// A previous run left two heartbeats unsent.
	earlier := outbox.Open(path, 50)
	for i := 0; i < 2; i++ {
		seq := earlier.NextSeq()
		earlier.Add(seq, []byte(`{"seq":`+string(rune('0'+seq))+`}`))
	}

	start(t, m, path)

	eventually(t, "the earlier heartbeats and a new one delivered", func() bool {
		_, received := m.snapshot()
		return len(received) >= 3
	})
	_, received := m.snapshot()
	if received[0] != 1 || received[1] != 2 || received[2] != 3 {
		t.Errorf("expected the earlier heartbeats first, then numbering to continue: %v", received)
	}
}

func TestTheInventoryGoesOutOnceWithTheNextHeartbeat(t *testing.T) {
	m := newManager(t)
	asked := &atomic.Int32{}
	startWith(t, m, filepath.Join(t.TempDir(), "outbox.json"), stockedRuntime{asked: asked}, true)

	eventually(t, "an inventory to arrive", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.inventories) == 1
	})
	// Several more heartbeats pass, and none repeats it.
	_, before := m.snapshot()
	eventually(t, "more heartbeats", func() bool {
		_, now := m.snapshot()
		return len(now) >= len(before)+4
	})

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.inventories) != 1 {
		t.Fatalf("the inventory was sent %d times, want once per collection", len(m.inventories))
	}
	var got heartbeat.Inventory
	if err := json.Unmarshal([]byte(m.inventories[0]), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "summarise" || len(got.Connections) != 1 || got.Revision == nil || !got.Revision.Dirty {
		t.Fatalf("unexpected inventory: %s", m.inventories[0])
	}
	if got.CollectedAt.IsZero() {
		t.Fatal("the inventory does not say when it was collected")
	}
}

func TestNoInventoryIsCollectedOrSentWhenSwitchedOff(t *testing.T) {
	m := newManager(t)
	asked := &atomic.Int32{}
	startWith(t, m, filepath.Join(t.TempDir(), "outbox.json"), stockedRuntime{asked: asked}, false)

	eventually(t, "several heartbeats", func() bool {
		_, received := m.snapshot()
		return len(received) >= 6
	})

	m.mu.Lock()
	defer m.mu.Unlock()
	if asked.Load() != 0 || len(m.inventories) != 0 {
		t.Fatalf("asked the runtime %d times and sent %d inventories, want none", asked.Load(), len(m.inventories))
	}
}

func TestARuntimeThatCannotListItsInventoryIsNeverAsked(t *testing.T) {
	m := newManager(t)
	start(t, m, filepath.Join(t.TempDir(), "outbox.json"))

	eventually(t, "several heartbeats", func() bool {
		_, received := m.snapshot()
		return len(received) >= 4
	})

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.inventories) != 0 {
		t.Fatalf("sent %d inventories, want none", len(m.inventories))
	}
}

func TestUsageGoesOutOnceWithTodayAndAFinalYesterday(t *testing.T) {
	m := newManager(t)
	startWith(t, m, filepath.Join(t.TempDir(), "outbox.json"), stockedRuntime{asked: &atomic.Int32{}}, true)

	eventually(t, "usage to arrive", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.usages) == 1
	})
	_, before := m.snapshot()
	eventually(t, "more heartbeats", func() bool {
		_, now := m.snapshot()
		return len(now) >= len(before)+4
	})

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.usages) != 1 {
		t.Fatalf("usage was sent %d times, want once per count", len(m.usages))
	}
	var got heartbeat.Usage
	if err := json.Unmarshal([]byte(m.usages[0]), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Days) != 2 || !got.Days[0].Final || got.Days[1].Final || got.Days[0].Date >= got.Days[1].Date {
		t.Fatalf("want yesterday (final) then today (not final), got %s", m.usages[0])
	}
	if got.Days[1].Sessions != 3 || got.Days[1].UserMessages != 7 || got.CollectedAt.IsZero() {
		t.Fatalf("unexpected usage: %s", m.usages[0])
	}
}

func TestNoUsageIsCountedOrSentWhenSwitchedOff(t *testing.T) {
	m := newManager(t)
	startWith(t, m, filepath.Join(t.TempDir(), "outbox.json"), stockedRuntime{asked: &atomic.Int32{}}, false)

	eventually(t, "several heartbeats", func() bool {
		_, received := m.snapshot()
		return len(received) >= 6
	})

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.usages) != 0 {
		t.Fatalf("sent usage %d times, want none", len(m.usages))
	}
}

// scheduledRuntime is a runtime with scheduled jobs. Each listing finds one
// more finished run than the last, and records the cursor it was given.
type scheduledRuntime struct {
	fakeRuntime
	mu      sync.Mutex
	calls   int
	cursors []string
}

func (*scheduledRuntime) Capabilities() []adapter.Capability {
	return []adapter.Capability{adapter.CapHealth, adapter.CapAutomations}
}

func (s *scheduledRuntime) Automations(_ context.Context, cursor []byte) (adapter.AutomationReport, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.cursors = append(s.cursors, string(cursor))
	started := time.Date(2026, 1, 1, 0, s.calls, 0, 0, time.UTC)
	return adapter.AutomationReport{
		Jobs:   []adapter.AutomationJob{{ID: "job-1", Name: "Nightly tidy", Enabled: true, Schedule: adapter.AutomationSchedule{Kind: adapter.ScheduleEvery, EveryMs: 60000}}},
		States: []adapter.AutomationState{{ID: "job-1", LastRunAt: &started, LastStatus: adapter.Ptr(adapter.RunOK)}},
		Runs:   []adapter.AutomationRun{{ID: fmt.Sprintf("job-1:%d", s.calls), JobID: "job-1", StartedAt: started, Status: adapter.RunOK}},
		Cursor: []byte(fmt.Sprintf(`{"seen":%d}`, s.calls)),
	}, true
}

func TestAutomationsGoOutWithJobsOnlyWhenTheyChange(t *testing.T) {
	m := newManager(t)
	dir := t.TempDir()
	runtime := &scheduledRuntime{}
	_, stop := startWith(t, m, filepath.Join(dir, "outbox.json"), runtime, true)

	eventually(t, "three automation reports", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.automations) >= 3
	})
	stop()

	m.mu.Lock()
	defer m.mu.Unlock()
	seenRuns := map[string]bool{}
	for i, raw := range m.automations {
		var got heartbeat.Automations
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatal(err)
		}
		if (got.Jobs != nil) != (i == 0) {
			t.Errorf("report %d: jobs present = %v, want only in the first: %s", i, got.Jobs != nil, raw)
		}
		if got.DefinitionsHash == "" || len(got.States) != 1 || got.CollectedAt.IsZero() {
			t.Errorf("report %d = %s", i, raw)
		}
		for _, run := range got.Runs {
			if seenRuns[run.ID] {
				t.Errorf("run %s sent twice", run.ID)
			}
			seenRuns[run.ID] = true
		}
	}

	// Each listing is handed the cursor of the one before, and the last is
	// kept on disk for the next start.
	runtime.mu.Lock()
	cursors := append([]string(nil), runtime.cursors...)
	runtime.mu.Unlock()
	if cursors[0] != "" || cursors[1] != `{"seen":1}` {
		t.Errorf("cursors = %v", cursors)
	}
	data, err := os.ReadFile(filepath.Join(dir, "automations.json"))
	if err != nil || !strings.Contains(string(data), `"runtime":"fake"`) || !strings.Contains(string(data), `"seen":`) {
		t.Fatalf("saved cursor = %s, %v", data, err)
	}

	restarted := &scheduledRuntime{}
	m2 := newManager(t)
	_, stop2 := startWith(t, m2, filepath.Join(dir, "outbox.json"), restarted, true)
	eventually(t, "a listing after the restart", func() bool {
		restarted.mu.Lock()
		defer restarted.mu.Unlock()
		return restarted.calls >= 1
	})
	stop2()
	if !strings.Contains(restarted.cursors[0], `"seen":`) {
		t.Errorf("the saved cursor was not handed back after a restart: %q", restarted.cursors[0])
	}
}

func TestARuntimeWithoutAutomationsSendsNone(t *testing.T) {
	m := newManager(t)
	startWith(t, m, filepath.Join(t.TempDir(), "outbox.json"), stockedRuntime{asked: &atomic.Int32{}}, true)

	eventually(t, "several heartbeats", func() bool {
		_, received := m.snapshot()
		return len(received) >= 6
	})

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.automations) != 0 {
		t.Fatalf("sent automations %d times, want none", len(m.automations))
	}
}
