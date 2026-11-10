package openclaw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writePackage(t *testing.T, root, name, version string) {
	t.Helper()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"` + name + `","version":"` + version + `","other":{"ignored":true}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayPackageRoot(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "a gateway started by node",
			args: []string{"/usr/bin/node", "--max-old-space-size=2048", "/home/me/.npm-global/lib/node_modules/openclaw/dist/index.js", "gateway", "--port", "18789"},
			want: filepath.FromSlash("/home/me/.npm-global/lib/node_modules/openclaw"),
		},
		{
			name: "another openclaw command is not the gateway",
			args: []string{"node", "/home/me/.npm-global/lib/node_modules/openclaw/dist/index.js", "status"},
			want: "",
		},
		{
			name: "an unrelated process that merely says gateway",
			args: []string{"/usr/sbin/some-gateway", "gateway"},
			want: "",
		},
		{
			name: "a package whose name only starts with openclaw",
			args: []string{"node", "/x/node_modules/openclaw-tools/index.js", "gateway"},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gatewayPackageRoot(tt.args); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPackageVersion(t *testing.T) {
	dir := t.TempDir()

	root := filepath.Join(dir, "node_modules", "openclaw")
	writePackage(t, root, "openclaw", "2026.9.9")
	if got := packageVersion(root); got != "2026.9.9" {
		t.Errorf("got %q, want 2026.9.9", got)
	}

	other := filepath.Join(dir, "other")
	writePackage(t, other, "something-else", "1.0.0")
	if got := packageVersion(other); got != "" {
		t.Errorf("read a version from a package that is not OpenClaw: %q", got)
	}

	odd := filepath.Join(dir, "odd")
	writePackage(t, odd, "openclaw", "not a version; rm -rf")
	if got := packageVersion(odd); got != "" {
		t.Errorf("accepted something that is not a version: %q", got)
	}

	if got := packageVersion(filepath.Join(dir, "missing")); got != "" {
		t.Errorf("expected no version for a missing package, got %q", got)
	}
}

// The service's lookup path may not include node, so running the command
// fails. Following the command to its package still finds the version.
func TestVersionOfInstalledCommandNeedsNoWorkingCommand(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "lib", "node_modules", "openclaw")
	writePackage(t, root, "openclaw", "2026.10.1")
	entry := filepath.Join(root, "openclaw.mjs")
	if err := os.WriteFile(entry, []byte("#!/usr/bin/env node-that-is-not-installed\n"), 0700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(entry, filepath.Join(bin, "openclaw")); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("HOME", t.TempDir())

	if got := versionOfInstalledCommand(); got != "2026.10.1" {
		t.Errorf("got %q, want 2026.10.1", got)
	}
	if got := versionFromCommand(); got != "" {
		t.Errorf("expected running the command to fail in this setup, got %q", got)
	}
}

func TestParseVersionOutput(t *testing.T) {
	for in, want := range map[string]string{
		"OpenClaw 2026.3.28 (f9b1079)\n": "2026.3.28",
		"openclaw/2026.3.15":             "2026.3.15",
		"":                               "",
		"unknown":                        "",
	} {
		if got := parseVersionOutput(in); got != want {
			t.Errorf("parseVersionOutput(%q) = %q, want %q", in, got, want)
		}
	}
}

// A degraded gateway answers 503 and still says what is failing.
func TestProbeKeepsDetailWhenTheGatewayIsDegraded(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"ready":false,"failing":["discord","slack"],"uptimeMs":120000}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	status := NewProber(portFromServer(t, srv)).Probe(context.Background())

	if status.State != "degraded" || status.Ready {
		t.Errorf("expected degraded and not ready, got state=%s ready=%v", status.State, status.Ready)
	}
	if len(status.FailingChannels) != 2 || status.FailingChannels[0] != "discord" {
		t.Errorf("the failing channels were lost: %v", status.FailingChannels)
	}
	if status.UptimeMs != 120000 {
		t.Errorf("the uptime was lost: %d", status.UptimeMs)
	}
}

func TestProbeTrustsTheStatusOverABodyThatSaysReady(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"ready":true,"failing":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if status := NewProber(portFromServer(t, srv)).Probe(context.Background()); status.Ready {
		t.Error("a 503 was reported as ready")
	}
}

func TestProbeHandlesADegradedAnswerWithNoBody(t *testing.T) {
	srv := testServer(200, http.StatusServiceUnavailable, nil)
	defer srv.Close()

	status := NewProber(portFromServer(t, srv)).Probe(context.Background())
	if status.Ready || status.State != "degraded" {
		t.Errorf("expected degraded, got state=%s ready=%v", status.State, status.Ready)
	}
}
