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
