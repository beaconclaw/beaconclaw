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

	"github.com/beaconclaw/beaconclaw/internal/actions"
	"github.com/beaconclaw/beaconclaw/internal/openclaw"
)

const (
	DefaultManagerEndpoint = "wss://api.beaconclaw.ai/ws"
	DefaultOpenClawPort    = openclaw.DefaultPort
	DefaultHeartbeatSec    = 30
	// Two hours of heartbeats are kept through an outage.
	DefaultBufferCapacity = 240

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
	// Inventory: report the names of what the runtime has loaded. On unless
	// switched off.
	Inventory bool
	// Usage: report daily counts of the runtime's activity. On unless switched off.
	Usage bool
	// InventoryAllNames: also send the names of declared credentials the
	// catalogue does not recognise. Off unless switched on, on this machine.
	InventoryAllNames bool
	// Automations: report the runtime's scheduled jobs and their runs. On
	// unless switched off.
	Automations bool
	// Signals: report credential health, pending approvals and pairing
	// requests, backups, updates and the runtime's own audits. On unless
	// switched off.
	Signals bool
	// Actions are the remote actions switched on on this machine. Empty
	// unless someone here switched one on; the manager cannot.
	Actions []string
	// Runtime names the agent runtime to report on: "openclaw" or "hermes".
	// Empty means found automatically.
	Runtime string
	// HermesHome is Hermes's home directory. Empty means $HERMES_HOME, then
	// ~/.hermes, as Hermes itself decides.
	HermesHome string
	// AutomationNames is AutomationNamesShown (the default) or
	// AutomationNamesHidden, which sends "Automation 1", "Automation 2" and so
	// on in place of each scheduled job's name.
	AutomationNames string
}

// Values of the automation_names setting.
const (
	AutomationNamesShown  = "shown"
	AutomationNamesHidden = "hidden"
)

// HideAutomationNames reports whether scheduled jobs' names are replaced
// before they are sent.
func (c *Config) HideAutomationNames() bool {
	return c.AutomationNames == AutomationNamesHidden
}

type fileConfig struct {
	Token        string `json:"token,omitempty"`
	Endpoint     string `json:"endpoint,omitempty"`
	OpenClawPort int    `json:"openclaw_port,omitempty"`
	Channel      string `json:"channel,omitempty"`
	Inventory    *bool  `json:"inventory,omitempty"`
	Usage        *bool  `json:"usage,omitempty"`
	// InventoryAllNames is off unless set to true here.
	InventoryAllNames *bool `json:"inventory_all_names,omitempty"`
	Automations       *bool `json:"automations,omitempty"`
	Signals           *bool `json:"signals,omitempty"`
	// Actions are the remote actions switched on here. There is no
	// environment variable for this: it is set only by the actions command.
	Actions []string `json:"actions,omitempty"`
	// Runtime is "openclaw" or "hermes"; empty means found automatically.
	Runtime    string `json:"runtime,omitempty"`
	HermesHome string `json:"hermes_home,omitempty"`
	// AutomationNames is "shown" or "hidden"; empty means shown.
	AutomationNames string `json:"automation_names,omitempty"`
}

func Load() (*Config, error) {
	cfg := &Config{
		Endpoint:          DefaultManagerEndpoint,
		OpenClawPort:      DefaultOpenClawPort,
		HeartbeatInterval: DefaultHeartbeatSec,
		BufferCapacity:    DefaultBufferCapacity,
		Channel:           ChannelStable,
		Inventory:         true,
		Usage:             true,
		Automations:       true,
		Signals:           true,
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
		if fc.Inventory != nil {
			cfg.Inventory = *fc.Inventory
		}
		if fc.InventoryAllNames != nil {
			cfg.InventoryAllNames = *fc.InventoryAllNames
		}
		if fc.Usage != nil {
			cfg.Usage = *fc.Usage
		}
		if fc.Automations != nil {
			cfg.Automations = *fc.Automations
		}
		if fc.Signals != nil {
			cfg.Signals = *fc.Signals
		}
		cfg.Actions = actions.Normalize(fc.Actions)
		cfg.Runtime = fc.Runtime
		cfg.HermesHome = fc.HermesHome
		cfg.AutomationNames = fc.AutomationNames
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

	if v := os.Getenv("BEACONCLAW_RUNTIME"); v != "" {
		cfg.Runtime = v
	}
	if v := os.Getenv("BEACONCLAW_HERMES_HOME"); v != "" {
		cfg.HermesHome = v
	}

	if v := os.Getenv("BEACONCLAW_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("BEACONCLAW_ENDPOINT"); v != "" {
		cfg.Endpoint = v
	}
	if v := os.Getenv("BEACONCLAW_INVENTORY"); v != "" {
		if on, err := strconv.ParseBool(v); err == nil {
			cfg.Inventory = on
		}
	}
	if v := os.Getenv("BEACONCLAW_INVENTORY_ALL_NAMES"); v != "" {
		if on, err := strconv.ParseBool(v); err == nil {
			cfg.InventoryAllNames = on
		}
	}
	if v := os.Getenv("BEACONCLAW_USAGE"); v != "" {
		if on, err := strconv.ParseBool(v); err == nil {
			cfg.Usage = on
		}
	}
	if v := os.Getenv("BEACONCLAW_AUTOMATIONS"); v != "" {
		if on, err := strconv.ParseBool(v); err == nil {
			cfg.Automations = on
		}
	}
	if v := os.Getenv("BEACONCLAW_SIGNALS"); v != "" {
		if on, err := strconv.ParseBool(v); err == nil {
			cfg.Signals = on
		}
	}
	if v := os.Getenv("BEACONCLAW_AUTOMATION_NAMES"); v != "" {
		cfg.AutomationNames = v
	}
	cfg.AutomationNames = strings.ToLower(strings.TrimSpace(cfg.AutomationNames))
	if cfg.AutomationNames == "" {
		cfg.AutomationNames = AutomationNamesShown
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
	if cfg.Runtime != "" && cfg.Runtime != "openclaw" && cfg.Runtime != "hermes" {
		return fmt.Errorf("unknown runtime %q: use openclaw or hermes", cfg.Runtime)
	}
	// A mistyped value stops the agent rather than send names someone meant
	// to hide.
	switch cfg.AutomationNames {
	case "", AutomationNamesShown, AutomationNamesHidden:
	default:
		return fmt.Errorf("unknown automation_names %q: use %s or %s", cfg.AutomationNames, AutomationNamesShown, AutomationNamesHidden)
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

// OutboxPath is where this profile keeps heartbeats that are waiting to be
// acknowledged.
func OutboxPath() string {
	return filepath.Join(stateDirectory(), "outbox.json")
}

// AutomationsCursorPath is where this profile records which scheduled-job runs
// it has already reported, so a restart does not report them again.
func AutomationsCursorPath() string {
	return filepath.Join(stateDirectory(), "automations.json")
}

// SavePrivate writes a file in this profile's folder that only this user may
// read.
func SavePrivate(path string, data []byte) error {
	return writePrivate(path, data)
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

// LoadActions returns the remote actions switched on in this profile.
func LoadActions() []string {
	if fc, err := loadFileConfig(); err == nil {
		return actions.Normalize(fc.Actions)
	}
	return nil
}

// SaveActions records which remote actions are switched on in this profile,
// keeping other settings. Unknown names are dropped.
func SaveActions(names []string) error {
	fc, _ := loadFileConfig()
	if fc == nil {
		fc = &fileConfig{}
	}
	fc.Actions = actions.Normalize(names)
	return writeFileConfig(fc)
}

// ActionsLogPath is where this profile records every remote action it was
// asked for, so the machine's owner can check without asking the manager.
func ActionsLogPath() string {
	return filepath.Join(stateDirectory(), "actions.log")
}

// SaveRuntime records which runtime this profile reports on, keeping other
// settings. Once saved, the choice no longer depends on what is installed.
func SaveRuntime(runtime string) error {
	if runtime != "openclaw" && runtime != "hermes" {
		return fmt.Errorf("unknown runtime %q: use openclaw or hermes", runtime)
	}
	fc, _ := loadFileConfig()
	if fc == nil {
		fc = &fileConfig{}
	}
	fc.Runtime = runtime
	return writeFileConfig(fc)
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
