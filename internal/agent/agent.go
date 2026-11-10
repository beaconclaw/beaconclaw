package agent

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
	"github.com/beaconclaw/beaconclaw/internal/config"
	"github.com/beaconclaw/beaconclaw/internal/heartbeat"
	"github.com/beaconclaw/beaconclaw/internal/openclaw"
	"github.com/beaconclaw/beaconclaw/internal/outbox"
	"github.com/beaconclaw/beaconclaw/internal/ws"
)

const (
	// How long the manager has to acknowledge a heartbeat before the
	// connection is treated as broken.
	defaultAckTimeout = 20 * time.Second
	// How often unacknowledged heartbeats are checked.
	defaultAckCheck = 5 * time.Second
	// A heartbeat the manager never acknowledges is given up on after this
	// many sends, so one bad heartbeat cannot hold up the rest for ever.
	maxSendAttempts = 3
	// How often the runtime is asked what it has loaded, and how soon after
	// starting it is first asked.
	defaultInventoryEvery = 15 * time.Minute
	defaultInventoryDelay = 20 * time.Second
)

// Agent is the main daemon orchestrator.
//
// It collects a heartbeat on every tick whether or not the manager can be
// reached, and keeps each one in the outbox until the manager acknowledges
// it. Connecting happens on the side, so an outage never stops collection.
type Agent struct {
	cfg       *config.Config
	client    *ws.Client
	collector *heartbeat.Collector
	runtime   adapter.Adapter
	outbox    *outbox.Outbox

	interval       time.Duration
	ackTimeout     time.Duration
	ackCheck       time.Duration
	inventoryEvery time.Duration
	inventoryDelay time.Duration

	// The fields below belong to the Run loop alone.
	connecting bool
	// sent holds when each unacknowledged heartbeat was last sent on the
	// current connection.
	sent map[uint64]time.Time
	// attempts counts how many times each heartbeat has been sent in all.
	attempts map[uint64]int
	// managerProtocol is the protocol version the manager said it understands.
	managerProtocol int
	// inventory is waiting to go out with the next heartbeat.
	inventory *heartbeat.Inventory
}

func New(cfg *config.Config) *Agent {
	runtime := openclaw.NewAdapter(cfg.OpenClawPort)
	return &Agent{
		cfg:            cfg,
		client:         ws.NewClient(cfg.Endpoint, cfg.Token),
		collector:      heartbeat.NewCollector(cfg.InstanceID, runtime),
		runtime:        runtime,
		outbox:         outbox.Open(config.OutboxPath(), cfg.BufferCapacity),
		interval:       time.Duration(cfg.HeartbeatInterval) * time.Second,
		ackTimeout:     defaultAckTimeout,
		ackCheck:       defaultAckCheck,
		inventoryEvery: defaultInventoryEvery,
		inventoryDelay: defaultInventoryDelay,
		sent:           map[uint64]time.Time{},
		attempts:       map[uint64]int{},
	}
}

// Run starts the agent and blocks until the context is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	log.Printf("beaconclaw agent starting (agent=%s)", a.cfg.InstanceID)
	log.Printf("manager endpoint: %s", a.cfg.Endpoint)
	log.Printf("%s gateway: %s", a.runtime.Name(), a.runtime.Target())
	if waiting := a.outbox.Len(); waiting > 0 {
		log.Printf("%d heartbeats from before this start are waiting to be sent", waiting)
	}

	connected := make(chan struct{}, 1)
	a.connect(ctx, connected)

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	ackTicker := time.NewTicker(a.ackCheck)
	defer ackTicker.Stop()
	inventories := a.watchInventory(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("shutting down...")
			a.client.Close()
			return nil

		case <-ticker.C:
			a.collect(ctx)
			a.flush(ctx, connected)

		case inventory := <-inventories:
			a.inventory = &inventory
			log.Printf("inventory collected (%d skills, %d connections)", len(inventory.Skills), len(inventory.Connections))

		case <-connected:
			a.connecting = false
			a.flush(ctx, connected)

		case m := <-a.client.Messages():
			a.handle(m)

		case <-a.client.Lost():
			log.Println("connection to manager lost")
			a.reconnect(ctx, connected)

		case <-ackTicker.C:
			if seq, late := a.overdue(); late {
				log.Printf("no acknowledgement for heartbeat seq=%d after %s; reconnecting", seq, a.ackTimeout)
				a.client.Close()
				a.reconnect(ctx, connected)
			}
		}
	}
}

// collect takes one heartbeat and puts it in the outbox. It never waits on
// the network.
func (a *Agent) collect(ctx context.Context) {
	payload := a.collector.CollectAs(ctx, a.outbox.NextSeq())
	// The inventory rides on one heartbeat. If that heartbeat is lost, the
	// next collection replaces it.
	payload.Inventory = a.inventory
	a.inventory = nil
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("could not encode heartbeat seq=%d: %v", payload.Seq, err)
		return
	}
	if err := a.outbox.Add(payload.Seq, data); err != nil {
		// The heartbeat is still held in memory; only its copy on disk failed.
		log.Printf("could not save heartbeat seq=%d to disk: %v", payload.Seq, err)
	}
	log.Printf("heartbeat collected (seq=%d, %s=%s, cpu=%.1f%%, mem=%.1f%%)",
		payload.Seq, a.runtime.Name(), runtimeState(payload.Report), payload.System.CPUPercent, payload.System.MemoryPercent)
}

// watchInventory asks the runtime what it has loaded, shortly after starting
// and then at a slow, steady pace. It runs on the side because the answer can
// take many seconds, and a heartbeat must never wait for it. The channel is
// nil, and so never ready, for a runtime that cannot be asked.
func (a *Agent) watchInventory(ctx context.Context) <-chan heartbeat.Inventory {
	source, ok := a.runtime.(adapter.Inventorier)
	if !ok || !a.cfg.Inventory || !adapter.Supports(a.runtime, adapter.CapInventory) {
		return nil
	}

	out := make(chan heartbeat.Inventory)
	go func() {
		wait := a.inventoryDelay
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			wait = a.inventoryEvery

			found := source.Inventory(ctx)
			inventory := heartbeat.Inventory{
				CollectedAt: time.Now().UTC(),
				Skills:      found.Skills,
				Connections: found.Connections,
				Revision:    found.Revision,
			}
			select {
			case out <- inventory:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// flush sends every heartbeat that is waiting and has not yet been sent on
// this connection, oldest first.
func (a *Agent) flush(ctx context.Context, connected chan struct{}) {
	if !a.client.IsConnected() {
		if waiting := a.outbox.Len(); waiting > 0 && !a.connecting {
			log.Printf("offline: %d heartbeats waiting", waiting)
		}
		a.connect(ctx, connected)
		return
	}

	for _, entry := range a.outbox.Pending() {
		if _, inFlight := a.sent[entry.Seq]; inFlight {
			continue
		}
		if a.attempts[entry.Seq] >= maxSendAttempts {
			log.Printf("giving up on heartbeat seq=%d: sent %d times with no acknowledgement", entry.Seq, maxSendAttempts)
			a.outbox.Drop(entry.Seq)
			delete(a.attempts, entry.Seq)
			continue
		}
		if err := a.client.SendHeartbeat(heartbeat.ProtocolVersion, entry.Data); err != nil {
			log.Printf("heartbeat send error (seq=%d): %v", entry.Seq, err)
			a.client.Close()
			a.reconnect(ctx, connected)
			return
		}
		a.sent[entry.Seq] = time.Now()
		a.attempts[entry.Seq]++
	}
}

// handle acts on a message from the manager.
func (a *Agent) handle(m ws.Message) {
	switch m.Type {
	case "heartbeat_ack":
		delete(a.sent, m.Seq)
		delete(a.attempts, m.Seq)
		if a.outbox.Ack(m.Seq) {
			log.Printf("heartbeat acknowledged (seq=%d, waiting=%d)", m.Seq, a.outbox.Len())
		}
	case "register_ack":
		// A manager from before protocol version 2 does not say; it speaks 1.
		a.managerProtocol = m.Protocol
		if a.managerProtocol < 1 {
			a.managerProtocol = 1
		}
		log.Printf("registered with manager (manager protocol %d, agent protocol %d)", a.managerProtocol, heartbeat.ProtocolVersion)
	}
}

// overdue reports a heartbeat that was sent and not acknowledged in time.
func (a *Agent) overdue() (uint64, bool) {
	for seq, at := range a.sent {
		if time.Since(at) > a.ackTimeout {
			return seq, true
		}
	}
	return 0, false
}

// reconnect forgets what was in flight on the old connection, so it is sent
// again on the new one, and starts connecting.
func (a *Agent) reconnect(ctx context.Context, connected chan struct{}) {
	a.sent = map[uint64]time.Time{}
	a.connect(ctx, connected)
}

// connect starts connecting on the side, unless that is already under way.
// Collection carries on meanwhile.
func (a *Agent) connect(ctx context.Context, connected chan struct{}) {
	if a.connecting || a.client.IsConnected() {
		return
	}
	a.connecting = true
	go func() {
		a.connectWithRetry(ctx)
		select {
		case connected <- struct{}{}:
		case <-ctx.Done():
		}
	}()
}

// connectWithRetry dials and registers, backing off between failures, until it
// succeeds or the agent is stopped.
func (a *Agent) connectWithRetry(ctx context.Context) {
	backoff := ws.NewBackoff()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := a.client.Connect()
		if err == nil {
			err = a.client.SendRegistration(a.registration())
			if err != nil {
				a.client.Close()
			}
		}
		if err == nil {
			log.Println("connected to manager")
			return
		}

		wait := backoff.Next()
		log.Printf("connection failed (attempt %d): %v — retrying in %s",
			backoff.Attempt(), err, wait.Round(time.Millisecond))

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (a *Agent) registration() ws.Registration {
	hostname, _ := os.Hostname()
	capabilities := []string{}
	for _, c := range a.runtime.Capabilities() {
		// Say only what this copy will in fact report.
		if c == adapter.CapInventory && !a.cfg.Inventory {
			continue
		}
		capabilities = append(capabilities, string(c))
	}
	return ws.Registration{
		Protocol:   heartbeat.ProtocolVersion,
		AgentID:    a.cfg.InstanceID,
		InstanceID: a.cfg.InstanceID,
		Runtime:    ws.RegistrationRuntime{Kind: a.runtime.Name(), Capabilities: capabilities},
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		Hostname:   hostname,
		Version:    Version,
	}
}

// runtimeState puts a report in one word for the log.
func runtimeState(r adapter.Report) string {
	switch {
	case !r.Healthy:
		return "offline"
	case !r.Ready:
		return "degraded"
	default:
		return "online"
	}
}
