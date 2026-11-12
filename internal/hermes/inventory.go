package hermes

import (
	"bufio"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

// Connection kinds, the same words the OpenClaw adapter uses.
const (
	kindChannel       = "channel"
	kindToolServer    = "tool_server"
	kindModelProvider = "model_provider"
)

// maxSkillDepth bounds the walk of the skills folder: skills sit one or two
// folders down (category/name/SKILL.md).
const maxSkillDepth = 3

// configNames is the part of Hermes's config.yaml read here: names only.
// Values under these keys (addresses, keys) are never kept.
type configNames struct {
	Model struct {
		Provider string `yaml:"provider"`
	} `yaml:"model"`
	Providers  map[string]yaml.Node `yaml:"providers"`
	MCPServers map[string]yaml.Node `yaml:"mcp_servers"`
	Platforms  map[string]struct {
		Enabled *bool `yaml:"enabled"`
	} `yaml:"platforms"`
}

// Inventory lists, by name, the skills on disk and the platforms, tool servers
// and model providers Hermes is set up to use. It does not report credentials:
// Hermes keeps them in its environment file, which is never opened.
func (a *Adapter) Inventory(ctx context.Context) adapter.Inventory {
	return adapter.Inventory{
		Skills:      a.skills(),
		Connections: a.connections(),
	}
}

// skills lists every folder holding a SKILL.md. Skills Hermes ships are marked
// "hermes-bundled", the rest "local".
func (a *Adapter) skills() []adapter.Skill {
	root := filepath.Join(a.home, "skills")
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	bundled := bundledSkills(filepath.Join(root, ".bundled_manifest"))
	skills := []adapter.Skill{}
	seen := map[string]bool{}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if entry.IsDir() && rel != "." && (strings.HasPrefix(entry.Name(), ".") || strings.Count(rel, string(filepath.Separator)) >= maxSkillDepth) {
			return filepath.SkipDir
		}
		if entry.IsDir() || entry.Name() != "SKILL.md" {
			return nil
		}
		name := filepath.Base(filepath.Dir(path))
		if seen[name] {
			return nil
		}
		seen[name] = true
		source := "local"
		if bundled[name] {
			source = "hermes-bundled"
		}
		skills = append(skills, adapter.Skill{Name: name, Source: source})
		return nil
	})
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills
}

// bundledSkills reads the names in Hermes's manifest of the skills it ships
// ("name:checksum" per line).
func bundledSkills(path string) map[string]bool {
	names := map[string]bool{}
	file, err := os.Open(path)
	if err != nil {
		return names
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name, _, _ := strings.Cut(strings.TrimSpace(scanner.Text()), ":")
		if name != "" {
			names[name] = true
		}
	}
	return names
}

// connections lists the platforms the gateway runs (from its own state file,
// falling back to the platforms switched on in config.yaml), the tool servers
// and the model providers. The API server Hermes runs for itself is left out:
// it is how Hermes is reached locally, not something it connects to.
func (a *Adapter) connections() []adapter.Connection {
	connections := []adapter.Connection{}
	seen := map[string]bool{}
	add := func(kind, name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[kind+"/"+name] {
			return
		}
		seen[kind+"/"+name] = true
		connections = append(connections, adapter.Connection{Kind: kind, Name: name})
	}

	if data, err := os.ReadFile(filepath.Join(a.home, stateFile)); err == nil {
		var state GatewayState
		if json.Unmarshal(data, &state) == nil {
			for name := range state.Platforms {
				if name != "api_server" {
					add(kindChannel, platformName(name))
				}
			}
		}
	}

	if data, err := os.ReadFile(filepath.Join(a.home, "config.yaml")); err == nil {
		var config configNames
		if yaml.Unmarshal(data, &config) == nil {
			for name, platform := range config.Platforms {
				if platform.Enabled != nil && *platform.Enabled && name != "api_server" {
					add(kindChannel, name)
				}
			}
			for name := range config.MCPServers {
				add(kindToolServer, name)
			}
			if provider := config.Model.Provider; provider != "" && provider != "auto" {
				add(kindModelProvider, provider)
			}
			for name := range config.Providers {
				add(kindModelProvider, name)
			}
		}
	}

	sort.Slice(connections, func(i, j int) bool {
		if connections[i].Kind != connections[j].Kind {
			return connections[i].Kind < connections[j].Kind
		}
		return connections[i].Name < connections[j].Name
	})
	return connections
}

// platformName drops a profile prefix ("profile:telegram" becomes "telegram").
func platformName(name string) string {
	if _, after, found := strings.Cut(name, ":"); found {
		return after
	}
	return name
}
