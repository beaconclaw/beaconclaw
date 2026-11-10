package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/beaconclaw/beaconclaw/internal/profile"
)

const (
	DefaultManagerEndpoint = "wss://api.beaconclaw.ai/ws"
	DefaultOpenClawPort    = 18789
	DefaultHeartbeatSec    = 30
	DefaultBufferCapacity  = 100

	// Release tracks. Stable is the default; beta also includes pre-releases.
	ChannelStable = "stable"
	ChannelBeta   = "beta"

	instanceIDFile   = "instance-id"
	configFile       = "config.json"
	openclawStateDir = ".openclaw"
	openclawConfig   = "openclaw.json"
)

type Config struct {
	Token             string
	Endpoint          string
	OpenClawPort      int
	InstanceID        string
	HeartbeatInterval int
	BufferCapacity    int
	// Channel is the release track this profile updates from.
	Channel string
}

type fileConfig struct {
	Token        string `json:"token,omitempty"`
	Endpoint     string `json:"endpoint,omitempty"`
	OpenClawPort int    `json:"openclaw_port,omitempty"`
	Channel      string `json:"channel,omitempty"`
}

func Load() (*Config, error) {
	cfg := &Config{
		Endpoint:          DefaultManagerEndpoint,
		OpenClawPort:      DefaultOpenClawPort,
		HeartbeatInterval: DefaultHeartbeatSec,
		BufferCapacity:    DefaultBufferCapacity,
		Channel:           ChannelStable,
	}

	// Load from config file (lowest priority)
	if fc, err := loadFileConfig(); err == nil {
		if fc.Token != "" {
			cfg.Token = fc.Token
		}
		if fc.Endpoint != "" {
			cfg.Endpoint = fc.Endpoint
		}
		if fc.OpenClawPort > 0 {
			cfg.OpenClawPort = fc.OpenClawPort
		}
		if ValidChannel(fc.Channel) {
			cfg.Channel = fc.Channel
		}
	}

	// Auto-detect OpenClaw port from its config
	if port := detectOpenClawPort(); port > 0 {
		cfg.OpenClawPort = port
	}

	// Environment variables override config file
	if v := os.Getenv("OPENCLAW_GATEWAY_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil && port > 0 {
			cfg.OpenClawPort = port
		}
	}

	// BeaconClaw-specific overrides (highest priority for OpenClaw port)
	if v := os.Getenv("BEACONCLAW_OPENCLAW_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil && port > 0 {
			cfg.OpenClawPort = port
		}
	}

	if v := os.Getenv("BEACONCLAW_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("BEACONCLAW_ENDPOINT"); v != "" {
		cfg.Endpoint = v
	}

	// Load or generate instance ID
	id, err := loadOrCreateInstanceID()
	if err != nil {
		return nil, fmt.Errorf("agent ID: %w", err)
	}
	cfg.InstanceID = id

	return cfg, nil
}

func Validate(cfg *Config) error {
	if cfg.Token == "" {
		return fmt.Errorf("agent token required: run 'beaconclaw auth --token <token>' or set BEACONCLAW_TOKEN")
	}
	if !strings.HasPrefix(cfg.Token, "bc_") {
		return fmt.Errorf("agent token must start with 'bc_' prefix")
	}
	if cfg.Endpoint == "" {
		return fmt.Errorf("manager endpoint required")
	}
	if cfg.OpenClawPort < 1 || cfg.OpenClawPort > 65535 {
		return fmt.Errorf("invalid OpenClaw port: %d", cfg.OpenClawPort)
	}
	return nil
}

func stateDirectory() string {
	return profile.StateDir()
}

// SaveEndpoint records the manager address this profile reports to, keeping
// other settings.
func SaveEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" {
		return fmt.Errorf("invalid endpoint %q: expected a WebSocket address such as %s", endpoint, DefaultManagerEndpoint)
	}
	fc, _ := loadFileConfig()
	if fc == nil {
		fc = &fileConfig{}
	}
	fc.Endpoint = endpoint
	return writeFileConfig(fc)
}

// ValidChannel reports whether name is a known release track.
func ValidChannel(name string) bool {
	return name == ChannelStable || name == ChannelBeta
}

// LoadChannel returns this profile's release track without creating any
// state on disk. It defaults to stable.
func LoadChannel() string {
	if fc, err := loadFileConfig(); err == nil && ValidChannel(fc.Channel) {
		return fc.Channel
	}
	return ChannelStable
}

// SaveChannel records this profile's release track, keeping other settings.
func SaveChannel(channel string) error {
	if !ValidChannel(channel) {
		return fmt.Errorf("unknown channel %q: use %s or %s", channel, ChannelStable, ChannelBeta)
	}
	fc, _ := loadFileConfig()
	if fc == nil {
		fc = &fileConfig{}
	}
	fc.Channel = channel
	return writeFileConfig(fc)
}

func writeFileConfig(fc *fileConfig) error {
	if err := os.MkdirAll(stateDirectory(), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(configFilePath(), append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func configFilePath() string {
	return filepath.Join(stateDirectory(), configFile)
}

func SaveToken(token string) error {
	if !strings.HasPrefix(token, "bc_") {
		return fmt.Errorf("agent token must start with 'bc_' prefix")
	}

	// Load existing config to preserve other fields
	fc, _ := loadFileConfig()
	if fc == nil {
		fc = &fileConfig{}
	}
	fc.Token = token

	return writeFileConfig(fc)
}

func loadFileConfig() (*fileConfig, error) {
	data, err := os.ReadFile(configFilePath())
	if err != nil {
		return nil, err
	}
	var fc fileConfig
	if err := json.Unmarshal(data, &fc); err != nil {
		return nil, err
	}
	return &fc, nil
}

func detectOpenClawPort() int {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0
	}

	// Try ~/.openclaw/openclaw.json
	candidates := []string{
		filepath.Join(home, openclawStateDir, openclawConfig),
	}

	// Also check legacy dirs
	for _, legacy := range []string{".clawdbot", ".moldbot"} {
		candidates = append(candidates, filepath.Join(home, legacy, openclawConfig))
		candidates = append(candidates, filepath.Join(home, legacy, "clawdbot.json"))
	}

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}
		gatewayRaw, ok := raw["gateway"]
		if !ok {
			continue
		}
		var gateway struct {
			Port int `json:"port"`
		}
		if err := json.Unmarshal(gatewayRaw, &gateway); err != nil {
			continue
		}
		if gateway.Port > 0 {
			return gateway.Port
		}
	}

	return 0
}

func loadOrCreateInstanceID() (string, error) {
	dir := stateDirectory()
	path := filepath.Join(dir, instanceIDFile)

	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	}

	// Generate new ID
	id := uuid.New().String()

	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create state dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0600); err != nil {
		return "", fmt.Errorf("write agent ID: %w", err)
	}

	return id, nil
}
