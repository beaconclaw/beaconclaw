// Package actions is the agent's side of remote actions: a short, fixed list
// of things the manager may ask this machine to do.
//
// It is an add-on the machine's owner chooses. Every action is off until
// someone on this machine switches it on, and the manager cannot switch one
// on. What the manager sends is only the name of an action: there is no
// command line and there are no arguments, so the most a manager can ever ask
// for is one of the actions listed here that this machine allows.
package actions

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	// Diagnostics runs the agent's own checks again and reports what they
	// found. It changes nothing.
	Diagnostics = "diagnostics"
	// RestartGateway restarts the runtime's gateway, as an operator would by
	// hand.
	RestartGateway = "restart_gateway"
)

// restartEvery is the least time between two restarts on this machine,
// whoever asks and whatever the manager says.
const restartEvery = 5 * time.Minute

// maxValidity bounds how far ahead a command may claim to be valid. The
// manager gives one minute; anything much longer is not from a manager that
// follows the protocol.
const maxValidity = 5 * time.Minute

// Known lists every action this build can perform, in a stable order.
func Known() []string { return []string{Diagnostics, RestartGateway} }

// IsKnown reports whether name is an action this build can perform.
func IsKnown(name string) bool {
	for _, known := range Known() {
		if known == name {
			return true
		}
	}
	return false
}

// Describe says in one line what an action does on this machine.
func Describe(name string) string {
	switch name {
	case Diagnostics:
		return "run the agent's own checks again and report the result; changes nothing"
	case RestartGateway:
		return "restart the runtime's gateway; conversations in progress are cut off"
	default:
		return ""
	}
}

// Normalize returns the known actions in names, without repeats, in a stable
// order. Anything unknown is dropped.
func Normalize(names []string) []string {
	seen := map[string]bool{}
	for _, name := range names {
		if IsKnown(name) {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Command is what the manager sends. It has no field for a command line or
// for arguments, by design.
type Command struct {
	// ID is used once. A command with an ID already seen is refused.
	ID string
	// AgentID is the agent the manager meant, by the manager's own ID for it.
	AgentID string
	// Action is one of the names in Known.
	Action string
	// ExpiresAt is when the manager stops waiting. A command is refused after it.
	ExpiresAt time.Time
}

// Gate decides whether a command is acceptable on this machine. It is the
// last check before anything is done, and it does not trust the manager.
type Gate struct {
	mu          sync.Mutex
	allowed     map[string]bool
	seen        map[string]time.Time
	lastRestart time.Time
}

// NewGate makes a gate that lets through only the given actions, and only
// those this build knows.
func NewGate(allowed []string) *Gate {
	g := &Gate{allowed: map[string]bool{}, seen: map[string]time.Time{}}
	for _, name := range Normalize(allowed) {
		g.allowed[name] = true
	}
	return g
}

// Check returns nil when the command may be carried out now, and otherwise
// the reason it is refused. agentID is this agent's ID as the manager gave it
// when the agent registered. A command that passes is remembered, so the same
// ID is refused from then on.
func (g *Gate) Check(c Command, agentID string, now time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Forget IDs whose commands have long expired, so the list stays small.
	for id, expiry := range g.seen {
		if now.After(expiry.Add(maxValidity)) {
			delete(g.seen, id)
		}
	}

	switch {
	case !Enabled:
		return fmt.Errorf("remote actions are not part of this build")
	case c.ID == "":
		return fmt.Errorf("the command has no ID")
	case !IsKnown(c.Action):
		return fmt.Errorf("%q is not an action this agent can perform", c.Action)
	case !g.allowed[c.Action]:
		return fmt.Errorf("%q is not switched on on this machine", c.Action)
	case agentID == "" || c.AgentID != agentID:
		return fmt.Errorf("the command is for another agent")
	case c.ExpiresAt.IsZero() || !now.Before(c.ExpiresAt):
		return fmt.Errorf("the command has expired")
	case c.ExpiresAt.After(now.Add(maxValidity)):
		return fmt.Errorf("the command claims to be valid for too long")
	}
	if _, repeated := g.seen[c.ID]; repeated {
		return fmt.Errorf("the command was already received")
	}
	if c.Action == RestartGateway && !g.lastRestart.IsZero() && now.Sub(g.lastRestart) < restartEvery {
		return fmt.Errorf("the gateway was restarted less than %s ago", restartEvery)
	}

	g.seen[c.ID] = c.ExpiresAt
	if c.Action == RestartGateway {
		g.lastRestart = now
	}
	return nil
}

// Check is one line of a diagnostics report: a name and whether it is well.
// Note holds a short state such as a version or a percentage, never content.
type Check struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Note string `json:"note,omitempty"`
}
