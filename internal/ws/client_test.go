package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			conn.WriteMessage(mt, msg)
		}
	}))
}

func wsURL(s *httptest.Server) string {
	return "ws" + strings.TrimPrefix(s.URL, "http")
}

func TestClientConnectAndSend(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	c := NewClient(wsURL(srv), "bc_test")

	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	if !c.IsConnected() {
		t.Error("expected connected")
	}

	err := c.SendJSON(map[string]string{"hello": "world"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
}

func TestClientSendRegistration(t *testing.T) {
	received := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		received <- msg
		conn.ReadMessage()
	}))
	defer srv.Close()

	c := NewClient(wsURL(srv), "bc_test")
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	reg := Registration{
		Protocol:   2,
		AgentID:    "inst-123",
		Runtime:    RegistrationRuntime{Kind: "openclaw", Capabilities: []string{"health"}},
		InstanceID: "inst-123",
		OS:         "linux",
		Arch:       "amd64",
		Hostname:   "test-host",
		Version:    "beaconclaw/dev",
	}
	if err := c.SendRegistration(reg); err != nil {
		t.Fatalf("send registration: %v", err)
	}

	msg := <-received

	var parsed map[string]any
	if err := json.Unmarshal(msg, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["type"] != "register" {
		t.Errorf("expected type=register, got %v", parsed["type"])
	}
	if parsed["instance_id"] != "inst-123" || parsed["agent_id"] != "inst-123" {
		t.Errorf("expected both ID names to be inst-123, got instance_id=%v agent_id=%v", parsed["instance_id"], parsed["agent_id"])
	}
	if parsed["protocol"] != float64(2) {
		t.Errorf("expected protocol=2, got %v", parsed["protocol"])
	}
	runtime, _ := parsed["runtime"].(map[string]any)
	if runtime["kind"] != "openclaw" {
		t.Errorf("expected runtime.kind=openclaw, got %v", runtime["kind"])
	}
}

func TestClientSendHeartbeat(t *testing.T) {
	received := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		received <- msg
		conn.ReadMessage()
	}))
	defer srv.Close()

	c := NewClient(wsURL(srv), "bc_test")
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	payload := map[string]any{"seq": 1, "cpu": 42.5}
	if err := c.SendHeartbeat(2, payload); err != nil {
		t.Fatalf("send heartbeat: %v", err)
	}

	msg := <-received

	var parsed map[string]any
	if err := json.Unmarshal(msg, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["type"] != "heartbeat" {
		t.Errorf("expected type=heartbeat, got %v", parsed["type"])
	}
	if parsed["payload"] == nil {
		t.Error("expected payload field")
	}
}

func TestClientAuthHeader(t *testing.T) {
	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.ReadMessage()
	}))
	defer srv.Close()

	c := NewClient(wsURL(srv), "bc_mytoken123")
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	c.Close()

	if authHeader != "Bearer bc_mytoken123" {
		t.Errorf("expected 'Bearer bc_mytoken123', got '%s'", authHeader)
	}
}

func TestClientConnectFailure(t *testing.T) {
	c := NewClient("ws://127.0.0.1:19999", "bc_test")
	err := c.Connect()
	if err == nil {
		t.Error("expected connection error")
		c.Close()
	}
}

func TestClientSendNotConnected(t *testing.T) {
	c := NewClient("ws://127.0.0.1:19999", "bc_test")
	err := c.SendJSON(map[string]string{"test": "data"})
	if err == nil {
		t.Error("expected error when not connected")
	}
}

func TestClientCloseIdempotent(t *testing.T) {
	c := NewClient("ws://127.0.0.1:19999", "bc_test")
	// Close without connecting should not panic
	if err := c.Close(); err != nil {
		t.Errorf("close on unconnected client: %v", err)
	}
}

func TestClientIsConnected(t *testing.T) {
	c := NewClient("ws://127.0.0.1:19999", "bc_test")
	if c.IsConnected() {
		t.Error("should not be connected before Connect()")
	}
}

func waitLost(t *testing.T, c *Client, within time.Duration) {
	t.Helper()
	select {
	case <-c.Lost():
	case <-time.After(within):
		t.Fatal("the client did not notice the connection was gone")
	}
	if c.IsConnected() {
		t.Error("the client still reports connected after losing the connection")
	}
}

func TestClientDeliversWhatTheManagerSends(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.WriteJSON(map[string]any{"type": "register_ack", "protocol": 2})
		conn.WriteJSON(map[string]any{"type": "heartbeat_ack", "seq": 7})
		conn.WriteMessage(websocket.TextMessage, []byte("not json"))
		conn.WriteJSON(map[string]any{"type": "something_new", "detail": "ignored by older agents"})
		conn.ReadMessage()
	}))
	defer srv.Close()

	c := NewClient(wsURL(srv), "bc_test")
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	want := []Message{{Type: "register_ack", Protocol: 2}, {Type: "heartbeat_ack", Seq: 7}, {Type: "something_new"}}
	for _, expected := range want {
		select {
		case got := <-c.Messages():
			if got != expected {
				t.Errorf("got %+v, want %+v", got, expected)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("did not receive %+v", expected)
		}
	}
}

func TestClientNoticesWhenTheManagerClosesTheConnection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.Close()
	}))
	defer srv.Close()

	c := NewClient(wsURL(srv), "bc_test")
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	waitLost(t, c, 2*time.Second)
}

// A connection can die without either side being told, for example when a
// network path drops packets. The manager here stays connected but never
// answers, not even a ping.
func TestClientNoticesASilentConnection(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		<-release // never reads, so never answers a ping
	}))
	defer srv.Close()
	defer close(release)

	c := NewClient(wsURL(srv), "bc_test")
	c.SetTiming(Timing{PingEvery: 30 * time.Millisecond, SilentAfter: 200 * time.Millisecond})
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	waitLost(t, c, 2*time.Second)
}

func TestClientStaysConnectedWhileTheManagerAnswersPings(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	c := NewClient(wsURL(srv), "bc_test")
	c.SetTiming(Timing{PingEvery: 30 * time.Millisecond, SilentAfter: 150 * time.Millisecond})
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	// Several times the silence limit passes with nothing sent but pings.
	select {
	case <-c.Lost():
		t.Fatal("a quiet but healthy connection was treated as dead")
	case <-time.After(600 * time.Millisecond):
	}
	if !c.IsConnected() {
		t.Error("expected the client to still be connected")
	}
}

func TestClosingOnPurposeIsNotReportedAsALoss(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	c := NewClient(wsURL(srv), "bc_test")
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	c.Close()

	select {
	case <-c.Lost():
		t.Error("a deliberate close was reported as a lost connection")
	case <-time.After(200 * time.Millisecond):
	}
}
