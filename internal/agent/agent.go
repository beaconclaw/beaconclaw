package agent

import (
	"context"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
	"github.com/beaconclaw/beaconclaw/internal/buffer"
	"github.com/beaconclaw/beaconclaw/internal/config"
	"github.com/beaconclaw/beaconclaw/internal/heartbeat"
	"github.com/beaconclaw/beaconclaw/internal/openclaw"
	"github.com/beaconclaw/beaconclaw/internal/ws"
)

// Agent is the main daemon orchestrator.
type Agent struct {
	cfg       *config.Config
	client    *ws.Client
	collector *heartbeat.Collector
	runtime   adapter.Adapter
	buf       *buffer.Ring[heartbeat.Payload]
	backoff   *ws.Backoff
}

func New(cfg *config.Config) *Agent {
	runtime := openclaw.NewAdapter(cfg.OpenClawPort)
	return &Agent{
		cfg:       cfg,
		client:    ws.NewClient(cfg.Endpoint, cfg.Token),
		collector: heartbeat.NewCollector(cfg.InstanceID, runtime),
		runtime:   runtime,
		buf:       buffer.New[heartbeat.Payload](cfg.BufferCapacity),
		backoff:   ws.NewBackoff(),
	}
}

// Run starts the agent and blocks until the context is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	log.Printf("beaconclaw agent starting (agent=%s)", a.cfg.InstanceID)
	log.Printf("manager endpoint: %s", a.cfg.Endpoint)
	log.Printf("%s gateway: %s", a.runtime.Name(), a.runtime.Target())

	// Initial connection
	a.connectWithRetry(ctx)

	ticker := time.NewTicker(time.Duration(a.cfg.HeartbeatInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("shutting down...")
			a.client.Close()
			return nil
		case <-ticker.C:
			a.tick(ctx)
		}
	}
}

func (a *Agent) tick(ctx context.Context) {
	payload := a.collector.Collect(ctx)

	if !a.client.IsConnected() {
		a.buf.Push(payload)
		log.Printf("offline — buffered heartbeat (seq=%d, buffered=%d)", payload.Seq, a.buf.Len())
		a.connectWithRetry(ctx)
		return
	}

	// Flush any buffered events first
	if buffered := a.buf.Drain(); len(buffered) > 0 {
		log.Printf("flushing %d buffered heartbeats", len(buffered))
		for _, p := range buffered {
			if err := a.client.SendHeartbeat(heartbeat.ProtocolVersion, p); err != nil {
				log.Printf("flush error: %v", err)
				a.buf.Push(payload)
				a.handleDisconnect()
				return
			}
		}
	}

	if err := a.client.SendHeartbeat(heartbeat.ProtocolVersion, payload); err != nil {
		log.Printf("heartbeat send error: %v", err)
		a.buf.Push(payload)
		a.handleDisconnect()
		return
	}

	log.Printf("heartbeat sent (seq=%d, %s=%s, cpu=%.1f%%, mem=%.1f%%)",
		payload.Seq, a.runtime.Name(), runtimeState(payload.Report), payload.System.CPUPercent, payload.System.MemoryPercent)
}

func (a *Agent) connectWithRetry(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := a.client.Connect(); err != nil {
			wait := a.backoff.Next()
			log.Printf("connection failed (attempt %d): %v — retrying in %s",
				a.backoff.Attempt(), err, wait.Round(time.Millisecond))

			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
				continue
			}
		}

		a.backoff.Reset()
		log.Println("connected to manager")

		// Send registration
		hostname, _ := os.Hostname()
		capabilities := []string{}
		for _, c := range a.runtime.Capabilities() {
			capabilities = append(capabilities, string(c))
		}
		reg := ws.Registration{
			Protocol:   heartbeat.ProtocolVersion,
			AgentID:    a.cfg.InstanceID,
			InstanceID: a.cfg.InstanceID,
			Runtime:    ws.RegistrationRuntime{Kind: a.runtime.Name(), Capabilities: capabilities},
			OS:         runtime.GOOS,
			Arch:       runtime.GOARCH,
			Hostname:   hostname,
			Version:    Version,
		}
		if err := a.client.SendRegistration(reg); err != nil {
			log.Printf("registration failed: %v", err)
			a.handleDisconnect()
			continue
		}

		log.Println("registered with manager")
		return
	}
}

func (a *Agent) handleDisconnect() {
	a.client.Close()
	a.backoff.Reset()
}

// Status returns the current agent status for the CLI.
func (a *Agent) Status() string {
	if a.client.IsConnected() {
		return "connected"
	}
	return "disconnected"
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
