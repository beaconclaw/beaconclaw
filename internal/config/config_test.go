package config

import "testing"

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				Token:        "bc_test123",
				Endpoint:     "wss://api.beaconclaw.ai/ws",
				OpenClawPort: 18789,
				InstanceID:   "test-id",
			},
		},
		{
			name: "missing token",
			cfg: Config{
				Endpoint:     "wss://api.beaconclaw.ai/ws",
				OpenClawPort: 18789,
			},
			wantErr: true,
		},
		{
			name: "token missing prefix",
			cfg: Config{
				Token:        "bad_token",
				Endpoint:     "wss://api.beaconclaw.ai/ws",
				OpenClawPort: 18789,
			},
			wantErr: true,
		},
		{
			name: "missing endpoint",
			cfg: Config{
				Token:        "bc_test123",
				OpenClawPort: 18789,
			},
			wantErr: true,
		},
		{
			name: "unencrypted endpoint on another host",
			cfg: Config{
				Token:        "bc_test123",
				Endpoint:     "ws://manager.example.com/ws",
				OpenClawPort: 18789,
			},
			wantErr: true,
		},
		{
			name: "unencrypted endpoint on this machine",
			cfg: Config{
				Token:        "bc_test123",
				Endpoint:     "ws://127.0.0.1:3001/ws",
				OpenClawPort: 18789,
			},
		},
		{
			name: "endpoint that is not a WebSocket address",
			cfg: Config{
				Token:        "bc_test123",
				Endpoint:     "https://api.beaconclaw.ai/ws",
				OpenClawPort: 18789,
			},
			wantErr: true,
		},
		{
			name: "invalid port",
			cfg: Config{
				Token:        "bc_test123",
				Endpoint:     "wss://api.beaconclaw.ai/ws",
				OpenClawPort: 0,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(&tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSaveEndpointAndChannelKeepOtherSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BEACONCLAW_TOKEN", "")
	t.Setenv("BEACONCLAW_ENDPOINT", "")

	if err := SaveToken("bc_test123"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "api.example.com", "https://api.example.com/ws", "wss://"} {
		if err := SaveEndpoint(bad); err == nil {
			t.Errorf("SaveEndpoint(%q) should fail", bad)
		}
	}
	if err := SaveEndpoint("wss://api.example.com/ws"); err != nil {
		t.Fatal(err)
	}
	if err := SaveChannel("nightly"); err == nil {
		t.Error("SaveChannel(nightly) should fail")
	}
	if err := SaveChannel(ChannelBeta); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "bc_test123" || cfg.Endpoint != "wss://api.example.com/ws" || cfg.Channel != ChannelBeta {
		t.Errorf("config = %+v", cfg)
	}
}

func TestAutomationNames(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BEACONCLAW_TOKEN", "")
	t.Setenv("BEACONCLAW_ENDPOINT", "")

	t.Setenv("BEACONCLAW_AUTOMATION_NAMES", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutomationNames != AutomationNamesShown || cfg.HideAutomationNames() {
		t.Errorf("default = %q, want shown", cfg.AutomationNames)
	}

	t.Setenv("BEACONCLAW_AUTOMATION_NAMES", " Hidden ")
	if cfg, err = Load(); err != nil {
		t.Fatal(err)
	}
	if !cfg.HideAutomationNames() {
		t.Errorf("BEACONCLAW_AUTOMATION_NAMES=Hidden gave %q", cfg.AutomationNames)
	}

	valid := Config{Token: "bc_test123", Endpoint: "wss://api.beaconclaw.ai/ws", OpenClawPort: 18789}
	for value, wantErr := range map[string]bool{"": false, "shown": false, "hidden": false, "hide": true, "off": true} {
		cfg := valid
		cfg.AutomationNames = value
		if err := Validate(&cfg); (err != nil) != wantErr {
			t.Errorf("Validate(automation_names=%q) error = %v, want error %v", value, err, wantErr)
		}
	}
}

func TestAutomationsAndSignalsSwitches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BEACONCLAW_TOKEN", "")
	t.Setenv("BEACONCLAW_ENDPOINT", "")
	t.Setenv("BEACONCLAW_AUTOMATIONS", "")
	t.Setenv("BEACONCLAW_SIGNALS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Automations || !cfg.Signals {
		t.Errorf("defaults: automations=%v signals=%v, want both on", cfg.Automations, cfg.Signals)
	}

	// The config file switches them off.
	if err := writeFileConfig(&fileConfig{Automations: boolPtr(false), Signals: boolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	if cfg, err = Load(); err != nil {
		t.Fatal(err)
	}
	if cfg.Automations || cfg.Signals {
		t.Errorf("file off: automations=%v signals=%v", cfg.Automations, cfg.Signals)
	}

	// The environment overrides the file; a value that is not a boolean is
	// ignored, as for the other switches.
	t.Setenv("BEACONCLAW_AUTOMATIONS", "true")
	t.Setenv("BEACONCLAW_SIGNALS", "maybe")
	if cfg, err = Load(); err != nil {
		t.Fatal(err)
	}
	if !cfg.Automations || cfg.Signals {
		t.Errorf("env: automations=%v signals=%v", cfg.Automations, cfg.Signals)
	}

	if err := writeFileConfig(&fileConfig{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEACONCLAW_AUTOMATIONS", "0")
	t.Setenv("BEACONCLAW_SIGNALS", "false")
	if cfg, err = Load(); err != nil {
		t.Fatal(err)
	}
	if cfg.Automations || cfg.Signals {
		t.Errorf("env off: automations=%v signals=%v", cfg.Automations, cfg.Signals)
	}
}

func boolPtr(b bool) *bool { return &b }
