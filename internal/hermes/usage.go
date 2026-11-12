package hermes

import (
	"context"
	"database/sql"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite" // read-only access to Hermes's session store

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

// sessionStore is Hermes's SQLite database of sessions and messages.
const sessionStore = "state.db"

// Counts per day from the messages table. Hermes copies a message's row when it
// compacts a conversation but keeps its message_uid, so rows are counted by
// that id. Nothing but counts and the platform name leaves the database.
const usageQuery = `
SELECT COALESCE(s.source, 'unknown') AS channel,
       COUNT(DISTINCT m.session_id) AS sessions,
       COUNT(DISTINCT CASE WHEN m.role = 'user' THEN COALESCE(m.message_uid, m.id) END) AS user_messages,
       COUNT(DISTINCT CASE WHEN m.role = 'assistant' THEN COALESCE(m.message_uid, m.id) END) AS assistant_messages,
       COUNT(DISTINCT CASE WHEN m.role = 'tool' THEN COALESCE(m.message_uid, m.id) END) AS tool_calls,
       COALESCE(SUM(CASE WHEN m.active = 1 OR m.active IS NULL THEN m.token_count END), 0) AS tokens
FROM messages m
LEFT JOIN sessions s ON s.id = m.session_id
WHERE m.timestamp >= ? AND m.timestamp < ?
GROUP BY channel`

// Cost per day. Hermes records an estimated cost per conversation (and an
// actual one where its provider reports it), not per message. A conversation's
// cost is shared across the days it was active, in proportion to the tokens of
// its messages on each day, or to its messages when no tokens are recorded.
const costQuery = `
WITH day AS (
  SELECT session_id, COALESCE(SUM(token_count), 0) AS tokens, COUNT(*) AS messages
  FROM messages
  WHERE timestamp >= ? AND timestamp < ? AND (active = 1 OR active IS NULL)
  GROUP BY session_id
), whole AS (
  SELECT session_id, COALESCE(SUM(token_count), 0) AS tokens, COUNT(*) AS messages
  FROM messages
  WHERE session_id IN (SELECT session_id FROM day) AND (active = 1 OR active IS NULL)
  GROUP BY session_id
)
SELECT COALESCE(SUM(
  COALESCE(s.actual_cost_usd, s.estimated_cost_usd, 0) *
  CASE WHEN whole.tokens > 0 THEN 1.0 * day.tokens / whole.tokens
       ELSE 1.0 * day.messages / whole.messages END
), 0)
FROM day
JOIN whole ON whole.session_id = day.session_id
JOIN sessions s ON s.id = day.session_id`

// Usage returns the counts for each UTC day asked for. A day before today is
// final. When the store cannot be read, it returns nothing.
func (a *Adapter) Usage(ctx context.Context, days []string, today string) []adapter.UsageDay {
	path := filepath.Join(a.home, sessionStore)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?mode=ro&_pragma=busy_timeout(2000)&_pragma=query_only(1)")
	if err != nil {
		return nil
	}
	defer db.Close()

	var result []adapter.UsageDay
	for _, day := range days {
		start, err := time.Parse("2006-01-02", day)
		if err != nil {
			continue
		}
		usage, ok := usageForDay(ctx, db, start)
		if !ok {
			continue
		}
		usage.Date = day
		usage.Final = day < today
		result = append(result, usage)
	}
	return result
}

func usageForDay(ctx context.Context, db *sql.DB, start time.Time) (adapter.UsageDay, bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	from := float64(start.Unix())
	to := float64(start.Add(24 * time.Hour).Unix())
	rows, err := db.QueryContext(ctx, usageQuery, from, to)
	if err != nil {
		return adapter.UsageDay{}, false
	}
	defer rows.Close()

	day := adapter.UsageDay{Channels: []adapter.UsageChannel{}}
	for rows.Next() {
		var channel adapter.UsageChannel
		var tokens int64
		if err := rows.Scan(&channel.Channel, &channel.Sessions, &channel.UserMessages, &channel.AssistantMessages, &channel.ToolCalls, &tokens); err != nil {
			return adapter.UsageDay{}, false
		}
		day.Sessions += channel.Sessions
		day.UserMessages += channel.UserMessages
		day.AssistantMessages += channel.AssistantMessages
		day.ToolCalls += channel.ToolCalls
		day.Tokens += tokens
		day.Channels = append(day.Channels, channel)
	}
	if rows.Err() != nil {
		return adapter.UsageDay{}, false
	}
	rows.Close()
	// A store without cost columns still gives its counts.
	var cost float64
	if db.QueryRowContext(ctx, costQuery, from, to).Scan(&cost) == nil {
		day.Cost = math.Round(cost*10000) / 10000
	}
	sort.Slice(day.Channels, func(i, j int) bool { return day.Channels[i].Channel < day.Channels[j].Channel })
	return day, true
}
