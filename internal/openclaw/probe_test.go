package openclaw

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func testServer(healthStatus, readyStatus int, readyBody *readinessResponse) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(healthStatus)
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(readyStatus)
		if readyBody != nil {
			json.NewEncoder(w).Encode(readyBody)
		}
	})
	return httptest.NewServer(mux)
}

func portFromServer(t *testing.T, s *httptest.Server) int {
	t.Helper()
	// URL is http://127.0.0.1:PORT
	parts := strings.Split(s.URL, ":")
	port, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		t.Fatalf("parse port from %s: %v", s.URL, err)
	}
	return port
}

func TestProbeOnline(t *testing.T) {
	srv := testServer(200, 200, &readinessResponse{
		Ready:    true,
		Failing:  []string{},
		UptimeMs: 60000,
	})
	defer srv.Close()

	p := NewProber(portFromServer(t, srv))
	s := p.Probe(context.Background())

	if s.State != "online" {
		t.Errorf("expected online, got %s", s.State)
	}
	if !s.Healthy {
		t.Error("expected healthy=true")
	}
	if !s.Ready {
		t.Error("expected ready=true")
	}
	if s.UptimeMs != 60000 {
		t.Errorf("expected uptime 60000, got %d", s.UptimeMs)
	}
}

func TestProbeDegraded(t *testing.T) {
	srv := testServer(200, 200, &readinessResponse{
		Ready:   false,
		Failing: []string{"discord", "slack"},
	})
	defer srv.Close()

	p := NewProber(portFromServer(t, srv))
	s := p.Probe(context.Background())

	if s.State != "degraded" {
		t.Errorf("expected degraded, got %s", s.State)
	}
	if !s.Healthy {
		t.Error("expected healthy=true")
	}
	if s.Ready {
		t.Error("expected ready=false")
	}
	if len(s.FailingChannels) != 2 {
		t.Errorf("expected 2 failing channels, got %d", len(s.FailingChannels))
	}
}

func TestProbeOfflineHealthFails(t *testing.T) {
	srv := testServer(503, 503, nil)
	defer srv.Close()

	p := NewProber(portFromServer(t, srv))
	s := p.Probe(context.Background())

	if s.State != "offline" {
		t.Errorf("expected offline, got %s", s.State)
	}
	if s.Healthy {
		t.Error("expected healthy=false")
	}
}

func TestProbeOfflineNoServer(t *testing.T) {
	// Use a port where nothing is listening
	p := NewProber(19999)
	s := p.Probe(context.Background())

	if s.State != "offline" {
		t.Errorf("expected offline, got %s", s.State)
	}
	if s.Healthy {
		t.Error("expected healthy=false")
	}
}

func TestProbeReadyNoBody(t *testing.T) {
	// /ready returns 200 but no JSON body
	srv := testServer(200, 200, nil)
	defer srv.Close()

	p := NewProber(portFromServer(t, srv))
	s := p.Probe(context.Background())

	if s.State != "online" {
		t.Errorf("expected online, got %s", s.State)
	}
	if !s.Ready {
		t.Error("expected ready=true when 200 with no body")
	}
}

func TestProbeGatewayPort(t *testing.T) {
	srv := testServer(200, 200, &readinessResponse{Ready: true})
	defer srv.Close()

	port := portFromServer(t, srv)
	p := NewProber(port)
	s := p.Probe(context.Background())

	if s.GatewayPort != port {
		t.Errorf("expected port %d, got %d", port, s.GatewayPort)
	}
}

func TestDetectVersionUnknown(t *testing.T) {
	// When openclaw binary doesn't exist, should return "unknown"
	v := detectVersion()
	// On CI/dev machines without openclaw, this should be "unknown"
	// On machines with openclaw, it'll be a real version — both are valid
	if v == "" {
		t.Error("version should never be empty")
	}
}

func TestProberVersionCaching(t *testing.T) {
	p := NewProber(18789)

	v1 := p.getVersion()
	v2 := p.getVersion()

	if v1 != v2 {
		t.Errorf("cached version should be stable: %s != %s", v1, v2)
	}
}

func TestCheckHealthContextCancelled(t *testing.T) {
	srv := testServer(200, 200, nil)
	defer srv.Close()

	p := NewProber(portFromServer(t, srv))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	healthy := p.checkHealth(ctx)
	if healthy {
		t.Error("expected unhealthy with cancelled context")
	}
}
