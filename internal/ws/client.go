package ws

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeTimeout = 10 * time.Second
	pongTimeout  = 60 * time.Second
	pingInterval = 30 * time.Second
)

// Registration is sent as the first message after connecting.
type Registration struct {
	Type string `json:"type"`
	// Protocol is the wire format version this agent speaks.
	Protocol int    `json:"protocol"`
	AgentID  string `json:"agent_id"`
	// InstanceID is the version 1 name for AgentID.
	InstanceID string `json:"instance_id"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	Hostname   string `json:"hostname"`
	Version    string `json:"version"`
	// Runtime says which runtime this agent watches and what it can report.
	Runtime RegistrationRuntime `json:"runtime"`
}

// RegistrationRuntime describes the runtime adapter in use.
type RegistrationRuntime struct {
	Kind         string   `json:"kind"`
	Capabilities []string `json:"capabilities"`
}

// Message is something the manager sent to the agent.
type Message struct {
	// Type is "register_ack" or "heartbeat_ack". Other types are ignored by
	// the agent, so a newer manager can add messages without breaking it.
	Type string `json:"type"`
	// Seq is the heartbeat being acknowledged, on a "heartbeat_ack".
	Seq uint64 `json:"seq"`
	// Protocol is the newest protocol version the manager understands, on a
	// "register_ack". A manager from before version 2 sends none.
	Protocol int `json:"protocol"`
}

// maxMessageBytes bounds what the agent will read from the manager. Its
// messages are a few dozen bytes.
const maxMessageBytes = 64 * 1024

// Timing is how the client decides a connection is alive. It is a field so
// tests can use short times.
type Timing struct {
	// PingEvery is how often the agent pings the manager.
	PingEvery time.Duration
	// SilentAfter is how long the agent waits to hear anything, a pong
	// included, before it treats the connection as dead.
	SilentAfter time.Duration
}

// Client manages a WebSocket connection to the BeaconClaw manager. It reads as
// well as writes: what the manager sends arrives on Messages, and a connection
// that closes or goes silent is reported on Lost.
type Client struct {
	endpoint string
	token    string
	timing   Timing

	messages chan Message
	lost     chan struct{}

	mu   sync.Mutex
	conn *websocket.Conn
}

func NewClient(endpoint, token string) *Client {
	return &Client{
		endpoint: endpoint,
		token:    token,
		timing:   Timing{PingEvery: pingInterval, SilentAfter: pongTimeout},
		messages: make(chan Message, 256),
		lost:     make(chan struct{}, 1),
	}
}

// SetTiming replaces the liveness timing. Call it before Connect.
func (c *Client) SetTiming(t Timing) { c.timing = t }

// Messages delivers what the manager sends, in order.
func (c *Client) Messages() <-chan Message { return c.messages }

// Lost signals that the connection closed or went silent. The agent should
// reconnect.
func (c *Client) Lost() <-chan struct{} { return c.lost }

// Connect establishes a WebSocket connection to the manager and starts
// reading from it.
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.token)

	conn, _, err := dialer.Dial(c.endpoint, header)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.endpoint, err)
	}

	conn.SetReadLimit(maxMessageBytes)
	silentAfter := c.timing.SilentAfter
	_ = conn.SetReadDeadline(time.Now().Add(silentAfter))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(silentAfter))
	})

	c.conn = conn
	go c.readLoop(conn, silentAfter)
	go c.pingLoop(conn, c.timing.PingEvery)
	return nil
}

// readLoop reads until the connection fails. Anything heard, a message or a
// pong, proves the connection is alive and pushes the deadline back. When the
// read fails, because the manager closed the connection or nothing was heard
// in time, the connection is dropped and Lost is signalled.
func (c *Client) readLoop(conn *websocket.Conn, silentAfter time.Duration) {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			c.drop(conn)
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(silentAfter))

		var m Message
		if json.Unmarshal(data, &m) != nil || m.Type == "" {
			continue
		}
		select {
		case c.messages <- m:
		default:
			// Nobody is taking messages. Dropping one only delays an
			// acknowledgement; blocking here would stop liveness checks.
		}
	}
}

// pingLoop pings the manager until the connection is replaced or fails. The
// pongs are what keep a quiet connection from being treated as dead.
func (c *Client) pingLoop(conn *websocket.Conn, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for range ticker.C {
		if !c.isCurrent(conn) {
			return
		}
		if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout)); err != nil {
			c.drop(conn)
			return
		}
	}
}

func (c *Client) isCurrent(conn *websocket.Conn) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn == conn
}

// drop closes a failed connection. If it is still the current one, the client
// becomes disconnected and Lost is signalled once.
func (c *Client) drop(conn *websocket.Conn) {
	c.mu.Lock()
	current := c.conn == conn
	if current {
		c.conn = nil
	}
	c.mu.Unlock()

	conn.Close()
	if current {
		select {
		case c.lost <- struct{}{}:
		default:
		}
	}
}

// SendJSON marshals v to JSON and sends it over the WebSocket.
func (c *Client) SendJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return fmt.Errorf("not connected")
	}

	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	if err := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// SendRegistration sends the initial registration message.
func (c *Client) SendRegistration(reg Registration) error {
	reg.Type = "register"
	return c.SendJSON(reg)
}

// SendHeartbeat sends a heartbeat payload, marked with the protocol version.
func (c *Client) SendHeartbeat(protocol int, payload any) error {
	wrapper := struct {
		Type     string `json:"type"`
		Protocol int    `json:"protocol"`
		Payload  any    `json:"payload"`
	}{
		Type:     "heartbeat",
		Protocol: protocol,
		Payload:  payload,
	}
	return c.SendJSON(wrapper)
}

// Close shuts down the WebSocket connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil
	}

	// A deadline, so closing a connection that has gone dead cannot hang.
	err := c.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second),
	)
	c.conn.Close()
	c.conn = nil
	return err
}

// IsConnected returns whether the client has an active connection.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}
