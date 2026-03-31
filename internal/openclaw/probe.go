package openclaw

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	probeTimeout       = 3 * time.Second
	versionRecheckInterval = 10 * time.Minute
)

// Status represents the observed state of the local OpenClaw gateway.
type Status struct {
	State           string   `json:"status"`
	Healthy         bool     `json:"healthy"`
	Ready           bool     `json:"ready"`
	FailingChannels []string `json:"failing_channels"`
	UptimeMs        int64    `json:"uptime_ms"`
	GatewayPort     int      `json:"gateway_port"`
	Version         string   `json:"version"`
}

type readinessResponse struct {
	Ready    bool     `json:"ready"`
	Failing  []string `json:"failing"`
	UptimeMs int64    `json:"uptimeMs"`
}

// Prober checks the health of a local OpenClaw gateway via HTTP.
type Prober struct {
	port   int
	client *http.Client

	mu             sync.RWMutex
	cachedVersion  string
	versionChecked time.Time
}

func NewProber(port int) *Prober {
	return &Prober{
		port: port,
		client: &http.Client{
			Timeout: probeTimeout,
		},
	}
}

// Probe checks the local OpenClaw gateway and returns its status.
func (p *Prober) Probe(ctx context.Context) Status {
	s := Status{
		GatewayPort: p.port,
		Version:     p.getVersion(),
	}

	// Check liveness
	healthy := p.checkHealth(ctx)
	s.Healthy = healthy

	if !healthy {
		s.State = "offline"
		return s
	}

	// Check readiness
	readiness := p.checkReady(ctx)
	s.Ready = readiness.Ready
	s.FailingChannels = readiness.Failing
	s.UptimeMs = readiness.UptimeMs

	if readiness.Ready {
		s.State = "online"
	} else {
		s.State = "degraded"
	}

	return s
}

func (p *Prober) checkHealth(ctx context.Context) bool {
	url := fmt.Sprintf("http://127.0.0.1:%d/health", p.port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (p *Prober) checkReady(ctx context.Context) readinessResponse {
	url := fmt.Sprintf("http://127.0.0.1:%d/ready", p.port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return readinessResponse{}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return readinessResponse{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readinessResponse{Ready: false}
	}

	var r readinessResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		// If we can't parse the body but got 200, consider it ready
		return readinessResponse{Ready: true}
	}
	return r
}

func (p *Prober) getVersion() string {
	p.mu.RLock()
	if p.cachedVersion != "" && time.Since(p.versionChecked) < versionRecheckInterval {
		v := p.cachedVersion
		p.mu.RUnlock()
		return v
	}
	p.mu.RUnlock()

	v := detectVersion()

	p.mu.Lock()
	p.cachedVersion = v
	p.versionChecked = time.Now()
	p.mu.Unlock()

	return v
}

func detectVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "openclaw", "--version")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}

	version := strings.TrimSpace(string(out))
	// openclaw --version outputs "OpenClaw 2026.3.28 (f9b1079)" or "openclaw/2026.3.15"
	if parts := strings.Fields(version); len(parts) >= 2 {
		version = parts[1] // extract "2026.3.28" from "OpenClaw 2026.3.28 ..."
	} else if idx := strings.LastIndex(version, "/"); idx >= 0 {
		version = version[idx+1:]
	}
	if version == "" {
		return "unknown"
	}
	return version
}
