package catalogue

import (
	"reflect"
	"strings"
	"testing"
)

func TestSystem(t *testing.T) {
	cases := map[string]string{
		"JIRA_API_TOKEN":        "Jira",
		"jira_url":              "Jira",
		"GH_TOKEN":              "GitHub",
		"GITHUB_TOKEN":          "GitHub",
		"VENICE_API_KEY":        "Venice",
		"SUPADATA_API_KEYS":     "Supadata",
		"GOOGLE_PLACES_API_KEY": "Google Maps",
		"GOOGLE_CLIENT_ID":      "Google",
		"OPENAI_API_KEY":        "OpenAI",
		"BEACONCLAW_API_KEY":    "BeaconClaw",
	}
	for credential, want := range cases {
		if got, ok := System(credential); !ok || got != want {
			t.Errorf("System(%q) = %q, %v; want %q", credential, got, ok, want)
		}
	}
	for _, unknown := range []string{"BASE_WALLET_PRIVATE_KEY", "GHOST_API_KEY", "MY_INTERNAL_DIR", ""} {
		if got, ok := System(unknown); ok {
			t.Errorf("System(%q) = %q; want it not to be recognised", unknown, got)
		}
	}
}

func TestSort(t *testing.T) {
	systems, unrecognised := Sort([]string{"VENICE_API_KEY", "JIRA_URL", "JIRA_API_TOKEN", "SOME_PRIVATE_THING", "SOME_PRIVATE_THING"})
	if want := []string{"Jira", "Venice"}; !reflect.DeepEqual(systems, want) {
		t.Errorf("systems = %v, want %v", systems, want)
	}
	if want := []string{"SOME_PRIVATE_THING"}; !reflect.DeepEqual(unrecognised, want) {
		t.Errorf("unrecognised = %v, want %v", unrecognised, want)
	}
}

// Every line of the file must parse, and no system may be named twice: a
// second line for the same system would be a prefix nobody finds.
func TestCatalogueFile(t *testing.T) {
	lines := 0
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			lines++
		}
	}
	if len(entries) != lines {
		t.Fatalf("%d lines but %d entries: a line did not parse", lines, len(entries))
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.system] {
			t.Errorf("%q is listed twice", e.system)
		}
		seen[e.system] = true
		for _, prefix := range e.prefixes {
			if !strings.HasSuffix(prefix, "_") {
				t.Errorf("prefix %q of %q should end in _ so it matches whole words", prefix, e.system)
			}
		}
	}
}
