package hermes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

// Hermes's scheduler keeps its jobs in cron/jobs.json and a ledger of their
// runs in the SQLite database cron/executions.db.
const (
	cronDir        = "cron"
	cronJobsFile   = "jobs.json"
	cronExecutions = "executions.db"
	// jobs.json is a short list; anything bigger is not read.
	maxJobsFile = 16 << 20
	// Rows read from the ledger in one collection, newest first.
	maxExecutionRows = 1000
	maxJobs          = 500
)

// cronJobsListing is the part of jobs.json read here. A job's prompt, script,
// model, skills, delivery target and error text are not decoded, except the
// error text, which is read only to be classified on this machine.
type cronJobsListing struct {
	Jobs *[]hermesJob `json:"jobs"`
}

type hermesJob struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Enabled  *bool  `json:"enabled"`
	State    string `json:"state"`
	Schedule struct {
		Kind    string  `json:"kind"`
		Expr    string  `json:"expr"`
		Minutes float64 `json:"minutes"`
		Display string  `json:"display"`
	} `json:"schedule"`
	Timezone      string  `json:"timezone"`
	NextRunAt     *string `json:"next_run_at"`
	LastRunAt     *string `json:"last_run_at"`
	LastStatus    *string `json:"last_status"`
	FailureStreak float64 `json:"failure_streak"`
	// LastError is read only to be classified here. It is never sent.
	LastError *string `json:"last_error"`
}

// execution is one finished row of the ledger.
type execution struct {
	ID       string
	JobID    string
	Status   string
	Started  *time.Time
	Finished time.Time
	Delivery string
	// Error is read only for a failed run, to be classified here. It is never
	// sent.
	Error string
}

// hermesCursor is the newest finish time reported, and the ids of the runs
// that finished at exactly that time.
type hermesCursor struct {
	FinishedAt time.Time `json:"finished_at"`
	IDs        []string  `json:"ids"`
}

// Only these columns of the ledger are read. A failed run's error text is
// read to be classified, and nothing else of it.
const executionsQuery = `
SELECT id, job_id, status, started_at, finished_at, delivery_outcome,
       scheduled_instant, claimed_at,
       CASE WHEN status = 'failed' THEN error END
FROM executions
WHERE status IN ('completed', 'failed', 'cancelled', 'missed') AND finished_at IS NOT NULL
ORDER BY COALESCE(julianday(finished_at), 0) DESC, id DESC
LIMIT ?`

// Automations reads Hermes's scheduled jobs from cron/jobs.json and their
// finished runs from cron/executions.db, opened read-only. It reports false
// when there is no scheduler folder, or when the job list cannot be read.
func (a *Adapter) Automations(ctx context.Context, cursor []byte) (adapter.AutomationReport, bool) {
	jobs, ok := a.readCronJobs()
	if !ok {
		return adapter.AutomationReport{}, false
	}
	executions, ok := a.readExecutions(ctx)
	if !ok {
		// The runs are picked up next time: the cursor stays where it was.
		executions = nil
	}
	report := buildHermesReport(jobs, executions, ok, a.configTimezone(), cursor)
	if ctx.Err() != nil {
		return adapter.AutomationReport{}, false
	}
	return report, true
}

func (a *Adapter) readCronJobs() ([]hermesJob, bool) {
	path := filepath.Join(a.home, cronDir, cronJobsFile)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		// A scheduler folder without a job list has no jobs yet.
		if info, err := os.Stat(filepath.Join(a.home, cronDir)); err == nil && info.IsDir() {
			return []hermesJob{}, true
		}
		return nil, false
	}
	if err != nil {
		return nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxJobsFile+1))
	if err != nil || len(data) > maxJobsFile {
		return nil, false
	}
	return parseCronJobs(data)
}

func parseCronJobs(data []byte) ([]hermesJob, bool) {
	var listing cronJobsListing
	if err := json.Unmarshal(data, &listing); err != nil || listing.Jobs == nil {
		return nil, false
	}
	jobs := []hermesJob{}
	seen := map[string]bool{}
	for _, job := range *listing.Jobs {
		if !adapter.AutomationIDPattern.MatchString(job.ID) || seen[job.ID] {
			continue
		}
		seen[job.ID] = true
		jobs = append(jobs, job)
	}
	if len(jobs) > maxJobs {
		jobs = jobs[:maxJobs]
	}
	return jobs, true
}

// readExecutions reads the newest finished runs. A ledger that does not exist
// yet has no runs.
func (a *Adapter) readExecutions(ctx context.Context) ([]execution, bool) {
	path := filepath.Join(a.home, cronDir, cronExecutions)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return []execution{}, true
	} else if err != nil {
		return nil, false
	}
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?mode=ro&_pragma=busy_timeout(2000)&_pragma=query_only(1)")
	if err != nil {
		return nil, false
	}
	defer db.Close()
	return queryExecutions(ctx, db)
}

func queryExecutions(ctx context.Context, db *sql.DB) ([]execution, bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, executionsQuery, maxExecutionRows)
	if err != nil {
		return nil, false
	}
	defer rows.Close()

	result := []execution{}
	for rows.Next() {
		var id, jobID, status string
		var started, finished, delivery, scheduled, claimed, errText sql.NullString
		if err := rows.Scan(&id, &jobID, &status, &started, &finished, &delivery, &scheduled, &claimed, &errText); err != nil {
			return nil, false
		}
		end, ok := parseHermesTime(finished.String)
		if !ok {
			continue
		}
		run := execution{ID: id, JobID: jobID, Status: status, Finished: end, Delivery: delivery.String, Error: errText.String}
		// A missed run never started: it is placed at the slot it missed.
		for _, candidate := range []sql.NullString{started, scheduled, claimed} {
			if t, ok := parseHermesTime(candidate.String); ok {
				run.Started = &t
				break
			}
		}
		result = append(result, run)
	}
	if rows.Err() != nil {
		return nil, false
	}
	return result, true
}

// buildHermesReport puts the jobs and the ledger together. With readRuns
// false the ledger could not be read, and the cursor is handed back as it was.
func buildHermesReport(jobs []hermesJob, executions []execution, readRuns bool, timezone string, cursor []byte) adapter.AutomationReport {
	report := adapter.AutomationReport{
		Jobs:   []adapter.AutomationJob{},
		States: []adapter.AutomationState{},
		Runs:   []adapter.AutomationRun{},
		Cursor: cursor,
	}

	// The newest finished run of each job, for its state.
	latest := map[string]execution{}
	for _, run := range executions {
		if have, ok := latest[run.JobID]; !ok || run.Finished.After(have.Finished) {
			latest[run.JobID] = run
		}
	}

	known := map[string]bool{}
	for _, job := range jobs {
		known[job.ID] = true
		report.Jobs = append(report.Jobs, hermesDefinition(job, timezone))
		last, hasLast := latest[job.ID]
		report.States = append(report.States, hermesStateOf(job, last, hasLast))
	}

	if !readRuns {
		return report
	}

	var previous *hermesCursor
	if len(cursor) > 0 {
		var c hermesCursor
		if json.Unmarshal(cursor, &c) == nil && !c.FinishedAt.IsZero() {
			previous = &c
		}
	}

	if previous == nil {
		// With no record of what was reported, only each job's latest run.
		for _, run := range latest {
			if r, ok := hermesRunOf(run); ok && known[run.JobID] {
				report.Runs = append(report.Runs, r)
			}
		}
	} else {
		reported := map[string]bool{}
		for _, id := range previous.IDs {
			reported[id] = true
		}
		for _, run := range executions {
			if run.Finished.Before(previous.FinishedAt) || (run.Finished.Equal(previous.FinishedAt) && reported[run.ID]) {
				continue
			}
			// Runs of a job no longer listed are left out.
			if r, ok := hermesRunOf(run); ok && known[run.JobID] {
				report.Runs = append(report.Runs, r)
			}
		}
	}
	sort.Slice(report.Runs, func(i, j int) bool {
		if !report.Runs[i].StartedAt.Equal(report.Runs[j].StartedAt) {
			return report.Runs[i].StartedAt.Before(report.Runs[j].StartedAt)
		}
		return report.Runs[i].ID < report.Runs[j].ID
	})

	// The cursor moves to the newest finished run, reported or not.
	next := hermesCursor{}
	if previous != nil {
		next = *previous
	}
	for _, run := range executions {
		switch {
		case run.Finished.After(next.FinishedAt):
			next = hermesCursor{FinishedAt: run.Finished, IDs: []string{run.ID}}
		case run.Finished.Equal(next.FinishedAt) && !contains(next.IDs, run.ID):
			next.IDs = append(next.IDs, run.ID)
		}
	}
	if !next.FinishedAt.IsZero() {
		sort.Strings(next.IDs)
		report.Cursor, _ = json.Marshal(next)
	}
	return report
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func hermesDefinition(job hermesJob, timezone string) adapter.AutomationJob {
	name := adapter.CleanAutomationName(job.Name)
	if name == "" {
		name = job.ID
	}
	enabled := (job.Enabled == nil || *job.Enabled) && job.State != "paused"
	display := adapter.CleanAutomationName(job.Schedule.Display)

	schedule := adapter.AutomationSchedule{Kind: adapter.ScheduleOther, Display: display}
	switch job.Schedule.Kind {
	case "cron":
		tz := adapter.CleanTimezone(job.Timezone)
		if tz == "" {
			tz = timezone
		}
		schedule = adapter.AutomationSchedule{Kind: adapter.ScheduleCron, Expr: strings.TrimSpace(job.Schedule.Expr), TZ: tz, Display: display}
	case "interval":
		if job.Schedule.Minutes > 0 {
			schedule = adapter.AutomationSchedule{Kind: adapter.ScheduleEvery, EveryMs: int64(job.Schedule.Minutes * 60000), Display: display}
		}
	case "once":
		schedule = adapter.AutomationSchedule{Kind: adapter.ScheduleAt, Display: display}
	}
	return adapter.AutomationJob{ID: job.ID, Name: name, Enabled: enabled, Schedule: schedule}
}

func hermesStateOf(job hermesJob, last execution, hasLast bool) adapter.AutomationState {
	state := adapter.AutomationState{ID: job.ID, ConsecutiveErrors: int64(job.FailureStreak)}
	if state.ConsecutiveErrors < 0 {
		state.ConsecutiveErrors = 0
	}
	if job.NextRunAt != nil {
		if t, ok := parseHermesTime(*job.NextRunAt); ok {
			state.NextRunAt = &t
		}
	}
	if job.LastRunAt != nil {
		if t, ok := parseHermesTime(*job.LastRunAt); ok {
			state.LastRunAt = &t
		}
	}
	if hasLast {
		state.LastDurationMs = duration(last)
		state.LastDelivered = hermesDelivered(last.Delivery)
	}
	if job.LastStatus == nil {
		return state
	}
	switch *job.LastStatus {
	case "ok", "delivery_queued":
		state.LastStatus = adapter.Ptr(adapter.RunOK)
	case "delivery_failed":
		// The run worked; sending its result did not.
		state.LastStatus = adapter.Ptr(adapter.RunOK)
		state.LastDelivered = adapter.Ptr(false)
		state.ErrorCode = adapter.Ptr(adapter.ErrorDelivery)
	case "error":
		state.LastStatus = adapter.Ptr(adapter.RunError)
		text := ""
		if job.LastError != nil {
			text = *job.LastError
		}
		state.ErrorCode = adapter.Ptr(adapter.ClassifyError(text))
	case "cancelled":
		state.LastStatus = adapter.Ptr(adapter.RunSkipped)
	case "missed":
		state.LastStatus = adapter.Ptr(adapter.RunMissed)
	}
	return state
}

func hermesRunOf(run execution) (adapter.AutomationRun, bool) {
	r := adapter.AutomationRun{ID: run.ID, JobID: run.JobID, Delivered: hermesDelivered(run.Delivery)}
	switch run.Status {
	case "completed":
		r.Status = adapter.RunOK
		if run.Delivery == "failed" {
			r.ErrorCode = adapter.Ptr(adapter.ErrorDelivery)
		}
	case "failed":
		r.Status = adapter.RunError
		r.ErrorCode = adapter.Ptr(adapter.ClassifyError(run.Error))
	case "cancelled":
		r.Status = adapter.RunSkipped
	case "missed":
		r.Status = adapter.RunMissed
	default:
		return adapter.AutomationRun{}, false
	}
	if run.Started != nil {
		r.StartedAt = *run.Started
	} else {
		r.StartedAt = run.Finished
	}
	if run.Status != "missed" {
		r.DurationMs = duration(run)
	}
	return r, true
}

func duration(run execution) *int64 {
	if run.Started == nil || run.Status == "missed" {
		return nil
	}
	ms := run.Finished.Sub(*run.Started).Milliseconds()
	if ms < 0 {
		return nil
	}
	return &ms
}

// hermesDelivered reads the ledger's delivery outcome. Only "delivered" and
// "failed" say for certain whether the result arrived; results kept on the
// machine ("suppressed"), still queued, or with nowhere to go say nothing.
func hermesDelivered(outcome string) *bool {
	switch outcome {
	case "delivered":
		return adapter.Ptr(true)
	case "failed":
		return adapter.Ptr(false)
	}
	return nil
}

// parseHermesTime reads the ISO 8601 times Hermes writes. A time without an
// offset is read as UTC.
func parseHermesTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// configTimezone is the "timezone" setting in Hermes's config.yaml, which
// Hermes reads cron expressions in. Empty when it is not set, in which case
// Hermes uses the machine's own time zone.
func (a *Adapter) configTimezone() string {
	data, err := os.ReadFile(filepath.Join(a.home, "config.yaml"))
	if err != nil {
		return ""
	}
	var config struct {
		Timezone string `yaml:"timezone"`
	}
	if yaml.Unmarshal(data, &config) != nil {
		return ""
	}
	return adapter.CleanTimezone(config.Timezone)
}
