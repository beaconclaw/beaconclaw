package openclaw

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

const (
	// One call for one day. The gateway may still be reading its records.
	usageCallTimeout = 90 * time.Second
	// The first answer after a quiet spell is often empty while the gateway
	// refreshes its cache. Ask again a few times before giving up on the day.
	usageAttempts = 5
	// Conversations asked for per day, to split the day by channel.
	usageSessionLimit = 1000
	otherChannel      = "other"
)

// usageWait is how long to pause between attempts. A variable so tests do not wait.
var usageWait = 20 * time.Second

// Usage counts each day's activity by asking the gateway for its own usage
// figures. It keeps numbers only: how many conversations, messages, tool
// calls and tokens, and what the runtime says they cost, in total and per
// channel. Conversation names, contents, participants, tool names and file
// names are read by nothing here and are never sent.
func (a *Adapter) Usage(ctx context.Context, days []string, today string) []adapter.UsageDay {
	counted := []adapter.UsageDay{}
	for _, day := range days {
		output, ok := usageForDay(ctx, day, runUsageCall)
		if !ok {
			continue
		}
		if usage, ok := parseUsage(output, day, day < today); ok {
			counted = append(counted, usage)
		}
	}
	return counted
}

// usageForDay asks for one day until the gateway says its figures are
// complete. It reports false when they never were: an unfinished count would
// read as a quiet day.
func usageForDay(ctx context.Context, day string, call func(context.Context, string) ([]byte, error)) ([]byte, bool) {
	for attempt := 1; attempt <= usageAttempts; attempt++ {
		output, err := call(ctx, day)
		if err == nil && usageIsFresh(output) {
			return output, true
		}
		if attempt == usageAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(usageWait):
		}
	}
	return nil, false
}

func runUsageCall(ctx context.Context, day string) ([]byte, error) {
	params, err := json.Marshal(map[string]any{
		"startDate":  day,
		"endDate":    day,
		"mode":       "specific",
		"timeZone":   "UTC",
		"agentScope": "all",
		"groupBy":    "instance",
		"limit":      usageSessionLimit,
	})
	if err != nil {
		return nil, err
	}

	var lastErr error = exec.ErrNotFound
	for _, candidate := range commandCandidates() {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, usageCallTimeout)
		cmd := exec.CommandContext(ctx, candidate, "gateway", "call", "sessions.usage", "--json", "--timeout", "60000", "--params", string(params))
		cmd.Env = commandEnv()
		var stdout bytes.Buffer
		// A busy day's answer lists every conversation; allow for that.
		cmd.Stdout = &limitedWriter{w: &stdout, n: 64 << 20}
		err := cmd.Run()
		cancel()
		if err == nil {
			return stdout.Bytes(), nil
		}
		lastErr = err
	}
	return nil, lastErr
}

type usageAnswer struct {
	CacheStatus *struct {
		Status       string `json:"status"`
		PendingFiles int    `json:"pendingFiles"`
		StaleFiles   int    `json:"staleFiles"`
	} `json:"cacheStatus"`
	Totals struct {
		TotalTokens float64 `json:"totalTokens"`
		TotalCost   float64 `json:"totalCost"`
	} `json:"totals"`
	Aggregates *struct {
		SessionCount float64 `json:"sessionCount"`
		Messages     struct {
			User      float64 `json:"user"`
			Assistant float64 `json:"assistant"`
			ToolCalls float64 `json:"toolCalls"`
		} `json:"messages"`
	} `json:"aggregates"`
	// Only these fields of a conversation are decoded. Its name, key, label,
	// creator and file are not read.
	Sessions []struct {
		Channel string `json:"channel"`
		Usage   struct {
			MessageCounts struct {
				User      float64 `json:"user"`
				Assistant float64 `json:"assistant"`
				ToolCalls float64 `json:"toolCalls"`
			} `json:"messageCounts"`
		} `json:"usage"`
	} `json:"sessions"`
}

func decodeUsage(output []byte) (*usageAnswer, bool) {
	// The command may print notices before the JSON.
	start := bytes.IndexByte(output, '{')
	if start < 0 {
		return nil, false
	}
	var answer usageAnswer
	if err := json.NewDecoder(bytes.NewReader(output[start:])).Decode(&answer); err != nil {
		return nil, false
	}
	return &answer, true
}

// usageIsFresh reports whether the gateway has finished reading its records.
func usageIsFresh(output []byte) bool {
	answer, ok := decodeUsage(output)
	if !ok || answer.CacheStatus == nil {
		return false
	}
	return answer.CacheStatus.Status == "fresh" && answer.CacheStatus.PendingFiles == 0 && answer.CacheStatus.StaleFiles == 0
}

func whole(v float64) int64 {
	if v < 0 || v != v {
		return 0
	}
	return int64(v)
}

// parseUsage turns one day's answer into counts.
func parseUsage(output []byte, day string, final bool) (adapter.UsageDay, bool) {
	answer, ok := decodeUsage(output)
	if !ok || answer.Aggregates == nil {
		return adapter.UsageDay{}, false
	}

	byChannel := map[string]*adapter.UsageChannel{}
	for _, session := range answer.Sessions {
		// The list also holds conversations that were quiet on this day. They
		// are not part of the day's count, so they are not part of its split.
		counts := session.Usage.MessageCounts
		if whole(counts.User)+whole(counts.Assistant)+whole(counts.ToolCalls) == 0 {
			continue
		}
		name := cleanName(session.Channel)
		if name == "" {
			name = otherChannel
		}
		channel := byChannel[name]
		if channel == nil {
			channel = &adapter.UsageChannel{Channel: name}
			byChannel[name] = channel
		}
		channel.Sessions++
		channel.UserMessages += whole(session.Usage.MessageCounts.User)
		channel.AssistantMessages += whole(session.Usage.MessageCounts.Assistant)
		channel.ToolCalls += whole(session.Usage.MessageCounts.ToolCalls)
	}
	channels := make([]adapter.UsageChannel, 0, len(byChannel))
	for _, channel := range byChannel {
		channels = append(channels, *channel)
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].Channel < channels[j].Channel })
	if len(channels) > maxInventoryItems {
		channels = channels[:maxInventoryItems]
	}

	cost := answer.Totals.TotalCost
	if cost < 0 || cost != cost {
		cost = 0
	}
	return adapter.UsageDay{
		Date:              day,
		Final:             final,
		Sessions:          whole(answer.Aggregates.SessionCount),
		UserMessages:      whole(answer.Aggregates.Messages.User),
		AssistantMessages: whole(answer.Aggregates.Messages.Assistant),
		ToolCalls:         whole(answer.Aggregates.Messages.ToolCalls),
		Tokens:            whole(answer.Totals.TotalTokens),
		Cost:              cost,
		Channels:          channels,
		Truncated:         len(answer.Sessions) >= usageSessionLimit,
	}, true
}
