package heartbeat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/beaconclaw/beaconclaw/internal/openclaw"
)

func readyServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]any{
			"ready":    true,
			"failing":  []string{},
			"uptimeMs": 5000,
		})
	})
	return httptest.NewServer(mux)
}

func TestCollectorSequence(t *testing.T) {
	srv := readyServer()
	defer srv.Close()

	parts := strings.Split(srv.URL, ":")
	port, _ := strconv.Atoi(parts[len(parts)-1])

	runtime := openclaw.NewAdapter(port)
	c := NewCollector("test-instance-id", runtime)

	p1 := c.Collect(context.Background())
	p2 := c.Collect(context.Background())

	if p1.Seq != 1 {
		t.Errorf("expected seq 1, got %d", p1.Seq)
	}
	if p2.Seq != 2 {
		t.Errorf("expected seq 2, got %d", p2.Seq)
	}
}

func TestCollectorInstanceID(t *testing.T) {
	srv := readyServer()
	defer srv.Close()

	parts := strings.Split(srv.URL, ":")
	port, _ := strconv.Atoi(parts[len(parts)-1])

	runtime := openclaw.NewAdapter(port)
	c := NewCollector("my-instance", runtime)

	p := c.Collect(context.Background())

	if p.InstanceID != "my-instance" {
		t.Errorf("expected instance ID 'my-instance', got %s", p.InstanceID)
	}
}

func TestCollectorTimestamp(t *testing.T) {
	srv := readyServer()
	defer srv.Close()

	parts := strings.Split(srv.URL, ":")
	port, _ := strconv.Atoi(parts[len(parts)-1])

	runtime := openclaw.NewAdapter(port)
	c := NewCollector("test", runtime)

	p := c.Collect(context.Background())

	if p.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
}

func TestCollectorOpenClawStatus(t *testing.T) {
	srv := readyServer()
	defer srv.Close()

	parts := strings.Split(srv.URL, ":")
	port, _ := strconv.Atoi(parts[len(parts)-1])

	runtime := openclaw.NewAdapter(port)
	c := NewCollector("test", runtime)

	p := c.Collect(context.Background())

	status, ok := p.Legacy.(openclaw.Status)
	if !ok {
		t.Fatalf("expected the adapter's own status as the version 1 data, got %T", p.Legacy)
	}
	if p.Runtime.Kind != "openclaw" || !p.Runtime.Healthy || !p.Runtime.Ready || !p.Runtime.Present {
		t.Errorf("expected a healthy openclaw runtime report, got %+v", p.Runtime)
	}
	if status.State != "online" {
		t.Errorf("expected openclaw online, got %s", status.State)
	}
	if !p.Report.Healthy || !p.Report.Ready || !p.Report.Present {
		t.Errorf("expected a present, healthy, ready report, got %+v", p.Report)
	}
	if !status.Healthy {
		t.Error("expected openclaw healthy")
	}
}

func TestCollectorPayloadJSON(t *testing.T) {
	srv := readyServer()
	defer srv.Close()

	parts := strings.Split(srv.URL, ":")
	port, _ := strconv.Atoi(parts[len(parts)-1])

	runtime := openclaw.NewAdapter(port)
	c := NewCollector("test", runtime)

	p := c.Collect(context.Background())

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	if parsed["instance_id"] != "test" {
		t.Error("instance_id missing from JSON")
	}
	if parsed["system"] == nil {
		t.Error("system missing from JSON")
	}
	if parsed["openclaw"] == nil {
		t.Error("openclaw missing from JSON")
	}
}

// A manager that only knows protocol version 1 must still find its fields, and
// a version 2 manager must find the neutral ones, in the same heartbeat.
func TestPayloadCarriesBothProtocolVersions(t *testing.T) {
	srv := readyServer()
	defer srv.Close()

	parts := strings.Split(srv.URL, ":")
	port, _ := strconv.Atoi(parts[len(parts)-1])

	data, err := json.Marshal(NewCollector("agent-1", openclaw.NewAdapter(port)).Collect(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		AgentID    string         `json:"agent_id"`
		InstanceID string         `json:"instance_id"`
		OpenClaw   map[string]any `json:"openclaw"`
		Runtime    map[string]any `json:"runtime"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed.AgentID != "agent-1" || parsed.InstanceID != "agent-1" {
		t.Errorf("expected both ID names, got agent_id=%q instance_id=%q", parsed.AgentID, parsed.InstanceID)
	}
	for _, key := range []string{"status", "healthy", "ready", "failing_channels", "uptime_ms", "gateway_port", "version"} {
		if _, ok := parsed.OpenClaw[key]; !ok {
			t.Errorf("version 1 field openclaw.%s is missing", key)
		}
	}
	for _, key := range []string{"kind", "present", "healthy", "ready", "failing", "raw"} {
		if _, ok := parsed.Runtime[key]; !ok {
			t.Errorf("version 2 field runtime.%s is missing", key)
		}
	}
	if parsed.Runtime["kind"] != "openclaw" {
		t.Errorf("expected runtime.kind openclaw, got %v", parsed.Runtime["kind"])
	}
	if _, isList := parsed.Runtime["failing"].([]any); !isList {
		t.Errorf("runtime.failing must be a list even when empty, got %T", parsed.Runtime["failing"])
	}
}
