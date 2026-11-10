package openclaw

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

func TestAdapterReportsAHealthyGateway(t *testing.T) {
	srv := testServer(200, 200, &readinessResponse{Ready: true, UptimeMs: 5000})
	defer srv.Close()

	a := NewAdapter(portFromServer(t, srv))
	report := a.Probe(context.Background())

	if !report.Present || !report.Healthy || !report.Ready {
		t.Errorf("expected present, healthy and ready, got %+v", report)
	}
	status, ok := report.Raw.(Status)
	if !ok {
		t.Fatalf("raw data is %T, want Status", report.Raw)
	}
	if status.State != "online" || status.UptimeMs != 5000 {
		t.Errorf("raw data lost detail: %+v", status)
	}
}

func TestAdapterReportsADegradedGateway(t *testing.T) {
	srv := testServer(200, 200, &readinessResponse{Ready: false, Failing: []string{"discord"}})
	defer srv.Close()

	report := NewAdapter(portFromServer(t, srv)).Probe(context.Background())

	if !report.Healthy || report.Ready {
		t.Errorf("expected healthy but not ready, got %+v", report)
	}
}

func TestAdapterReportsAnAbsentRuntime(t *testing.T) {
	// No config in the home folder, no command on the path, nothing listening.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("PATH", t.TempDir())

	report := NewAdapter(1).Probe(context.Background())

	if report.Present || report.Healthy || report.Ready {
		t.Errorf("expected absent, got %+v", report)
	}
	if report.Version != "" {
		t.Errorf("expected no version, got %q", report.Version)
	}
}

func TestAdapterSeesAnInstalledButStoppedRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())
	writeConfig(t, home, ".openclaw", "openclaw.json", `{"gateway":{"port":19999}}`)

	report := NewAdapter(1).Probe(context.Background())

	if !report.Present || report.Healthy {
		t.Errorf("expected present but not healthy, got %+v", report)
	}
}

func TestAdapterDeclaresItsCapabilities(t *testing.T) {
	a := NewAdapter(1)
	for _, c := range []adapter.Capability{adapter.CapHealth, adapter.CapReadiness, adapter.CapChannels, adapter.CapUptime, adapter.CapVersion} {
		if !adapter.Supports(a, c) {
			t.Errorf("expected capability %s", c)
		}
	}
	if adapter.Supports(a, adapter.Capability("remote-actions")) {
		t.Error("claimed a capability it does not have")
	}
	if a.Name() != "openclaw" || a.Target() != "http://127.0.0.1:1" {
		t.Errorf("unexpected name or target: %s %s", a.Name(), a.Target())
	}
}

// The gateway status is sent to managers that are already deployed. Its field
// names must not change.
func TestStatusWireNamesAreStable(t *testing.T) {
	data, err := json.Marshal(Status{State: "online", Healthy: true, Ready: true, FailingChannels: []string{"x"}, UptimeMs: 1, GatewayPort: 2, Version: "3"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"status":"online","healthy":true,"ready":true,"failing_channels":["x"],"uptime_ms":1,"gateway_port":2,"version":"3"}`
	if string(data) != want {
		t.Errorf("wire format changed:\n got %s\nwant %s", data, want)
	}
}

func TestDetectPort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if port := DetectPort(); port != 0 {
		t.Errorf("expected 0 with no config, got %d", port)
	}

	writeConfig(t, home, ".clawdbot", "clawdbot.json", `{"gateway":{"port":18001}}`)
	if port := DetectPort(); port != 18001 {
		t.Errorf("expected the older folder's port 18001, got %d", port)
	}

	writeConfig(t, home, ".openclaw", "openclaw.json", `{"gateway":{"port":18002},"other":"ignored"}`)
	if port := DetectPort(); port != 18002 {
		t.Errorf("expected the current folder to win with 18002, got %d", port)
	}
}

func writeConfig(t *testing.T, home, dir, file, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, dir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, dir, file), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
