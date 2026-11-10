package openclaw

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

func TestParseSkillsKeepsOnlyUsableSkillsByName(t *testing.T) {
	output := []byte(`notice: something printed first
{
  "workspaceDir": "/home/me/work",
  "skills": [
    {"name": "weather", "source": "bundled", "eligible": true, "disabled": false, "description": "Tells the weather for PRIVATE-PLACE"},
    {"name": "calendar", "source": "bundled", "eligible": false, "disabled": true},
    {"name": "notes", "source": "workspace", "eligible": false, "disabled": false},
    {"name": "billing", "source": "workspace", "eligible": true, "disabled": false},
    {"name": "billing", "source": "duplicate", "eligible": true, "disabled": false},
    {"name": "/etc/passwd", "source": "workspace", "eligible": true, "disabled": false},
    {"name": "", "source": "workspace", "eligible": true, "disabled": false}
  ]
}`)

	skills, workspace := parseSkills(output)

	want := []adapter.Skill{{Name: "billing", Source: "workspace"}, {Name: "weather", Source: "bundled"}}
	if !reflect.DeepEqual(skills, want) {
		t.Fatalf("got %+v, want %+v", skills, want)
	}
	if workspace != "/home/me/work" {
		t.Fatalf("workspace = %q", workspace)
	}
	sent, _ := json.Marshal(skills)
	if strings.Contains(string(sent), "PRIVATE-PLACE") {
		t.Fatal("a skill's description must not be sent")
	}
}

func TestParseSkillsTellsUnknownFromNone(t *testing.T) {
	for _, output := range []string{"", "command not found", "{broken", `{"other": true}`} {
		if skills, _ := parseSkills([]byte(output)); skills != nil {
			t.Fatalf("output %q: got %+v, want nil for unknown", output, skills)
		}
	}
	skills, _ := parseSkills([]byte(`{"skills": []}`))
	if skills == nil || len(skills) != 0 {
		t.Fatalf("got %+v, want an empty list for a runtime with no skills", skills)
	}
}

func TestConnectionsFromConfigNamesWhatIsSwitchedOnAndNothingInside(t *testing.T) {
	var config map[string]json.RawMessage
	err := json.Unmarshal([]byte(`{
  "channels": {
    "chat": {"enabled": true, "token": "SECRET-TOKEN"},
    "mail": {"token": "SECRET-TOKEN"},
    "pager": {"enabled": false}
  },
  "mcp": {"servers": {"search": {"command": "run", "args": ["--key", "SECRET-KEY"]}}},
  "models": {"providers": {"acme": {"apiKey": "SECRET-KEY"}, "https://not-a-name.example/v1": {}}},
  "auth": {"profiles": {"acme:someone@example.com": {}}}
}`), &config)
	if err != nil {
		t.Fatal(err)
	}

	connections := connectionsFromConfig(config)

	want := []adapter.Connection{
		{Kind: "channel", Name: "chat"},
		{Kind: "channel", Name: "mail"},
		{Kind: "model_provider", Name: "acme"},
		{Kind: "tool_server", Name: "search"},
	}
	if !reflect.DeepEqual(connections, want) {
		t.Fatalf("got %+v, want %+v", connections, want)
	}
	sent, _ := json.Marshal(connections)
	for _, private := range []string{"SECRET", "someone@example.com", "not-a-name"} {
		if strings.Contains(string(sent), private) {
			t.Fatalf("%q must not be sent: %s", private, sent)
		}
	}
}

func TestConnectionsFromConfigWithNothingSetUp(t *testing.T) {
	connections := connectionsFromConfig(map[string]json.RawMessage{"channels": json.RawMessage(`"not an object"`)})
	if connections == nil || len(connections) != 0 {
		t.Fatalf("got %+v, want an empty list", connections)
	}
}

func TestRevisionOfAGitCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if revision := revisionOf(context.Background(), dir); revision != nil {
		t.Fatalf("got %+v for a folder that is not a checkout, want nil", revision)
	}

	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "skill.md"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "first")

	clean := revisionOf(context.Background(), dir)
	if clean == nil || len(clean.Commit) < 40 || clean.Dirty {
		t.Fatalf("got %+v, want a clean commit", clean)
	}

	if err := os.WriteFile(filepath.Join(dir, "skill.md"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed := revisionOf(context.Background(), dir)
	if changed == nil || changed.Commit != clean.Commit || !changed.Dirty {
		t.Fatalf("got %+v, want the same commit marked as changed", changed)
	}
}

func TestRevisionOfRefusesARelativeFolder(t *testing.T) {
	if revision := revisionOf(context.Background(), "."); revision != nil {
		t.Fatalf("got %+v, want nil", revision)
	}
}

func TestCleanNameAcceptsLabelsOnly(t *testing.T) {
	for name, want := range map[string]string{
		"weather":               "weather",
		" My Skill v2 ":         "My Skill v2",
		"a.b_c-d":               "a.b_c-d",
		"../../secret":          "",
		"has\nnewline":          "",
		"user@example.com":      "",
		strings.Repeat("x", 81): "",
	} {
		if got := cleanName(name); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", name, got, want)
		}
	}
}
