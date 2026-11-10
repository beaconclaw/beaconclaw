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

func TestDetectOpenClawPortDefault(t *testing.T) {
	// When no OpenClaw config exists, should return 0 (fallback to default)
	port := detectOpenClawPort()
	// This may or may not find a config depending on the test environment
	// Just ensure it doesn't panic and returns a valid value
	if port < 0 {
		t.Errorf("detectOpenClawPort returned negative: %d", port)
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
