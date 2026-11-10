package openclaw

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	probeTimeout           = 3 * time.Second
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

const (
	// The readiness answer is a few hundred bytes. Read no more than this.
	maxReadyBody = 64 * 1024
	// What is passed on to the manager is bounded too.
	maxFailingChannels = 50
	maxChannelNameLen  = 100
)

// boundNames caps how many channel names are relayed and how long each is.
func boundNames(names []string) []string {
	if len(names) > maxFailingChannels {
		names = names[:maxFailingChannels]
	}
	for i, name := range names {
		if len(name) > maxChannelNameLen {
			names[i] = name[:maxChannelNameLen]
		}
	}
	return names
}

func NewProber(port int) *Prober {
	return &Prober{
		port: port,
		client: &http.Client{
			Timeout: probeTimeout,
			// The probe talks to the local gateway only. Never follow it elsewhere.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
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

	ok := resp.StatusCode == http.StatusOK

	// The gateway answers 503 with the same body when it is degraded. That is
	// exactly when the names of what is failing matter, so the body is read
	// whatever the status.
	var r readinessResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReadyBody)).Decode(&r); err != nil {
		// No readable detail: the status alone decides.
		return readinessResponse{Ready: ok}
	}
	r.Failing = boundNames(r.Failing)
	// A status that says "not ready" wins over a body that claims otherwise.
	r.Ready = r.Ready && ok
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

// detectVersion finds the version of OpenClaw, preferring the copy the
// gateway is actually running over whatever a command lookup would find. The
// lookup path a service is given at install time goes stale, and has pointed
// at nothing, or at a different copy, on real machines.
func detectVersion() string {
	// 1. The package the running gateway was started from.
	if v := versionOfRunningGateway(); v != "" {
		return v
	}
	// 2. The package behind the openclaw command, read from disk.
	if v := versionOfInstalledCommand(); v != "" {
		return v
	}
	// 3. Ask the command itself.
	if v := versionFromCommand(); v != "" {
		return v
	}
	return "unknown"
}

func versionFromCommand() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "openclaw", "--version").Output()
	if err != nil {
		return ""
	}
	return parseVersionOutput(string(out))
}

// parseVersionOutput reads "OpenClaw 2026.3.28 (f9b1079)" or "openclaw/2026.3.15".
func parseVersionOutput(out string) string {
	version := strings.TrimSpace(out)
	if parts := strings.Fields(version); len(parts) >= 2 {
		version = parts[1]
	} else if idx := strings.LastIndex(version, "/"); idx >= 0 {
		version = version[idx+1:]
	}
	return cleanVersion(version)
}
