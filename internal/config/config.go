package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const (
	DefaultManagerEndpoint = "wss://api.beaconclaw.ai/ws"
	DefaultOpenClawPort    = 18789
	DefaultHeartbeatSec    = 30
	DefaultBufferCapacity  = 100

	stateDir       = ".beaconclaw"
	instanceIDFile = "instance-id"
	configFile     = "config.json"
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
}

type fileConfig struct {
	Token        string `json:"token,omitempty"`
	Endpoint     string `json:"endpoint,omitempty"`
	OpenClawPort int    `json:"openclaw_port,omitempty"`
}

func Load() (*Config, error) {
	cfg := &Config{
		Endpoint:          DefaultManagerEndpoint,
		OpenClawPort:      DefaultOpenClawPort,
		HeartbeatInterval: DefaultHeartbeatSec,
		BufferCapacity:    DefaultBufferCapacity,
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
		return nil, fmt.Errorf("instance ID: %w", err)
	}
	cfg.InstanceID = id

	return cfg, nil
}

func Validate(cfg *Config) error {
	if cfg.Token == "" {
		return fmt.Errorf("fleet token required: run 'beaconclaw auth --token <token>' or set BEACONCLAW_TOKEN")
	}
	if !strings.HasPrefix(cfg.Token, "bc_") {
		return fmt.Errorf("fleet token must start with 'bc_' prefix")
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
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", stateDir)
	}
	return filepath.Join(home, stateDir)
}

func configFilePath() string {
	return filepath.Join(stateDirectory(), configFile)
}

func SaveToken(token string) error {
	if !strings.HasPrefix(token, "bc_") {
		return fmt.Errorf("fleet token must start with 'bc_' prefix")
	}

	dir := stateDirectory()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Load existing config to preserve other fields
	fc, _ := loadFileConfig()
	if fc == nil {
		fc = &fileConfig{}
	}
	fc.Token = token

	data, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(configFilePath(), append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
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
		return "", fmt.Errorf("write instance ID: %w", err)
	}

	return id, nil
}
