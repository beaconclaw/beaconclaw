//go:build !noactions

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/beaconclaw/beaconclaw/internal/actions"
	"github.com/beaconclaw/beaconclaw/internal/adapter"
	"github.com/beaconclaw/beaconclaw/internal/config"
	"github.com/beaconclaw/beaconclaw/internal/heartbeat"
	"github.com/beaconclaw/beaconclaw/internal/outbox"
	"github.com/beaconclaw/beaconclaw/internal/ws"
)

// restartable is a runtime whose gateway can be restarted, and which counts
// how often it was.
type restartable struct {
	fakeRuntime
	mu       sync.Mutex
	restarts int
	// commandErr is what the runtime's restart command returns.
	commandErr error
}

func (r *restartable) RestartGateway(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restarts++
	return r.commandErr
}

func (r *restartable) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.restarts
}

// actingManager registers an agent as "agent-on-manager", sends it the
// commands it is given, and keeps what comes back.
type actingManager struct {
	srv *httptest.Server

	mu       sync.Mutex
	conn     *websocket.Conn
	register map[string]any
	results  []map[string]any
}

func newActingManager(t *testing.T) *actingManager {
	m := &actingManager{}
	upgrader := websocket.Upgrader{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			m.mu.Lock()
			switch msg["type"] {
			case "register":
				m.register = msg
				m.conn = conn
				conn.WriteJSON(map[string]any{"type": "register_ack", "protocol": 2, "agent_id": "agent-on-manager"})
			case "heartbeat":
				payload, _ := msg["payload"].(map[string]any)
				conn.WriteJSON(map[string]any{"type": "heartbeat_ack", "seq": payload["seq"]})
			case "action_result":
				m.results = append(m.results, msg)
			}
			m.mu.Unlock()
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *actingManager) send(id, agentID, action string, expires time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conn.WriteJSON(map[string]any{
		"type": "action", "id": id, "agent_id": agentID, "action": action,
		"expires_at": expires.UTC().Format(time.RFC3339),
		// A manager that tried to smuggle a command in would find nowhere for it to go.
		"command": "rm -rf /", "args": []string{"--force"},
	})
}

func (m *actingManager) result(id string) map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, result := range m.results {
		// The first answer to an accepted action only says it has begun.
		if result["id"] == id && result["started"] != true {
			return result
		}
	}
	return nil
}

// started reports whether the agent said it had begun the action.
func (m *actingManager) started(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, result := range m.results {
		if result["id"] == id && result["started"] == true {
			return true
		}
	}
	return false
}

func (m *actingManager) registered() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.register
}

func startActing(t *testing.T, m *actingManager, runtime adapter.Adapter, allowed []string) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "actions.log")
	url := "ws" + strings.TrimPrefix(m.srv.URL, "http")
	client := ws.NewClient(url, "bc_test")
	client.SetTiming(ws.Timing{PingEvery: 50 * time.Millisecond, SilentAfter: 500 * time.Millisecond})
	a := &Agent{
		cfg:            &config.Config{InstanceID: "local-id", Endpoint: url, Actions: allowed},
		client:         client,
		collector:      heartbeat.NewCollector("local-id", runtime),
		runtime:        runtime,
		gate:           actions.NewGate(allowed),
		actionsLog:     logPath,
		restartSettle:  300 * time.Millisecond,
		restartPoll:    20 * time.Millisecond,
		inventoryDelay: time.Hour,
		inventoryEvery: time.Hour,
		usageDelay:     time.Hour,
		usageEvery:     time.Hour,
		outbox:         outbox.Open(filepath.Join(dir, "outbox.json"), 50),
		interval:       40 * time.Millisecond,
		ackTimeout:     time.Second,
		ackCheck:       50 * time.Millisecond,
		sent:           map[uint64]time.Time{},
		attempts:       map[uint64]int{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	eventually(t, "the agent to register", func() bool { return m.registered() != nil })
	// Give the acknowledgement, which carries the agent's ID, time to arrive.
	time.Sleep(60 * time.Millisecond)
	return logPath
}

func TestAnAgentSaysWhichActionsAreSwitchedOnAndNothingByDefault(t *testing.T) {
	m := newActingManager(t)
	startActing(t, m, &restartable{}, nil)
	if _, present := m.registered()["actions"]; present {
		t.Fatalf("an agent with nothing switched on reported actions: %v", m.registered()["actions"])
	}

	m2 := newActingManager(t)
	startActing(t, m2, &restartable{}, []string{actions.Diagnostics, "format_disk"})
	got, _ := m2.registered()["actions"].([]any)
	if len(got) != 1 || got[0] != actions.Diagnostics {
		t.Fatalf("reported %v, want only diagnostics", got)
	}
}

func TestDiagnosticsReportsNamesAndStatesAndChangesNothing(t *testing.T) {
	m := newActingManager(t)
	runtime := &restartable{}
	logPath := startActing(t, m, runtime, []string{actions.Diagnostics})

	m.send("d1", "agent-on-manager", actions.Diagnostics, time.Now().Add(time.Minute))
	eventually(t, "the diagnostics result", func() bool { return m.result("d1") != nil })

	result := m.result("d1")
	report, _ := result["result"].(map[string]any)
	checks, _ := report["checks"].([]any)
	if len(checks) < 5 {
		t.Fatalf("expected a list of checks, got %v", result)
	}
	if runtime.count() != 0 {
		t.Fatal("diagnostics restarted the gateway")
	}
	record, _ := os.ReadFile(logPath)
	if !strings.Contains(string(record), "action=diagnostics id=d1 started") {
		t.Fatalf("the machine's own record is missing the request: %q", record)
	}
}

func TestRestartRunsOnlyWhenSwitchedOnAndReportsWhetherTheGatewayCameBack(t *testing.T) {
	m := newActingManager(t)
	runtime := &restartable{}
	logPath := startActing(t, m, runtime, []string{actions.Diagnostics})

	m.send("r1", "agent-on-manager", actions.RestartGateway, time.Now().Add(time.Minute))
	eventually(t, "the refusal", func() bool { return m.result("r1") != nil })
	if m.result("r1")["refused"] != true || runtime.count() != 0 {
		t.Fatalf("a restart that was not switched on was not refused: %v, restarts=%d", m.result("r1"), runtime.count())
	}
	record, _ := os.ReadFile(logPath)
	if !strings.Contains(string(record), "id=r1 refused") {
		t.Fatalf("the refusal was not recorded on the machine: %q", record)
	}

	m2 := newActingManager(t)
	runtime2 := &restartable{}
	startActing(t, m2, runtime2, []string{actions.RestartGateway})
	m2.send("r2", "agent-on-manager", actions.RestartGateway, time.Now().Add(time.Minute))
	eventually(t, "the restart result", func() bool { return m2.result("r2") != nil })
	if !m2.started("r2") {
		t.Fatal("the agent did not say it had begun the restart before reporting the result")
	}
	if m2.result("r2")["ok"] != true || runtime2.count() != 1 {
		t.Fatalf("restart: %v, restarts=%d", m2.result("r2"), runtime2.count())
	}
	// The same command again, and a second restart straight after, are both refused.
	m2.send("r2", "agent-on-manager", actions.RestartGateway, time.Now().Add(time.Minute))
	m2.send("r3", "agent-on-manager", actions.RestartGateway, time.Now().Add(time.Minute))
	eventually(t, "the second refusal", func() bool { return m2.result("r3") != nil })
	if m2.started("r3") {
		t.Fatal("a refused command was reported as begun")
	}
	if m2.result("r3")["refused"] != true || runtime2.count() != 1 {
		t.Fatalf("a second restart was not refused: %v, restarts=%d", m2.result("r3"), runtime2.count())
	}
}

func TestCommandsForAnotherAgentExpiredOrUnknownAreRefused(t *testing.T) {
	m := newActingManager(t)
	runtime := &restartable{}
	startActing(t, m, runtime, actions.Known())

	m.send("a", "some-other-agent", actions.RestartGateway, time.Now().Add(time.Minute))
	m.send("b", "agent-on-manager", actions.RestartGateway, time.Now().Add(-time.Second))
	m.send("c", "agent-on-manager", "run_command", time.Now().Add(time.Minute))
	eventually(t, "three refusals", func() bool { return m.result("a") != nil && m.result("b") != nil && m.result("c") != nil })

	for _, id := range []string{"a", "b", "c"} {
		if m.result(id)["refused"] != true {
			t.Errorf("command %s was not refused: %v", id, m.result(id))
		}
	}
	if runtime.count() != 0 {
		t.Fatalf("the gateway was restarted %d times by commands that should all have been refused", runtime.count())
	}
}

func TestARestartIsJudgedByWhetherTheGatewayAnswersNotByTheCommandsExitCode(t *testing.T) {
	// The runtime's command reports an error, but the gateway is answering afterwards.
	m := newActingManager(t)
	runtime := &restartable{commandErr: errors.New("exit status 1")}
	startActing(t, m, runtime, []string{actions.RestartGateway})
	m.send("r1", "agent-on-manager", actions.RestartGateway, time.Now().Add(time.Minute))
	eventually(t, "the restart result", func() bool { return m.result("r1") != nil })

	result := m.result("r1")
	detail, _ := result["result"].(map[string]any)
	if result["ok"] != true || detail["gateway_answering"] != true || detail["restart_command_ok"] != false {
		t.Fatalf("got %v, want a success that notes the command's error", result)
	}

	// With no copy of the runtime's command on the machine, nothing was run and it fails at once.
	m2 := newActingManager(t)
	missing := &restartable{commandErr: fmt.Errorf("not found: %w", exec.ErrNotFound)}
	startActing(t, m2, missing, []string{actions.RestartGateway})
	m2.send("r2", "agent-on-manager", actions.RestartGateway, time.Now().Add(time.Minute))
	eventually(t, "the failure", func() bool { return m2.result("r2") != nil })
	if m2.result("r2")["ok"] == true {
		t.Fatalf("a restart that never ran was reported as done: %v", m2.result("r2"))
	}
}
