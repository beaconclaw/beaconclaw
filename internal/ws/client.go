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
	Type       string `json:"type"`
	InstanceID string `json:"instance_id"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	Hostname   string `json:"hostname"`
	Version    string `json:"version"`
}

// Client manages a WebSocket connection to the BeaconClaw manager.
type Client struct {
	endpoint string
	token    string

	mu   sync.Mutex
	conn *websocket.Conn
}

func NewClient(endpoint, token string) *Client {
	return &Client{
		endpoint: endpoint,
		token:    token,
	}
}

// Connect establishes a WebSocket connection to the manager.
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

	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongTimeout))
	})

	c.conn = conn
	return nil
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

// SendHeartbeat sends a heartbeat payload.
func (c *Client) SendHeartbeat(payload any) error {
	wrapper := struct {
		Type    string `json:"type"`
		Payload any    `json:"payload"`
	}{
		Type:    "heartbeat",
		Payload: payload,
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

	err := c.conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
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
