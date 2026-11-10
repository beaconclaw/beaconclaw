package openclaw

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

const freshAnswer = `notice printed first
{
  "cacheStatus": {"status": "fresh", "cachedFiles": 40, "pendingFiles": 0, "staleFiles": 0},
  "totals": {"totalTokens": 1200300, "totalCost": 4.6005},
  "aggregates": {
    "sessionCount": 4,
    "messages": {"total": 61, "user": 11, "assistant": 50, "toolCalls": 44, "toolResults": 44, "errors": 3},
    "byCreator": [{"key": "PRIVATE-CREATOR-KEY"}],
    "tools": {"tools": [{"name": "PRIVATE-TOOL", "count": 9}]}
  },
  "sessions": [
    {"key": "PRIVATE-SESSION-KEY", "label": "PRIVATE LABEL", "creatorKey": "PRIVATE-CREATOR-KEY", "channel": "slack",
     "usage": {"sessionFile": "/home/me/PRIVATE.jsonl", "messageCounts": {"user": 6, "assistant": 20, "toolCalls": 15}}},
    {"key": "k2", "channel": "slack", "usage": {"messageCounts": {"user": 2, "assistant": 10, "toolCalls": 9}}},
    {"key": "k3", "channel": "cron", "usage": {"messageCounts": {"user": 3, "assistant": 20, "toolCalls": 20}}},
    {"key": "k4", "channel": "https://not-a-name.example/x", "usage": {"messageCounts": {"user": 0, "assistant": 1, "toolCalls": 0}}},
    {"key": "k5", "channel": "slack", "usage": {"messageCounts": {"user": 0, "assistant": 0, "toolCalls": 0}}},
    {"key": "k6", "usage": {}}
  ]
}`

const refreshingAnswer = `{"cacheStatus": {"status": "refreshing", "pendingFiles": 47, "staleFiles": 47},
  "totals": {}, "aggregates": {"sessionCount": 0, "messages": {}}, "sessions": []}`

// The answer lists six conversations, two of them quiet that day. The split
// must add up to the day's own total of four.
func TestParseUsageKeepsCountsAndNothingElse(t *testing.T) {
	usage, ok := parseUsage([]byte(freshAnswer), "2026-06-01", true)
	if !ok {
		t.Fatal("expected the answer to be read")
	}

	want := adapter.UsageDay{
		Date: "2026-06-01", Final: true, Sessions: 4, UserMessages: 11, AssistantMessages: 50, ToolCalls: 44,
		Tokens: 1200300, Cost: 4.6005,
		Channels: []adapter.UsageChannel{
			{Channel: "cron", Sessions: 1, UserMessages: 3, AssistantMessages: 20, ToolCalls: 20},
			{Channel: "other", Sessions: 1, AssistantMessages: 1},
			{Channel: "slack", Sessions: 2, UserMessages: 8, AssistantMessages: 30, ToolCalls: 24},
		},
	}
	got, _ := json.Marshal(usage)
	expected, _ := json.Marshal(want)
	if string(got) != string(expected) {
		t.Fatalf("got  %s\nwant %s", got, expected)
	}
	for _, private := range []string{"PRIVATE", "not-a-name", "jsonl"} {
		if strings.Contains(string(got), private) {
			t.Fatalf("%q must not be sent: %s", private, got)
		}
	}
}

func TestParseUsageRefusesWhatItCannotRead(t *testing.T) {
	for _, output := range []string{"", "command not found", "{broken", `{"cacheStatus": {"status": "fresh"}}`} {
		if _, ok := parseUsage([]byte(output), "2026-06-01", true); ok {
			t.Fatalf("output %q should not be read as a day", output)
		}
	}
}

func TestUsageIsFreshOnlyWhenTheGatewayHasFinishedReading(t *testing.T) {
	if !usageIsFresh([]byte(freshAnswer)) {
		t.Fatal("a fresh answer was not recognised")
	}
	for name, output := range map[string]string{
		"refreshing":    refreshingAnswer,
		"pending files": `{"cacheStatus": {"status": "fresh", "pendingFiles": 2, "staleFiles": 0}}`,
		"no status":     `{"aggregates": {}}`,
		"not JSON":      "oops",
	} {
		if usageIsFresh([]byte(output)) {
			t.Fatalf("%s: must not count as complete", name)
		}
	}
}

func TestUsageForDayAsksAgainUntilTheFiguresAreComplete(t *testing.T) {
	old := usageWait
	usageWait = time.Millisecond
	defer func() { usageWait = old }()

	answers := []string{refreshingAnswer, refreshingAnswer, freshAnswer}
	calls := 0
	output, ok := usageForDay(context.Background(), "2026-06-01", func(context.Context, string) ([]byte, error) {
		calls++
		return []byte(answers[calls-1]), nil
	})
	if !ok || calls != 3 || !usageIsFresh(output) {
		t.Fatalf("ok=%v calls=%d", ok, calls)
	}
}

func TestUsageForDayGivesUpRatherThanReportAnUnfinishedCount(t *testing.T) {
	old := usageWait
	usageWait = time.Millisecond
	defer func() { usageWait = old }()

	calls := 0
	_, ok := usageForDay(context.Background(), "2026-06-01", func(context.Context, string) ([]byte, error) {
		calls++
		if calls%2 == 0 {
			return nil, errors.New("gateway not answering")
		}
		return []byte(refreshingAnswer), nil
	})
	if ok || calls != usageAttempts {
		t.Fatalf("ok=%v calls=%d, want a refusal after %d attempts", ok, calls, usageAttempts)
	}
}
