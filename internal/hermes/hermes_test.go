package hermes

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

const now = int64(1_794_446_000_000) // a fixed "now", in ms

func testAdapter(home string, alive bool) *Adapter {
	return &Adapter{
		home: home,
		now:  func() int64 { return now },
		alive: func(int32, int64) (bool, int64) {
			if !alive {
				return false, 0
			}
			return true, 90_000
		},
	}
}

func stamp(ago time.Duration) string {
	return time.UnixMilli(now).Add(-ago).UTC().Format(time.RFC3339Nano)
}

func TestJudge(t *testing.T) {
	cases := []struct {
		name    string
		state   GatewayState
		alive   bool
		healthy bool
		ready   bool
		failing []string
		word    string
	}{
		{
			name:    "running, fresh, every platform connected",
			state:   GatewayState{GatewayState: "running", UpdatedAt: stamp(30 * time.Second), Platforms: map[string]PlatformState{"telegram": {State: "connected"}}},
			alive:   true,
			healthy: true, ready: true, word: "running",
		},
		{
			name:    "a platform is retrying",
			state:   GatewayState{GatewayState: "running", UpdatedAt: stamp(10 * time.Second), Platforms: map[string]PlatformState{"telegram": {State: "retrying"}, "discord": {State: "connected"}}},
			alive:   true,
			healthy: true, ready: false, failing: []string{"telegram"}, word: "running",
		},
		{
			name:    "process alive but the state stopped being refreshed: says connected, is not answering",
			state:   GatewayState{GatewayState: "running", UpdatedAt: stamp(5 * time.Minute), Platforms: map[string]PlatformState{"telegram": {State: "connected"}}},
			alive:   true,
			healthy: false, ready: false, failing: []string{"gateway"}, word: "running",
		},
		{
			name:    "the process is gone although the file says running",
			state:   GatewayState{GatewayState: "running", UpdatedAt: stamp(10 * time.Second)},
			alive:   false,
			healthy: false, ready: false, word: "stopped",
		},
		{
			name:    "the gateway says it is degraded",
			state:   GatewayState{GatewayState: "degraded", UpdatedAt: stamp(10 * time.Second)},
			alive:   true,
			healthy: true, ready: false, word: "degraded",
		},
		{
			name:    "a platform needs attention",
			state:   GatewayState{GatewayState: "running", UpdatedAt: stamp(10 * time.Second), Platforms: map[string]PlatformState{"slack": {State: "connected", NeedsAttention: true}}},
			alive:   true,
			healthy: true, ready: false, failing: []string{"slack"}, word: "running",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := testAdapter(t.TempDir(), c.alive).judge(c.state)
			if got.Healthy != c.healthy || got.Ready != c.ready || got.State != c.word {
				t.Errorf("healthy=%v ready=%v state=%q, want %v %v %q", got.Healthy, got.Ready, got.State, c.healthy, c.ready, c.word)
			}
			if !reflect.DeepEqual(got.Failing, c.failing) {
				t.Errorf("failing = %v, want %v", got.Failing, c.failing)
			}
		})
	}
}

func TestProbeWithNoHermes(t *testing.T) {
	report := testAdapter(t.TempDir(), true).Probe(context.Background())
	if report.Healthy || report.Ready {
		t.Errorf("no state file must not be healthy: %+v", report)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryNamesOnly(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "skills", ".bundled_manifest"), "arxiv:abc\n")
	write(t, filepath.Join(home, "skills", "research", "arxiv", "SKILL.md"), "---\nname: arxiv\n---\n")
	write(t, filepath.Join(home, "skills", "mine", "SKILL.md"), "x")
	write(t, filepath.Join(home, "skills", ".hub", "cached", "SKILL.md"), "ignored: a hidden folder")
	write(t, filepath.Join(home, stateFile), `{"gateway_state":"running","platforms":{"api_server":{"state":"connected"},"default:telegram":{"state":"connected"}}}`)
	write(t, filepath.Join(home, "config.yaml"), `
model:
  default: "some-model"
  provider: openrouter
  api_key: "sk-must-never-be-read-out"
providers:
  ollama:
    base_url: http://localhost:11434/v1
mcp_servers:
  github:
    command: npx
    env:
      GITHUB_TOKEN: "ghp_secret"
platforms:
  discord:
    enabled: true
  slack:
    enabled: false
`)
	write(t, filepath.Join(home, ".env"), "OPENROUTER_API_KEY=never-opened\n")

	found := testAdapter(home, true).Inventory(context.Background())

	wantSkills := []adapter.Skill{{Name: "arxiv", Source: "hermes-bundled"}, {Name: "mine", Source: "local"}}
	if !reflect.DeepEqual(found.Skills, wantSkills) {
		t.Errorf("skills = %v, want %v", found.Skills, wantSkills)
	}
	wantConnections := []adapter.Connection{
		{Kind: kindChannel, Name: "discord"},
		{Kind: kindChannel, Name: "telegram"},
		{Kind: kindModelProvider, Name: "ollama"},
		{Kind: kindModelProvider, Name: "openrouter"},
		{Kind: kindToolServer, Name: "github"},
	}
	if !reflect.DeepEqual(found.Connections, wantConnections) {
		t.Errorf("connections = %v, want %v", found.Connections, wantConnections)
	}
	if found.Credentials != nil {
		t.Errorf("credentials must not be reported: %v", found.Credentials)
	}
}

func TestUsageCountsByDay(t *testing.T) {
	home := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(home, sessionStore))
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	at := func(hours float64) float64 { return float64(day.Unix()) + hours*3600 }
	statements := []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, estimated_cost_usd REAL, actual_cost_usd REAL)`,
		`CREATE TABLE messages (id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT, timestamp REAL, token_count INTEGER, active INTEGER, message_uid TEXT)`,
		`INSERT INTO sessions VALUES ('s1','telegram',0.35,NULL),('s2','cli',9.99,1.6)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(session, role string, hours float64, tokens, active int, uid string) {
		if _, err := db.Exec(`INSERT INTO messages (session_id, role, content, timestamp, token_count, active, message_uid) VALUES (?,?,?,?,?,?,?)`, session, role, "private text", at(hours), tokens, active, uid); err != nil {
			t.Fatal(err)
		}
	}
	insert("s1", "user", 1, 10, 1, "u1")
	insert("s1", "assistant", 1.1, 20, 1, "a1")
	insert("s1", "tool", 1.2, 5, 1, "t1")
	insert("s1", "user", 1, 10, 0, "u1") // the same message, copied by compaction
	insert("s2", "user", 5, 7, 1, "u2")
	insert("s2", "assistant", 30, 9, 1, "a2") // the next day
	db.Close()

	days := testAdapter(home, true).Usage(context.Background(), []string{"2026-11-10", "2026-11-11"}, "2026-11-11")
	if len(days) != 2 {
		t.Fatalf("got %d days, want 2", len(days))
	}
	first := days[0]
	// s1 (0.35, all on the first day) and 7 of s2's 16 tokens (actual cost 1.6, preferred to the estimate).
	if first.Cost != 1.05 || days[1].Cost != 0.9 {
		t.Errorf("cost = %v then %v, want 1.05 then 0.9", first.Cost, days[1].Cost)
	}
	if !first.Final || first.Sessions != 2 || first.UserMessages != 2 || first.AssistantMessages != 1 || first.ToolCalls != 1 || first.Tokens != 42 {
		t.Errorf("first day = %+v", first)
	}
	if len(first.Channels) != 2 || first.Channels[0].Channel != "cli" || first.Channels[1].Channel != "telegram" {
		t.Errorf("channels = %+v", first.Channels)
	}
	if days[1].Final || days[1].AssistantMessages != 1 {
		t.Errorf("second day = %+v", days[1])
	}
}

func TestRevisionWhenHomeIsUnderGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	write(t, filepath.Join(home, "config.yaml"), "model: {}\n")
	if testAdapter(home, true).Inventory(context.Background()).Revision != nil {
		t.Fatal("a home that is not a git checkout has no revision")
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-qm", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", home}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	revision := testAdapter(home, true).Inventory(context.Background()).Revision
	if revision == nil || len(revision.Commit) < 40 || revision.Dirty {
		t.Fatalf("revision = %+v", revision)
	}
	write(t, filepath.Join(home, "config.yaml"), "model: {provider: x}\n")
	if !testAdapter(home, true).Inventory(context.Background()).Revision.Dirty {
		t.Error("a changed file must show as dirty")
	}
}
