package openclaw

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

const (
	// Listing jobs or one job's runs normally takes a few seconds.
	cronTimeout = 30 * time.Second
	// A job that may run more often than this can run more than once between
	// two collections, so its run history is asked for. It is twice the
	// agent's collection period, to allow for timing.
	frequentBelow = 10 * time.Minute
	// Runs asked for per job.
	cronRunsLimit = 20
	// At most this many jobs have their history asked for in one collection,
	// so a long list of frequent jobs cannot hold collection up for minutes.
	maxHistoryCalls = 10
)

// cronJob is the part of a job in "openclaw cron list --json" read here. The
// job's payload (its message or command), model, delivery target and error
// text are not decoded.
type cronJob struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Enabled     *bool  `json:"enabled"`
	Schedule    struct {
		Kind    string  `json:"kind"`
		Expr    string  `json:"expr"`
		TZ      string  `json:"tz"`
		EveryMs float64 `json:"everyMs"`
	} `json:"schedule"`
	State cronState `json:"state"`
	// Older versions put these beside the state as well; the state wins.
	NextRunAtMs   float64 `json:"nextRunAtMs"`
	LastRunAtMs   float64 `json:"lastRunAtMs"`
	LastRunStatus string  `json:"lastRunStatus"`
}

type cronState struct {
	NextRunAtMs        float64  `json:"nextRunAtMs"`
	LastRunAtMs        float64  `json:"lastRunAtMs"`
	LastRunStatus      string   `json:"lastRunStatus"`
	LastDurationMs     *float64 `json:"lastDurationMs"`
	LastDeliveryStatus string   `json:"lastDeliveryStatus"`
	ConsecutiveErrors  float64  `json:"consecutiveErrors"`
	// LastError is read only to be classified here. It is never sent.
	LastError string `json:"lastError"`
}

// cronRun is one entry of "openclaw cron runs <id> --json". Its summary,
// session and delivery details are not decoded.
type cronRun struct {
	JobID          string   `json:"jobId"`
	Status         string   `json:"status"`
	RunAtMs        float64  `json:"runAtMs"`
	DurationMs     *float64 `json:"durationMs"`
	DeliveryStatus string   `json:"deliveryStatus"`
	// Error is read only to be classified here. It is never sent.
	Error string `json:"error"`
}

// cronCursor records, per job, the start of the newest run reported.
type cronCursor struct {
	LastRunAtMs map[string]int64 `json:"last_run_at_ms"`
}

// Automations lists OpenClaw's scheduled jobs with "openclaw cron list" and
// works out which runs finished since the last report from each job's last
// run time. For a job that runs more often than the agent collects, it also
// asks for that job's recent runs, so runs in between are not missed.
func (a *Adapter) Automations(ctx context.Context, cursor []byte) (adapter.AutomationReport, bool) {
	output, err := runCron(ctx, "cron", "list", "--json", "--all")
	if err != nil {
		return adapter.AutomationReport{}, false
	}
	jobs, ok := parseCronList(output)
	if !ok {
		return adapter.AutomationReport{}, false
	}
	history := func(id string) ([]cronRun, bool) {
		output, err := runCron(ctx, "cron", "runs", id, "--limit", strconv.Itoa(cronRunsLimit), "--json")
		if err != nil {
			return nil, false
		}
		return parseCronRuns(output)
	}
	report := buildCronReport(jobs, cursor, history)
	if ctx.Err() != nil {
		return adapter.AutomationReport{}, false
	}
	return report, true
}

func runCron(ctx context.Context, args ...string) ([]byte, error) {
	var lastErr error = exec.ErrNotFound
	for _, candidate := range commandCandidates() {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, cronTimeout)
		cmd := exec.CommandContext(ctx, candidate, args...)
		cmd.Env = commandEnv()
		var stdout bytes.Buffer
		cmd.Stdout = &limitedWriter{w: &stdout, n: maxCommandOutput}
		err := cmd.Run()
		cancel()
		if err == nil {
			return stdout.Bytes(), nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// parseCronList reads "openclaw cron list --json". It reports false when the
// output has no job list, as when the gateway could not be reached.
func parseCronList(output []byte) ([]cronJob, bool) {
	start := bytes.IndexByte(output, '{')
	if start < 0 {
		return nil, false
	}
	var listing struct {
		Jobs *[]cronJob `json:"jobs"`
	}
	if err := json.NewDecoder(bytes.NewReader(output[start:])).Decode(&listing); err != nil || listing.Jobs == nil {
		return nil, false
	}
	jobs := []cronJob{}
	seen := map[string]bool{}
	for _, job := range *listing.Jobs {
		if !adapter.AutomationIDPattern.MatchString(job.ID) || seen[job.ID] {
			continue
		}
		seen[job.ID] = true
		if job.State.NextRunAtMs == 0 {
			job.State.NextRunAtMs = job.NextRunAtMs
		}
		if job.State.LastRunAtMs == 0 {
			job.State.LastRunAtMs = job.LastRunAtMs
		}
		if job.State.LastRunStatus == "" {
			job.State.LastRunStatus = job.LastRunStatus
		}
		jobs = append(jobs, job)
	}
	if len(jobs) > maxInventoryItems {
		jobs = jobs[:maxInventoryItems]
	}
	return jobs, true
}

// parseCronRuns reads "openclaw cron runs <id> --json".
func parseCronRuns(output []byte) ([]cronRun, bool) {
	start := bytes.IndexByte(output, '{')
	if start < 0 {
		return nil, false
	}
	var listing struct {
		Entries *[]cronRun `json:"entries"`
	}
	if err := json.NewDecoder(bytes.NewReader(output[start:])).Decode(&listing); err != nil || listing.Entries == nil {
		return nil, false
	}
	return *listing.Entries, true
}

// buildCronReport turns the job list into a report. history is asked for a
// job's recent runs, and only for a frequent job whose last run changed.
func buildCronReport(jobs []cronJob, cursor []byte, history func(id string) ([]cronRun, bool)) adapter.AutomationReport {
	var previous cronCursor
	_ = json.Unmarshal(cursor, &previous)
	next := cronCursor{LastRunAtMs: map[string]int64{}}

	report := adapter.AutomationReport{
		Jobs:   []adapter.AutomationJob{},
		States: []adapter.AutomationState{},
		Runs:   []adapter.AutomationRun{},
	}
	historyCalls := 0
	for _, job := range jobs {
		report.Jobs = append(report.Jobs, cronDefinition(job))
		report.States = append(report.States, cronStateOf(job))

		last := int64(job.State.LastRunAtMs)
		seen, known := previous.LastRunAtMs[job.ID]
		if last <= 0 {
			if known {
				next.LastRunAtMs[job.ID] = seen
			}
			continue
		}
		next.LastRunAtMs[job.ID] = last
		if known && last <= seen {
			continue
		}

		latest, latestOK := cronLatestRun(job)
		runs := []adapter.AutomationRun{}
		if known && historyCalls < maxHistoryCalls && isFrequent(job) {
			historyCalls++
			if entries, ok := history(job.ID); ok {
				for _, entry := range entries {
					if int64(entry.RunAtMs) <= seen {
						continue
					}
					if run, ok := cronRunOf(job.ID, entry); ok {
						runs = append(runs, run)
					}
				}
			}
		}
		if latestOK && !containsRun(runs, latest.ID) {
			runs = append(runs, latest)
		}
		// History may already hold a run newer than the list showed.
		for _, run := range runs {
			if ms := run.StartedAt.UnixMilli(); ms > next.LastRunAtMs[job.ID] {
				next.LastRunAtMs[job.ID] = ms
			}
		}
		report.Runs = append(report.Runs, runs...)
	}
	report.Cursor, _ = json.Marshal(next)
	return report
}

func containsRun(runs []adapter.AutomationRun, id string) bool {
	for _, run := range runs {
		if run.ID == id {
			return true
		}
	}
	return false
}

func cronDefinition(job cronJob) adapter.AutomationJob {
	name := adapter.CleanAutomationName(job.DisplayName)
	if name == "" {
		name = adapter.CleanAutomationName(job.Name)
	}
	if name == "" {
		name = job.ID
	}
	enabled := job.Enabled == nil || *job.Enabled
	return adapter.AutomationJob{ID: job.ID, Name: name, Enabled: enabled, Schedule: cronSchedule(job)}
}

func cronSchedule(job cronJob) adapter.AutomationSchedule {
	s := job.Schedule
	switch s.Kind {
	case "cron":
		return adapter.AutomationSchedule{Kind: adapter.ScheduleCron, Expr: strings.TrimSpace(s.Expr), TZ: adapter.CleanTimezone(s.TZ)}
	case "every":
		return adapter.AutomationSchedule{Kind: adapter.ScheduleEvery, EveryMs: int64(s.EveryMs)}
	case "at":
		return adapter.AutomationSchedule{Kind: adapter.ScheduleAt}
	default:
		// Event triggers such as "on-exit" and "stream" carry a command;
		// nothing of them is sent.
		return adapter.AutomationSchedule{Kind: adapter.ScheduleOther}
	}
}

func cronStateOf(job cronJob) adapter.AutomationState {
	st := job.State
	state := adapter.AutomationState{
		ID:                job.ID,
		NextRunAt:         msTime(st.NextRunAtMs),
		LastRunAt:         msTime(st.LastRunAtMs),
		LastDurationMs:    msDuration(st.LastDurationMs),
		ConsecutiveErrors: int64(st.ConsecutiveErrors),
		LastDelivered:     delivered(st.LastDeliveryStatus),
	}
	if status, ok := cronStatus(st.LastRunStatus); ok {
		state.LastStatus = &status
		state.ErrorCode = cronErrorCode(status, st.LastError, st.LastDeliveryStatus)
	}
	if state.ConsecutiveErrors < 0 {
		state.ConsecutiveErrors = 0
	}
	return state
}

// cronLatestRun is the job's last run, as its state describes it.
func cronLatestRun(job cronJob) (adapter.AutomationRun, bool) {
	st := job.State
	return cronRunOf(job.ID, cronRun{
		JobID:          job.ID,
		Status:         st.LastRunStatus,
		RunAtMs:        st.LastRunAtMs,
		DurationMs:     st.LastDurationMs,
		DeliveryStatus: st.LastDeliveryStatus,
		Error:          st.LastError,
	})
}

// cronRunOf turns a run into its report. OpenClaw gives a run no id of its
// own, so the id is the job's id and the run's start in ms.
func cronRunOf(jobID string, entry cronRun) (adapter.AutomationRun, bool) {
	status, ok := cronStatus(entry.Status)
	started := msTime(entry.RunAtMs)
	if !ok || started == nil || (entry.JobID != "" && entry.JobID != jobID) {
		return adapter.AutomationRun{}, false
	}
	return adapter.AutomationRun{
		ID:         jobID + ":" + strconv.FormatInt(int64(entry.RunAtMs), 10),
		JobID:      jobID,
		StartedAt:  *started,
		DurationMs: msDuration(entry.DurationMs),
		Status:     status,
		Delivered:  delivered(entry.DeliveryStatus),
		ErrorCode:  cronErrorCode(status, entry.Error, entry.DeliveryStatus),
	}, true
}

func cronStatus(status string) (string, bool) {
	switch status {
	case "ok":
		return adapter.RunOK, true
	case "error":
		return adapter.RunError, true
	case "skipped":
		return adapter.RunSkipped, true
	}
	return "", false
}

func cronErrorCode(status, errorText, deliveryStatus string) *string {
	switch {
	case status == adapter.RunError:
		return adapter.Ptr(adapter.ClassifyError(errorText))
	case status == adapter.RunOK && deliveryStatus == "not-delivered":
		return adapter.Ptr(adapter.ErrorDelivery)
	}
	return nil
}

func delivered(status string) *bool {
	switch status {
	case "delivered":
		return adapter.Ptr(true)
	case "not-delivered":
		return adapter.Ptr(false)
	}
	return nil
}

// isFrequent reports whether a job may run more often than frequentBelow.
func isFrequent(job cronJob) bool {
	switch job.Schedule.Kind {
	case "every":
		return job.Schedule.EveryMs > 0 && time.Duration(job.Schedule.EveryMs)*time.Millisecond < frequentBelow
	case "cron":
		return cronIsFrequent(job.Schedule.Expr)
	}
	return false
}

// cronIsFrequent is a simple reading of a cron expression's minute field: it
// fires more than once in frequentBelow when the field is "*", a step under
// ten ("*/5", "0-59/2"), or a list of many minutes. A six-field expression
// with seconds that are not a single number fires every minute or more.
func cronIsFrequent(expr string) bool {
	fields := strings.Fields(expr)
	var minute string
	switch len(fields) {
	case 5:
		minute = fields[0]
	case 6:
		if _, err := strconv.Atoi(fields[0]); err != nil {
			return true
		}
		minute = fields[1]
	default:
		return false
	}
	if minute == "*" {
		return true
	}
	if i := strings.Index(minute, "/"); i >= 0 {
		step, err := strconv.Atoi(minute[i+1:])
		return err == nil && step > 0 && time.Duration(step)*time.Minute < frequentBelow
	}
	return strings.Count(minute, ",") >= 6
}

func msTime(ms float64) *time.Time {
	if ms <= 0 || ms != ms {
		return nil
	}
	t := time.UnixMilli(int64(ms)).UTC()
	return &t
}

func msDuration(ms *float64) *int64 {
	if ms == nil || *ms < 0 || *ms != *ms {
		return nil
	}
	return adapter.Ptr(int64(*ms))
}
