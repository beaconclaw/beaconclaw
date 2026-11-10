// Package maintenance tells the manager that this agent is being worked on,
// so that its alerts are held back and the time is left out of its uptime.
// It uses the agent's own token, which can only act on this agent.
package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	path = "/v1/agent/maintenance"
	// DefaultFor is how long maintenance lasts when no length is given.
	DefaultFor = time.Hour
	// MaxFor is the longest the manager accepts.
	MaxFor = 14 * 24 * time.Hour
	// MaxReason is the longest reason the manager accepts.
	MaxReason = 255
)

// Window is the maintenance the manager now has on record.
type Window struct {
	StartsAt time.Time `json:"startsAt"`
	EndsAt   time.Time `json:"endsAt"`
	Reason   string    `json:"reason"`
}

// Client talks to the manager's maintenance address.
type Client struct {
	base  string
	token string
	http  *http.Client
	agent string
}

// BaseURL turns the manager's WebSocket address into its HTTP address:
// wss://host/ws becomes https://host.
func BaseURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid manager endpoint %q", endpoint)
	}
	switch u.Scheme {
	case "wss":
		return "https://" + u.Host, nil
	case "ws":
		return "http://" + u.Host, nil
	default:
		return "", fmt.Errorf("invalid manager endpoint %q", endpoint)
	}
}

// NewClient returns a client for the manager at the given WebSocket endpoint.
func NewClient(endpoint, token, version string) (*Client, error) {
	base, err := BaseURL(endpoint)
	if err != nil {
		return nil, err
	}
	return &Client{
		base:  base,
		token: token,
		http:  &http.Client{Timeout: 20 * time.Second},
		agent: "beaconclaw/" + version,
	}, nil
}

// Start puts this agent into maintenance from now, for the given length. If
// it is already in maintenance the manager keeps the window it has, made
// longer if this one runs later.
func (c *Client) Start(ctx context.Context, reason string, length time.Duration) (Window, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Window{}, fmt.Errorf("give a reason, such as --reason \"runtime upgrade\"")
	}
	if len(reason) > MaxReason {
		return Window{}, fmt.Errorf("the reason may be at most %d characters", MaxReason)
	}
	if length < time.Minute || length > MaxFor {
		return Window{}, fmt.Errorf("maintenance may last from 1 minute to 14 days")
	}

	body, err := json.Marshal(map[string]any{
		"reason": reason,
		// Round up, so "90s" is two minutes and never fewer than asked for.
		"minutes": int((length + time.Minute - 1) / time.Minute),
	})
	if err != nil {
		return Window{}, err
	}

	var window Window
	if err := c.do(ctx, http.MethodPost, body, &window); err != nil {
		return Window{}, err
	}
	return window, nil
}

// End takes this agent out of maintenance now. It reports whether the agent
// was in maintenance.
func (c *Client) End(ctx context.Context) (bool, error) {
	var result struct {
		Ended bool `json:"ended"`
	}
	if err := c.do(ctx, http.MethodDelete, nil, &result); err != nil {
		return false, err
	}
	return result.Ended, nil
}

func (c *Client) do(ctx context.Context, method string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.agent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the manager: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("the manager did not accept this agent's token; run 'beaconclaw auth --token <token>'")
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("this manager does not support maintenance from the command line yet")
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("the manager answered %d%s", resp.StatusCode, managerMessage(data))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("the manager's answer could not be read")
	}
	return nil
}

// managerMessage pulls the short explanation out of an error answer.
func managerMessage(data []byte) string {
	var answer struct {
		Message any `json:"message"`
	}
	if json.Unmarshal(data, &answer) != nil {
		return ""
	}
	switch m := answer.Message.(type) {
	case string:
		if m != "" {
			return ": " + m
		}
	case []any:
		if len(m) > 0 {
			return fmt.Sprintf(": %v", m[0])
		}
	}
	return ""
}
