package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/beaconclaw/agent/internal/buffer"
	"github.com/beaconclaw/agent/internal/config"
	"github.com/beaconclaw/agent/internal/heartbeat"
	"github.com/beaconclaw/agent/internal/openclaw"
	"github.com/beaconclaw/agent/internal/ws"
)

// Agent is the main daemon orchestrator.
type Agent struct {
	cfg       *config.Config
	client    *ws.Client
	collector *heartbeat.Collector
	buf       *buffer.Ring[heartbeat.Payload]
	backoff   *ws.Backoff
}

func New(cfg *config.Config) *Agent {
	prober := openclaw.NewProber(cfg.OpenClawPort)
	return &Agent{
		cfg:       cfg,
		client:    ws.NewClient(cfg.Endpoint, cfg.Token),
		collector: heartbeat.NewCollector(cfg.InstanceID, prober),
		buf:       buffer.New[heartbeat.Payload](cfg.BufferCapacity),
		backoff:   ws.NewBackoff(),
	}
}

// Run starts the agent and blocks until the context is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	log.Printf("beaconclaw agent starting (instance=%s)", a.cfg.InstanceID)
	log.Printf("manager endpoint: %s", a.cfg.Endpoint)
	log.Printf("openclaw gateway: http://127.0.0.1:%d", a.cfg.OpenClawPort)

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
			if err := a.client.SendHeartbeat(p); err != nil {
				log.Printf("flush error: %v", err)
				a.buf.Push(payload)
				a.handleDisconnect()
				return
			}
		}
	}

	if err := a.client.SendHeartbeat(payload); err != nil {
		log.Printf("heartbeat send error: %v", err)
		a.buf.Push(payload)
		a.handleDisconnect()
		return
	}

	log.Printf("heartbeat sent (seq=%d, openclaw=%s, cpu=%.1f%%, mem=%.1f%%)",
		payload.Seq, payload.OpenClaw.State, payload.System.CPUPercent, payload.System.MemoryPercent)
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
		reg := ws.Registration{
			InstanceID: a.cfg.InstanceID,
			OS:         runtime.GOOS,
			Arch:       runtime.GOARCH,
			Hostname:   hostname,
			Version:    fmt.Sprintf("beaconclaw/%s", Version),
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
