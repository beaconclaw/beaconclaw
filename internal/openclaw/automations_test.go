package openclaw

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

// A small "openclaw cron list --json --all" answer with invented jobs. The
// payload and error text must never reach the report.
const cronListFixture = `Config warnings: none
{
  "jobs": [
    {
      "id": "daily-standup",
      "name": "standup",
      "displayName": "Daily standup notes",
      "enabled": true,
      "schedule": { "kind": "cron", "expr": "0 9 * * 1-5", "tz": "America/New_York" },
      "payload": { "kind": "agentTurn", "message": "SECRET PROMPT TEXT" },
      "delivery": { "channel": "chat", "to": "SECRET TARGET" },
      "state": {
        "nextRunAtMs": 1790000000000,
        "lastRunAtMs": 1789990000000,
        "lastRunStatus": "error",
        "lastDurationMs": 4200,
        "lastDeliveryStatus": "not-requested",
        "lastDelivered": false,
        "consecutiveErrors": 3,
        "lastError": "HTTP 429 Too Many Requests: SECRET ERROR DETAIL"
      }
    },
    {
      "id": "inbox-poll",
      "name": "Inbox poll",
      "enabled": true,
      "schedule": { "kind": "every", "everyMs": 120000, "anchorMs": 1789000000000 },
      "state": {
        "nextRunAtMs": 1790000060000,
        "lastRunAtMs": 1789999940000,
        "lastRunStatus": "ok",
        "lastDurationMs": 900,
        "lastDeliveryStatus": "delivered"
      }
    },
    {
      "id": "old-reminder",
      "name": "Reminder",
      "enabled": false,
      "schedule": { "kind": "at", "at": "2026-01-01T09:00:00Z" },
      "nextRunAtMs": 0,
      "lastRunAtMs": 1789000000000,
      "lastRunStatus": "skipped",
      "state": {}
    },
    {
      "id": "build-watch",
      "name": "Build watch",
      "schedule": { "kind": "on-exit", "command": "SECRET COMMAND" },
      "state": {}
    },
    { "id": "--rm", "name": "not an id" }
  ]
}`

func TestParseCronList(t *testing.T) {
	jobs, ok := parseCronList([]byte(cronListFixture))
	if !ok || len(jobs) != 4 {
		t.Fatalf("parsed %d jobs (ok=%v), want 4 without the bad id", len(jobs), ok)
	}
	report := buildCronReport(jobs, nil, func(string) ([]cronRun, bool) { return nil, false })

	want := []adapter.AutomationJob{
		{ID: "daily-standup", Name: "Daily standup notes", Enabled: true, Schedule: adapter.AutomationSchedule{Kind: "cron", Expr: "0 9 * * 1-5", TZ: "America/New_York"}},
		{ID: "inbox-poll", Name: "Inbox poll", Enabled: true, Schedule: adapter.AutomationSchedule{Kind: "every", EveryMs: 120000}},
		{ID: "old-reminder", Name: "Reminder", Enabled: false, Schedule: adapter.AutomationSchedule{Kind: "at"}},
		{ID: "build-watch", Name: "Build watch", Enabled: true, Schedule: adapter.AutomationSchedule{Kind: "other"}},
	}
	for i, job := range want {
		if report.Jobs[i] != job {
			t.Errorf("job %d = %+v, want %+v", i, report.Jobs[i], job)
		}
	}

	standup := report.States[0]
	if standup.LastStatus == nil || *standup.LastStatus != "error" || standup.ErrorCode == nil || *standup.ErrorCode != "rate_limit" ||
		standup.ConsecutiveErrors != 3 || *standup.LastDurationMs != 4200 || standup.LastDelivered != nil ||
		standup.NextRunAt.UnixMilli() != 1790000000000 {
		t.Errorf("standup state = %+v", standup)
	}
	poll := report.States[1]
	if poll.LastDelivered == nil || !*poll.LastDelivered || poll.ErrorCode != nil {
		t.Errorf("poll state = %+v", poll)
	}
	// The top-level fields stand in for an empty state.
	reminder := report.States[2]
	if reminder.LastStatus == nil || *reminder.LastStatus != "skipped" || reminder.LastRunAt == nil || reminder.NextRunAt != nil {
		t.Errorf("reminder state = %+v", reminder)
	}

	data, _ := json.Marshal(report)
	for _, secret := range []string{"SECRET", "agentTurn", "anchorMs", "Too Many"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("report carries %q: %s", secret, data)
		}
	}
}

func TestParseCronListRefusesAnswersWithoutJobs(t *testing.T) {
	for _, output := range []string{
		`{"ok": false, "error": {"type": "cli_error", "message": "Gateway not reachable"}}`,
		`{"jobs": null}`,
		`not json`,
		``,
	} {
		if _, ok := parseCronList([]byte(output)); ok {
			t.Errorf("parseCronList(%q) should fail", output)
		}
	}
	if jobs, ok := parseCronList([]byte(`{"jobs": []}`)); !ok || len(jobs) != 0 {
		t.Error("an empty job list is a valid answer")
	}
}

func TestParseCronRuns(t *testing.T) {
	runs, ok := parseCronRuns([]byte(`{"entries": [
		{"jobId": "inbox-poll", "status": "ok", "runAtMs": 1000, "durationMs": 10, "deliveryStatus": "delivered", "ts": 1010, "summary": "SECRET"},
		{"jobId": "inbox-poll", "status": "error", "runAtMs": 2000, "error": "exit status 1", "ts": 2010}
	]}`))
	if !ok || len(runs) != 2 || runs[1].Status != "error" || runs[1].Error != "exit status 1" {
		t.Fatalf("runs = %+v, ok = %v", runs, ok)
	}
	run, ok := cronRunOf("inbox-poll", runs[1])
	if !ok || run.ID != "inbox-poll:2000" || run.ErrorCode == nil || *run.ErrorCode != "script" || run.DurationMs != nil {
		t.Errorf("run = %+v", run)
	}
	if _, ok := parseCronRuns([]byte(`{"ok": false}`)); ok {
		t.Error("an answer without entries should fail")
	}
}

func job(id, kind string, everyMs float64, expr string, lastRunAtMs float64, status string) cronJob {
	j := cronJob{ID: id, Name: id}
	j.Schedule.Kind = kind
	j.Schedule.EveryMs = everyMs
	j.Schedule.Expr = expr
	j.State.LastRunAtMs = lastRunAtMs
	j.State.LastRunStatus = status
	return j
}

func cursorOf(t *testing.T, seen map[string]int64) []byte {
	t.Helper()
	data, err := json.Marshal(cronCursor{LastRunAtMs: seen})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func runIDs(report adapter.AutomationReport) []string {
	ids := []string{}
	for _, run := range report.Runs {
		ids = append(ids, run.ID)
	}
	return ids
}

func TestRunDerivation(t *testing.T) {
	noHistory := func(string) ([]cronRun, bool) { t.Error("history asked for unexpectedly"); return nil, false }

	cases := []struct {
		name    string
		jobs    []cronJob
		cursor  []byte
		history map[string][]cronRun
		want    []string
		cursorW map[string]int64
		asked   []string
	}{
		{
			name:    "first collection: only the latest run of each job, history not asked",
			jobs:    []cronJob{job("slow", "cron", 0, "0 9 * * *", 5000, "ok"), job("fast", "every", 60000, "", 7000, "ok"), job("never", "cron", 0, "0 9 * * *", 0, "")},
			want:    []string{"slow:5000", "fast:7000"},
			cursorW: map[string]int64{"slow": 5000, "fast": 7000},
		},
		{
			name:    "last run unchanged: no runs",
			jobs:    []cronJob{job("slow", "cron", 0, "0 9 * * *", 5000, "ok"), job("fast", "every", 60000, "", 7000, "ok")},
			cursor:  cursorOf(t, map[string]int64{"slow": 5000, "fast": 7000}),
			want:    []string{},
			cursorW: map[string]int64{"slow": 5000, "fast": 7000},
		},
		{
			name:    "a slow job's last run changed: one run, from its state",
			jobs:    []cronJob{job("slow", "cron", 0, "0 9 * * *", 9000, "error")},
			cursor:  cursorOf(t, map[string]int64{"slow": 5000}),
			want:    []string{"slow:9000"},
			cursorW: map[string]int64{"slow": 9000},
		},
		{
			name:   "a frequent job: runs since the last report from its history, without repeats",
			jobs:   []cronJob{job("fast", "every", 60000, "", 9000, "ok")},
			cursor: cursorOf(t, map[string]int64{"fast": 5000}),
			history: map[string][]cronRun{"fast": {
				{JobID: "fast", Status: "ok", RunAtMs: 9000},
				{JobID: "fast", Status: "error", RunAtMs: 8000, Error: "timed out"},
				{JobID: "fast", Status: "ok", RunAtMs: 7000},
				{JobID: "fast", Status: "ok", RunAtMs: 5000}, // already reported
				{JobID: "fast", Status: "ok", RunAtMs: 4000},
			}},
			want:    []string{"fast:9000", "fast:8000", "fast:7000"},
			cursorW: map[string]int64{"fast": 9000},
			asked:   []string{"fast"},
		},
		{
			name:    "a frequent job whose history cannot be read: the latest run still",
			jobs:    []cronJob{job("fast", "cron", 0, "*/2 * * * *", 9000, "ok")},
			cursor:  cursorOf(t, map[string]int64{"fast": 5000}),
			history: map[string][]cronRun{},
			want:    []string{"fast:9000"},
			cursorW: map[string]int64{"fast": 9000},
			asked:   []string{"fast"},
		},
		{
			name:   "history newer than the list moves the cursor past it",
			jobs:   []cronJob{job("fast", "every", 60000, "", 9000, "ok")},
			cursor: cursorOf(t, map[string]int64{"fast": 5000}),
			history: map[string][]cronRun{"fast": {
				{JobID: "fast", Status: "ok", RunAtMs: 10000},
				{JobID: "fast", Status: "ok", RunAtMs: 9000},
			}},
			want:    []string{"fast:10000", "fast:9000"},
			cursorW: map[string]int64{"fast": 10000},
			asked:   []string{"fast"},
		},
		{
			name:    "a job that is gone is forgotten",
			jobs:    []cronJob{},
			cursor:  cursorOf(t, map[string]int64{"gone": 5000}),
			want:    []string{},
			cursorW: map[string]int64{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asked := []string{}
			history := noHistory
			if c.history != nil {
				history = func(id string) ([]cronRun, bool) {
					asked = append(asked, id)
					runs, ok := c.history[id]
					return runs, ok
				}
			}
			report := buildCronReport(c.jobs, c.cursor, history)
			if got := runIDs(report); strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("runs = %v, want %v", got, c.want)
			}
			if c.asked != nil && strings.Join(asked, ",") != strings.Join(c.asked, ",") {
				t.Errorf("history asked for %v, want %v", asked, c.asked)
			}
			var next cronCursor
			if err := json.Unmarshal(report.Cursor, &next); err != nil {
				t.Fatal(err)
			}
			if len(next.LastRunAtMs) != len(c.cursorW) {
				t.Errorf("cursor = %v, want %v", next.LastRunAtMs, c.cursorW)
			}
			for id, ms := range c.cursorW {
				if next.LastRunAtMs[id] != ms {
					t.Errorf("cursor[%s] = %d, want %d", id, next.LastRunAtMs[id], ms)
				}
			}
		})
	}
}

func TestIsFrequent(t *testing.T) {
	cases := []struct {
		kind    string
		everyMs float64
		expr    string
		want    bool
	}{
		{"every", 60000, "", true},
		{"every", 9 * 60000, "", true},
		{"every", 10 * 60000, "", false},
		{"every", 3600000, "", false},
		{"cron", 0, "* * * * *", true},
		{"cron", 0, "*/5 * * * *", true},
		{"cron", 0, "0-59/2 * * * *", true},
		{"cron", 0, "*/10 * * * *", false},
		{"cron", 0, "*/15 9-17 * * 1-5", false},
		{"cron", 0, "0 9 * * 1-5", false},
		{"cron", 0, "0,5,10,15,20,25,30 * * * *", true},
		{"cron", 0, "30 0 9 * * *", false},
		{"cron", 0, "*/30 * * * * *", true},
		{"cron", 0, "", false},
		{"at", 0, "", false},
	}
	for _, c := range cases {
		j := job("x", c.kind, c.everyMs, c.expr, 0, "")
		if got := isFrequent(j); got != c.want {
			t.Errorf("isFrequent(%s %v %q) = %v, want %v", c.kind, c.everyMs, c.expr, got, c.want)
		}
	}
}

func TestDeliveredAndErrorCodes(t *testing.T) {
	cases := []struct {
		status, delivery, errText string
		delivered                 *bool
		code                      *string
	}{
		{"ok", "delivered", "", adapter.Ptr(true), nil},
		{"ok", "not-delivered", "", adapter.Ptr(false), adapter.Ptr("delivery")},
		{"ok", "not-requested", "", nil, nil},
		{"ok", "unknown", "", nil, nil},
		{"error", "not-requested", "401 Unauthorized", nil, adapter.Ptr("auth")},
		{"skipped", "", "provider unreachable", nil, nil},
	}
	for _, c := range cases {
		run, ok := cronRunOf("j", cronRun{Status: c.status, RunAtMs: 1000, DeliveryStatus: c.delivery, Error: c.errText})
		if !ok {
			t.Fatalf("%+v not a run", c)
		}
		if !samePtr(run.Delivered, c.delivered) || !samePtr(run.ErrorCode, c.code) {
			t.Errorf("%s/%s: delivered=%v code=%v", c.status, c.delivery, deref(run.Delivered), deref(run.ErrorCode))
		}
	}
	if _, ok := cronRunOf("j", cronRun{Status: "running", RunAtMs: 1000}); ok {
		t.Error("an unfinished run was reported")
	}
}

func samePtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
