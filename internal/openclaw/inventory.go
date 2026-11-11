package openclaw

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

const (
	// Listing skills starts the OpenClaw command, which can take several seconds.
	skillsTimeout = 60 * time.Second
	gitTimeout    = 10 * time.Second
	// More than this many of anything is not a real list.
	maxInventoryItems = 500
	maxCommandOutput  = 4 << 20
)

// Connection kinds this adapter reports.
const (
	kindChannel       = "channel"
	kindToolServer    = "tool_server"
	kindModelProvider = "model_provider"
)

// A name is a short label. Anything else (a path, a sentence, an address) is
// left out rather than sent.
var namePattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._ -]{0,79}$`)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

func cleanName(name string) string {
	name = strings.TrimSpace(name)
	if !namePattern.MatchString(name) {
		return ""
	}
	return name
}

// Inventory lists the skills OpenClaw has loaded, the systems it is set up to
// reach, the names of the credentials its skills declare and have, and the
// commit of its workspace. Names only: never a skill's contents, a setting's
// value or a credential. It asks OpenClaw; it never opens an environment file.
func (a *Adapter) Inventory(ctx context.Context) adapter.Inventory {
	inventory := adapter.Inventory{}

	workspace := ""
	if output, err := runSkillsList(ctx); err == nil {
		inventory.Skills, workspace = parseSkills(output)
		inventory.Credentials = parseCredentials(output)
	}

	config := readConfig()
	if config != nil {
		inventory.Connections = connectionsFromConfig(config)
		if workspace == "" {
			workspace = workspaceFromConfig(config)
		}
	}

	if workspace != "" {
		inventory.Revision = revisionOf(ctx, workspace)
	}
	return inventory
}

// runSkillsList asks OpenClaw itself which skills are loaded, so the answer
// follows OpenClaw's own rules for what is enabled and usable.
func runSkillsList(ctx context.Context) ([]byte, error) {
	var lastErr error = exec.ErrNotFound
	for _, candidate := range commandCandidates() {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, skillsTimeout)
		cmd := exec.CommandContext(ctx, candidate, "skills", "list", "--json")
		cmd.Env = commandEnv()
		var stdout bytes.Buffer
		cmd.Stdout = &limitedWriter{w: &stdout, n: maxCommandOutput}
		err := cmd.Run()
		cancel()
		if err == nil {
			return stdout.Bytes(), nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// commandEnv is this process's environment, with the folder of the node that
// runs the gateway added to the lookup path. A service often has a shorter
// path than a login shell, and the OpenClaw command needs to find node.
func commandEnv() []string {
	env := os.Environ()
	nodeDir := ""
	for _, command := range runningCommands() {
		if gatewayPackageRoot(command) != "" && len(command) > 0 && filepath.IsAbs(command[0]) {
			nodeDir = filepath.Dir(command[0])
			break
		}
	}
	if nodeDir == "" {
		return env
	}
	for i, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			env[i] = entry + string(os.PathListSeparator) + nodeDir
			return env
		}
	}
	return append(env, "PATH="+nodeDir)
}

// parseSkills reads the output of "openclaw skills list --json". It keeps the
// skills OpenClaw says are usable now, and returns the workspace folder the
// output names. A nil list means the output could not be read.
func parseSkills(output []byte) ([]adapter.Skill, string) {
	// The command may print notices before the JSON.
	start := bytes.IndexByte(output, '{')
	if start < 0 {
		return nil, ""
	}
	var listing struct {
		WorkspaceDir string `json:"workspaceDir"`
		Skills       []struct {
			Name     string `json:"name"`
			Source   string `json:"source"`
			Eligible bool   `json:"eligible"`
			Disabled bool   `json:"disabled"`
		} `json:"skills"`
	}
	if err := json.NewDecoder(bytes.NewReader(output[start:])).Decode(&listing); err != nil || listing.Skills == nil {
		return nil, ""
	}

	skills := []adapter.Skill{}
	seen := map[string]bool{}
	for _, skill := range listing.Skills {
		name := cleanName(skill.Name)
		if name == "" || seen[name] || !skill.Eligible || skill.Disabled {
			continue
		}
		seen[name] = true
		skills = append(skills, adapter.Skill{Name: name, Source: cleanName(skill.Source)})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	if len(skills) > maxInventoryItems {
		skills = skills[:maxInventoryItems]
	}
	return skills, listing.WorkspaceDir
}

// A credential's name is an environment-variable name. Anything else is not
// kept.
var credentialPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,79}$`)

// parseCredentials reads, from the same "openclaw skills list --json" output,
// the names of the credentials that usable skills declare and that OpenClaw
// says are in place. OpenClaw makes that judgement: a name is in place when a
// skill that is eligible and not disabled declares it and does not list it as
// missing. This never looks at the environment or at any file of secrets, so
// a variable no skill declares is never seen at all. A nil list means the
// output could not be read.
func parseCredentials(output []byte) []string {
	start := bytes.IndexByte(output, '{')
	if start < 0 {
		return nil
	}
	var listing struct {
		Skills []struct {
			Eligible     bool   `json:"eligible"`
			Disabled     bool   `json:"disabled"`
			PrimaryEnv   string `json:"primaryEnv"`
			Requirements struct {
				Env []string `json:"env"`
			} `json:"requirements"`
			Missing struct {
				Env []string `json:"env"`
			} `json:"missing"`
		} `json:"skills"`
	}
	if err := json.NewDecoder(bytes.NewReader(output[start:])).Decode(&listing); err != nil || listing.Skills == nil {
		return nil
	}

	names := []string{}
	seen := map[string]bool{}
	for _, skill := range listing.Skills {
		if !skill.Eligible || skill.Disabled {
			continue
		}
		missing := map[string]bool{}
		for _, name := range skill.Missing.Env {
			missing[name] = true
		}
		for _, name := range append([]string{skill.PrimaryEnv}, skill.Requirements.Env...) {
			name = strings.TrimSpace(name)
			if name == "" || missing[name] || seen[name] || !credentialPattern.MatchString(name) {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > maxInventoryItems {
		names = names[:maxInventoryItems]
	}
	return names
}

// readConfig returns the top level of OpenClaw's config, or nil when there is
// none that can be read.
func readConfig() map[string]json.RawMessage {
	for _, path := range configCandidates() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err == nil {
			return raw
		}
	}
	return nil
}

// connectionsFromConfig names the chat channels, tool servers and model
// providers in OpenClaw's config. It reads the names of the entries and
// whether each is switched on, and nothing inside them.
func connectionsFromConfig(config map[string]json.RawMessage) []adapter.Connection {
	connections := []adapter.Connection{}
	add := func(kind string, section json.RawMessage) {
		var entries map[string]json.RawMessage
		if json.Unmarshal(section, &entries) != nil {
			return
		}
		for key, entry := range entries {
			name := cleanName(key)
			if name == "" || switchedOff(entry) {
				continue
			}
			connections = append(connections, adapter.Connection{Kind: kind, Name: name})
		}
	}

	add(kindChannel, config["channels"])

	var mcp struct {
		Servers json.RawMessage `json:"servers"`
	}
	if json.Unmarshal(config["mcp"], &mcp) == nil {
		add(kindToolServer, mcp.Servers)
	}

	var models struct {
		Providers json.RawMessage `json:"providers"`
	}
	if json.Unmarshal(config["models"], &models) == nil {
		add(kindModelProvider, models.Providers)
	}

	sort.Slice(connections, func(i, j int) bool {
		if connections[i].Kind != connections[j].Kind {
			return connections[i].Kind < connections[j].Kind
		}
		return connections[i].Name < connections[j].Name
	})
	if len(connections) > maxInventoryItems {
		connections = connections[:maxInventoryItems]
	}
	return connections
}

// switchedOff reports whether a config entry says "enabled": false.
func switchedOff(entry json.RawMessage) bool {
	var flags struct {
		Enabled *bool `json:"enabled"`
	}
	if json.Unmarshal(entry, &flags) != nil {
		return false
	}
	return flags.Enabled != nil && !*flags.Enabled
}

func workspaceFromConfig(config map[string]json.RawMessage) string {
	var agents struct {
		Defaults struct {
			Workspace string `json:"workspace"`
		} `json:"defaults"`
	}
	if json.Unmarshal(config["agents"], &agents) != nil {
		return ""
	}
	return agents.Defaults.Workspace
}

// revisionOf reads the commit a folder is at, and whether it has changes
// beyond that commit. It returns nil when the folder is not a git checkout or
// git is not installed. It reads nothing about the repository's origin.
func revisionOf(ctx context.Context, dir string) *adapter.Revision {
	if !filepath.IsAbs(dir) {
		return nil
	}
	commit, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return nil
	}
	hash := strings.TrimSpace(string(commit))
	if !commitPattern.MatchString(hash) {
		return nil
	}
	revision := &adapter.Revision{Commit: hash}
	if changes, err := git(ctx, dir, "status", "--porcelain"); err == nil {
		revision.Dirty = len(bytes.TrimSpace(changes)) > 0
	}
	return revision
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	// Never stop to ask for anything, and take no lock a running agent might want.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var stdout bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, n: maxCommandOutput}
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// limitedWriter keeps the first n bytes and quietly drops the rest, so a
// command that prints without end cannot fill memory.
type limitedWriter struct {
	w *bytes.Buffer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if room := l.n - l.w.Len(); room > 0 {
		if len(p) > room {
			l.w.Write(p[:room])
		} else {
			l.w.Write(p)
		}
	}
	return len(p), nil
}
