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

	status, ok := p.Runtime.(openclaw.Status)
	if !ok {
		t.Fatalf("expected the adapter's own status as the runtime data, got %T", p.Runtime)
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
