package hermes

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

// Invented jobs in the shape Hermes writes. The prompt, script, model and
// error text must never reach the report.
const jobsFixture = `{
  "jobs": [
    {
      "id": "aaa111", "name": "Morning digest", "prompt": "SECRET PROMPT",
      "script": "SECRET.sh", "model": "SECRET-MODEL", "deliver": "SECRET-TARGET",
      "schedule": {"kind": "cron", "expr": "0 8 * * *", "display": "0 8 * * *"},
      "enabled": true, "state": "scheduled",
      "next_run_at": "2026-03-02T08:00:00+00:00",
      "last_run_at": "2026-03-01T08:00:01.500000+00:00",
      "last_status": "ok", "last_error": null, "failure_streak": 0
    },
    {
      "id": "bbb222", "name": "Failing export", "prompt": "SECRET PROMPT",
      "schedule": {"kind": "interval", "minutes": 15, "display": "every 15m"},
      "enabled": true, "state": "scheduled", "timezone": "Asia/Tokyo",
      "next_run_at": "2026-03-01T09:15:00+00:00",
      "last_run_at": "2026-03-01T09:00:00+00:00",
      "last_status": "error", "last_error": "Script exited with exit status 3: SECRET DETAIL",
      "last_delivery_error": "SECRET DELIVERY ERROR", "failure_streak": 4
    },
    {
      "id": "ccc333", "name": "Paused report",
      "schedule": {"kind": "cron", "expr": "30 17 * * 5", "display": "30 17 * * 5"},
      "enabled": true, "state": "paused", "timezone": "Europe/Berlin",
      "next_run_at": null, "last_run_at": null, "last_status": null, "failure_streak": 0
    },
    {
      "id": "ddd444", "name": "Send summary",
      "schedule": {"kind": "once", "run_at": "2026-03-05T10:00:00+00:00", "display": "once at 2026-03-05 10:00"},
      "enabled": true, "state": "scheduled",
      "last_run_at": "2026-03-01T07:00:00+00:00", "last_status": "delivery_failed",
      "last_delivery_error": "SECRET", "failure_streak": 0
    },
    {"id": "-x", "name": "bad id"}
  ]
}`

const executionsSchema = `CREATE TABLE executions (
	id TEXT PRIMARY KEY, job_id TEXT NOT NULL, source TEXT NOT NULL DEFAULT 'scheduler',
	process_id TEXT NOT NULL DEFAULT 'p', pid INTEGER NOT NULL DEFAULT 1, process_started_at INTEGER,
	status TEXT NOT NULL, handoff_pending INTEGER NOT NULL DEFAULT 0, handoff_started_at REAL,
	claimed_at TEXT NOT NULL, started_at TEXT, finished_at TEXT, error TEXT,
	delivery_outcome TEXT, scheduled_instant TEXT, progress_at TEXT)`

type row struct {
	id, job, status, started, finished, delivery, scheduled, errText string
}

func cronHome(t *testing.T, jobs string, rows []row) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, cronDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if jobs != "" {
		write(t, filepath.Join(dir, cronJobsFile), jobs)
	}
	if rows != nil {
		addRows(t, home, rows)
	}
	return home
}

func addRows(t *testing.T, home string, rows []row) {
	t.Helper()
	path := filepath.Join(home, cronDir, cronExecutions)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(executionsSchema); err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}
	null := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	for _, r := range rows {
		claimed := r.started
		if claimed == "" {
			claimed = r.finished
		}
		if _, err := db.Exec(`INSERT INTO executions (id, job_id, status, claimed_at, started_at, finished_at, error, delivery_outcome, scheduled_instant) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.id, r.job, r.status, claimed, null(r.started), null(r.finished), null(r.errText), null(r.delivery), null(r.scheduled)); err != nil {
			t.Fatal(err)
		}
	}
}

var ledger = []row{
	{id: "e1", job: "aaa111", status: "completed", started: "2026-03-01T07:59:59.000000+00:00", finished: "2026-03-01T08:00:01.500000+00:00", delivery: "suppressed"},
	{id: "e0", job: "aaa111", status: "completed", started: "2026-02-28T08:00:00+00:00", finished: "2026-02-28T08:00:02+00:00", delivery: "delivered"},
	{id: "e2", job: "bbb222", status: "failed", started: "2026-03-01T09:00:00+00:00", finished: "2026-03-01T09:00:00.250000+00:00", errText: "exit status 3: SECRET"},
	{id: "e3", job: "bbb222", status: "missed", finished: "2026-03-01T08:50:00+00:00", scheduled: "2026-03-01T08:45:00+00:00", errText: "missed ×1"},
	{id: "e4", job: "ddd444", status: "completed", started: "2026-03-01T06:59:00+00:00", finished: "2026-03-01T07:00:00+00:00", delivery: "failed"},
	{id: "e5", job: "bbb222", status: "running", started: "2026-03-01T09:15:00+00:00"},
	{id: "e6", job: "zzz999", status: "completed", started: "2026-03-01T05:00:00+00:00", finished: "2026-03-01T05:00:01+00:00"},
}

func TestHermesAutomations(t *testing.T) {
	home := cronHome(t, jobsFixture, ledger)
	write(t, filepath.Join(home, "config.yaml"), "timezone: America/Chicago\nmodel: {provider: x}\n")
	a := testAdapter(home, true)

	report, ok := a.Automations(context.Background(), nil)
	if !ok {
		t.Fatal("no report")
	}
	want := []adapter.AutomationJob{
		{ID: "aaa111", Name: "Morning digest", Enabled: true, Schedule: adapter.AutomationSchedule{Kind: "cron", Expr: "0 8 * * *", TZ: "America/Chicago", Display: "0 8 * * *"}},
		{ID: "bbb222", Name: "Failing export", Enabled: true, Schedule: adapter.AutomationSchedule{Kind: "every", EveryMs: 900000, Display: "every 15m"}},
		{ID: "ccc333", Name: "Paused report", Enabled: false, Schedule: adapter.AutomationSchedule{Kind: "cron", Expr: "30 17 * * 5", TZ: "Europe/Berlin", Display: "30 17 * * 5"}},
		{ID: "ddd444", Name: "Send summary", Enabled: true, Schedule: adapter.AutomationSchedule{Kind: "at", Display: "once at 2026-03-05 10:00"}},
	}
	if len(report.Jobs) != len(want) {
		t.Fatalf("jobs = %+v", report.Jobs)
	}
	for i := range want {
		if report.Jobs[i] != want[i] {
			t.Errorf("job %d = %+v, want %+v", i, report.Jobs[i], want[i])
		}
	}

	digest, failing, paused, summary := report.States[0], report.States[1], report.States[2], report.States[3]
	if *digest.LastStatus != "ok" || *digest.LastDurationMs != 2500 || digest.LastDelivered != nil || digest.ErrorCode != nil ||
		digest.NextRunAt.Format("15:04") != "08:00" {
		t.Errorf("digest state = %+v", digest)
	}
	if *failing.LastStatus != "error" || *failing.ErrorCode != "script" || failing.ConsecutiveErrors != 4 || *failing.LastDurationMs != 250 {
		t.Errorf("failing state = %+v", failing)
	}
	if paused.LastStatus != nil || paused.LastRunAt != nil || paused.NextRunAt != nil || paused.LastDurationMs != nil {
		t.Errorf("paused state = %+v", paused)
	}
	if *summary.LastStatus != "ok" || *summary.LastDelivered != false || *summary.ErrorCode != "delivery" {
		t.Errorf("summary state = %+v", summary)
	}

	// First collection: only each listed job's latest finished run, oldest first.
	if got := strings.Join(ids(report), ","); got != "e4,e1,e2" {
		t.Errorf("first runs = %s, want e4,e1,e2", got)
	}
	failed := report.Runs[2]
	if failed.Status != "error" || *failed.ErrorCode != "script" || *failed.DurationMs != 250 || failed.Delivered != nil {
		t.Errorf("failed run = %+v", failed)
	}
	undelivered := report.Runs[0]
	if undelivered.Status != "ok" || *undelivered.Delivered != false || *undelivered.ErrorCode != "delivery" {
		t.Errorf("undelivered run = %+v", undelivered)
	}

	data, _ := json.Marshal(report)
	for _, secret := range []string{"SECRET", "missed ×1", "exit status"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("report carries %q: %s", secret, data)
		}
	}

	// Nothing new: no runs, same cursor.
	again, ok := a.Automations(context.Background(), report.Cursor)
	if !ok || len(again.Runs) != 0 || string(again.Cursor) != string(report.Cursor) {
		t.Fatalf("second collection runs = %v, cursor %s then %s", ids(again), report.Cursor, again.Cursor)
	}

	// New rows, including one finishing at the cursor's exact time and the
	// running one that has now finished.
	addRows(t, home, []row{
		{id: "e1b", job: "aaa111", status: "completed", started: "2026-03-01T08:00:00+00:00", finished: "2026-03-01T09:00:00.250000+00:00", delivery: "delivered"},
		{id: "e7", job: "bbb222", status: "cancelled", started: "2026-03-01T09:30:00+00:00", finished: "2026-03-01T09:30:01+00:00"},
		{id: "e8", job: "bbb222", status: "missed", finished: "2026-03-01T10:00:00+00:00", scheduled: "2026-03-01T09:45:00+00:00"},
	})
	third, ok := a.Automations(context.Background(), again.Cursor)
	if !ok {
		t.Fatal("no third report")
	}
	if got := strings.Join(ids(third), ","); got != "e1b,e7,e8" {
		t.Errorf("new runs = %s, want e1b,e7,e8", got)
	}
	if third.Runs[1].Status != "skipped" || third.Runs[2].Status != "missed" || third.Runs[2].DurationMs != nil ||
		third.Runs[2].StartedAt.Format("15:04") != "09:45" || *third.Runs[0].Delivered != true {
		t.Errorf("new runs = %+v", third.Runs)
	}
	if fourth, _ := a.Automations(context.Background(), third.Cursor); len(fourth.Runs) != 0 {
		t.Errorf("runs reported twice: %v", ids(fourth))
	}
}

func ids(report adapter.AutomationReport) []string {
	out := []string{}
	for _, run := range report.Runs {
		out = append(out, run.ID)
	}
	return out
}

func TestHermesAutomationsWithoutAScheduler(t *testing.T) {
	// No scheduler folder: not supported, nothing sent.
	if _, ok := testAdapter(t.TempDir(), true).Automations(context.Background(), nil); ok {
		t.Error("a home without a cron folder should report nothing")
	}
	// A scheduler folder but no jobs yet: an empty list.
	report, ok := testAdapter(cronHome(t, "", nil), true).Automations(context.Background(), nil)
	if !ok || len(report.Jobs) != 0 || len(report.Runs) != 0 {
		t.Errorf("empty scheduler = %+v, %v", report, ok)
	}
	// A job list that cannot be read: nothing sent.
	if _, ok := testAdapter(cronHome(t, "{not json", nil), true).Automations(context.Background(), nil); ok {
		t.Error("an unreadable job list should report nothing")
	}
	// Jobs but no ledger yet: jobs and states, no runs.
	report, ok = testAdapter(cronHome(t, jobsFixture, nil), true).Automations(context.Background(), nil)
	if !ok || len(report.Jobs) != 4 || len(report.Runs) != 0 {
		t.Errorf("no ledger = %+v, %v", report, ok)
	}
}

func TestParseHermesTime(t *testing.T) {
	for in, want := range map[string]string{
		"2026-11-12T05:30:32.262173+00:00": "2026-11-12T05:30:32.262173Z",
		"2026-11-12T10:30:00+05:00":        "2026-11-12T05:30:00Z",
		"2026-11-12T05:30:00":              "2026-11-12T05:30:00Z",
	} {
		got, ok := parseHermesTime(in)
		if !ok || got.Format("2006-01-02T15:04:05.999999Z07:00") != want {
			t.Errorf("parseHermesTime(%q) = %v, want %s", in, got, want)
		}
	}
	if _, ok := parseHermesTime("yesterday"); ok {
		t.Error("parsed nonsense")
	}
}
