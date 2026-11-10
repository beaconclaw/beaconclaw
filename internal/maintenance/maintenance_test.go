package maintenance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type seen struct {
	method string
	path   string
	auth   string
	body   map[string]any
}

func manager(t *testing.T, status int, answer string) (*Client, *seen) {
	t.Helper()
	got := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &got.body)
		w.WriteHeader(status)
		io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	client, err := NewClient("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", "bc_test", "test")
	if err != nil {
		t.Fatal(err)
	}
	return client, got
}

func TestBaseURL(t *testing.T) {
	for endpoint, want := range map[string]string{
		"wss://api.example.com/ws":   "https://api.example.com",
		"ws://127.0.0.1:4000/ws":     "http://127.0.0.1:4000",
		"wss://api.example.com:8443": "https://api.example.com:8443",
	} {
		got, err := BaseURL(endpoint)
		if err != nil || got != want {
			t.Errorf("BaseURL(%q) = %q, %v; want %q", endpoint, got, err, want)
		}
	}
	for _, bad := range []string{"", "https://api.example.com", "api.example.com/ws"} {
		if _, err := BaseURL(bad); err == nil {
			t.Errorf("BaseURL(%q) should fail", bad)
		}
	}
}

func TestStartSendsTheReasonAndLengthWithTheAgentsOwnToken(t *testing.T) {
	client, got := manager(t, 201, `{"startsAt":"2026-06-01T12:00:00Z","endsAt":"2026-06-01T12:02:00Z","reason":"upgrade"}`)

	window, err := client.Start(context.Background(), "  upgrade  ", 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if got.method != "POST" || got.path != "/v1/agent/maintenance" || got.auth != "Bearer bc_test" {
		t.Fatalf("unexpected request: %+v", got)
	}
	if got.body["reason"] != "upgrade" || got.body["minutes"] != float64(2) {
		t.Fatalf("unexpected body: %v (90 seconds should round up to 2 minutes)", got.body)
	}
	if len(got.body) != 2 {
		t.Fatalf("only the reason and the length may be sent, got %v", got.body)
	}
	if window.Reason != "upgrade" || window.EndsAt.Sub(window.StartsAt) != 2*time.Minute {
		t.Fatalf("unexpected window: %+v", window)
	}
}

func TestStartRefusesWhatTheManagerWouldRefuse(t *testing.T) {
	client, got := manager(t, 201, `{}`)
	for name, call := range map[string]func() error{
		"no reason": func() error { _, err := client.Start(context.Background(), "  ", time.Hour); return err },
		"long reason": func() error {
			_, err := client.Start(context.Background(), strings.Repeat("x", 256), time.Hour)
			return err
		},
		"too short": func() error { _, err := client.Start(context.Background(), "x", time.Second); return err },
		"too long":  func() error { _, err := client.Start(context.Background(), "x", 15*24*time.Hour); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if got.method != "" {
		t.Fatal("nothing should have been sent")
	}
}

func TestEndReportsWhetherThereWasAny(t *testing.T) {
	client, got := manager(t, 200, `{"ended":true}`)
	ended, err := client.End(context.Background())
	if err != nil || !ended {
		t.Fatalf("ended=%v err=%v", ended, err)
	}
	if got.method != "DELETE" || got.path != "/v1/agent/maintenance" || got.body != nil {
		t.Fatalf("unexpected request: %+v", got)
	}

	client, _ = manager(t, 200, `{"ended":false}`)
	if ended, err := client.End(context.Background()); err != nil || ended {
		t.Fatalf("ended=%v err=%v", ended, err)
	}
}

func TestErrorsSayWhatToDo(t *testing.T) {
	for status, want := range map[int]string{
		401: "did not accept this agent's token",
		404: "does not support maintenance",
		400: "answered 400: reason should not be empty",
		500: "answered 500",
	} {
		client, _ := manager(t, status, `{"message":["reason should not be empty"]}`)
		_, err := client.End(context.Background())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("status %d: got %v, want it to contain %q", status, err, want)
		}
	}
}
