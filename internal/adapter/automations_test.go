package adapter

import (
	"strings"
	"testing"
)

func TestClassifyError(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"Rate limit reached for requests", ErrorRateLimit},
		{"HTTP 429 from provider", ErrorRateLimit},
		{"Too Many Requests", ErrorRateLimit},
		{"rate_limit_error: slow down", ErrorRateLimit},
		{"request timed out after 60s", ErrorTimeout},
		{"context deadline exceeded", ErrorTimeout},
		{"ETIMEDOUT: Timeout while connecting", ErrorTimeout},
		{"401 Unauthorized", ErrorAuth},
		{"status 403", ErrorAuth},
		{"Forbidden: missing scope", ErrorAuth},
		{"invalid API key", ErrorAuth},
		{"no credentials found for the provider", ErrorAuth},
		{"authentication failed", ErrorAuth},
		{"model is overloaded, try again", ErrorModel},
		{"503 Service Unavailable", ErrorModel},
		{"upstream connect error", ErrorModel},
		{"unknown model: example-1", ErrorModel},
		{"exit status 2", ErrorScript},
		{"script exited with non-zero code", ErrorScript},
		{"something went wrong", ErrorOther},
		{"", ErrorOther},
		// Numbers inside other numbers are not status codes.
		{"took 14290ms then failed", ErrorOther},
		// The first matching group wins.
		{"429 from upstream model", ErrorRateLimit},
		{"401 after timeout", ErrorTimeout},
	}
	for _, c := range cases {
		if got := ClassifyError(c.text); got != c.want {
			t.Errorf("ClassifyError(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestCleanAutomationName(t *testing.T) {
	cases := map[string]string{
		"  Morning brief (test) ": "Morning brief (test)",
		"two\nlines":              "two lines",
		"\t":                      "",
		strings.Repeat("x", 300):  strings.Repeat("x", maxAutomationName),
	}
	for in, want := range cases {
		if got := CleanAutomationName(in); got != want {
			t.Errorf("CleanAutomationName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanTimezone(t *testing.T) {
	for in, want := range map[string]string{
		"America/New_York":       "America/New_York",
		"UTC":                    "UTC",
		"Etc/GMT+5":              "Etc/GMT+5",
		"":                       "",
		"Europe/Paris; rm -rf /": "",
	} {
		if got := CleanTimezone(in); got != want {
			t.Errorf("CleanTimezone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAutomationIDPattern(t *testing.T) {
	for id, want := range map[string]bool{
		"66735c7e6157": true,
		"job_1.daily":  true,
		"-rf":          false,
		"--json":       false,
		"":             false,
		"a b":          false,
	} {
		if got := AutomationIDPattern.MatchString(id); got != want {
			t.Errorf("AutomationIDPattern(%q) = %v, want %v", id, got, want)
		}
	}
}
