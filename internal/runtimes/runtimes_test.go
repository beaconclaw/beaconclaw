package runtimes

import "testing"

func TestChoose(t *testing.T) {
	cases := []struct {
		name                   string
		configured             string
		hasOpenClaw, hasHermes bool
		want                   Choice
	}{
		{"nothing installed: OpenClaw, as before", "", false, false, Choice{Name: OpenClaw}},
		{"only OpenClaw", "", true, false, Choice{Name: OpenClaw}},
		{"only Hermes", "", false, true, Choice{Name: Hermes}},
		{"both, nothing set: OpenClaw, and say so", "", true, true, Choice{Name: OpenClaw, Both: true}},
		{"both, Hermes set", Hermes, true, true, Choice{Name: Hermes, Configured: true}},
		{"set to OpenClaw, only Hermes installed: stays OpenClaw so a missing runtime shows", OpenClaw, false, true, Choice{Name: OpenClaw, Configured: true}},
		{"an unknown setting is ignored", "something", false, true, Choice{Name: Hermes}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := choose(c.configured, c.hasOpenClaw, c.hasHermes); got != c.want {
				t.Errorf("choose(%q, %v, %v) = %+v, want %+v", c.configured, c.hasOpenClaw, c.hasHermes, got, c.want)
			}
		})
	}
}
