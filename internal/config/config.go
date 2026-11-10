package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/beaconclaw/beaconclaw/internal/profile"

	"github.com/beaconclaw/beaconclaw/internal/openclaw"
)

const (
	DefaultManagerEndpoint = "wss://api.beaconclaw.ai/ws"
	DefaultOpenClawPort    = openclaw.DefaultPort
	DefaultHeartbeatSec    = 30
	DefaultBufferCapacity  = 100

	// Release tracks. Stable is the default; beta also includes pre-releases.
	ChannelStable = "stable"
	ChannelBeta   = "beta"

	instanceIDFile = "instance-id"
	configFile     = "config.json"
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

	// Ask the runtime's adapter where the runtime listens.
	if port := openclaw.DetectPort(); port > 0 {
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
	if err := checkEndpoint(cfg.Endpoint); err != nil {
		return err
	}
	if cfg.OpenClawPort < 1 || cfg.OpenClawPort > 65535 {
		return fmt.Errorf("invalid OpenClaw port: %d", cfg.OpenClawPort)
	}
	return nil
}

// checkEndpoint accepts an encrypted WebSocket address. An unencrypted one
// would send the agent token in the clear, so it is allowed only for a manager
// on this same machine.
func checkEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "wss" && u.Scheme != "ws") {
		return fmt.Errorf("invalid endpoint %q: expected a WebSocket address such as %s", endpoint, DefaultManagerEndpoint)
	}
	if u.Scheme == "ws" && !isLoopback(u.Hostname()) {
		return fmt.Errorf("endpoint %q is not encrypted: use wss:// (ws:// is accepted only for a manager on this machine)", endpoint)
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// writePrivate writes a file that only this user may read, in a folder that
// only this user may enter. The modes are applied even when the file or
// folder already existed with looser ones.
func writePrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

func stateDirectory() string {
	return profile.StateDir()
}

// SaveEndpoint records the manager address this profile reports to, keeping
// other settings.
func SaveEndpoint(endpoint string) error {
	if err := checkEndpoint(endpoint); err != nil {
		return err
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
	data, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := writePrivate(configFilePath(), append(data, '\n')); err != nil {
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

	if err := writePrivate(path, []byte(id+"\n")); err != nil {
		return "", fmt.Errorf("write agent ID: %w", err)
	}

	return id, nil
}
