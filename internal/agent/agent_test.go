package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	return []adapter.Capability{adapter.CapHealth, adapter.CapInventory}
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
					Seq       uint64          `json:"seq"`
					Inventory json.RawMessage `json:"inventory"`
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
	box := outbox.Open(outboxPath, 50)
	client := ws.NewClient(m.url(), "bc_test")
	client.SetTiming(ws.Timing{PingEvery: 50 * time.Millisecond, SilentAfter: 300 * time.Millisecond})
	a := &Agent{
		cfg:       &config.Config{InstanceID: "agent-1", Endpoint: m.url(), Inventory: inventory},
		client:    client,
		collector: heartbeat.NewCollector("agent-1", runtime),
		runtime:   runtime,
		// The first collection comes quickly; the second not within a test.
		inventoryDelay: 60 * time.Millisecond,
		inventoryEvery: time.Hour,
		outbox:         box,
		interval:       40 * time.Millisecond,
		ackTimeout:     200 * time.Millisecond,
		ackCheck:       20 * time.Millisecond,
		sent:           map[uint64]time.Time{},
		attempts:       map[uint64]int{},
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
