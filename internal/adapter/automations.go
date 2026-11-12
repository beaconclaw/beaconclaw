package adapter

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// CapAutomations: the adapter can list the runtime's scheduled jobs and their
// runs. Such an adapter also implements Automationer.
const CapAutomations Capability = "automations"

// Schedule kinds, in terms that fit any runtime.
const (
	ScheduleCron  = "cron"
	ScheduleEvery = "every"
	ScheduleAt    = "at"
	ScheduleOther = "other"
)

// Run outcomes, in terms that fit any runtime.
const (
	RunOK      = "ok"
	RunError   = "error"
	RunSkipped = "skipped"
	RunMissed  = "missed"
)

// Error codes. An error's text stays on the machine; only one of these short
// codes is sent.
const (
	ErrorRateLimit = "rate_limit"
	ErrorTimeout   = "timeout"
	ErrorAuth      = "auth"
	ErrorDelivery  = "delivery"
	ErrorModel     = "model"
	ErrorScript    = "script"
	ErrorOther     = "error"
)

// AutomationSchedule says when a job runs. It never carries what the job does.
type AutomationSchedule struct {
	// Kind is one of the Schedule kinds above.
	Kind string `json:"kind"`
	// Expr is the cron expression, for a cron schedule only.
	Expr string `json:"expr,omitempty"`
	// EveryMs is the interval, for an "every" schedule only.
	EveryMs int64 `json:"every_ms,omitempty"`
	// TZ is the time zone the schedule is read in, when the runtime says.
	TZ string `json:"tz,omitempty"`
	// Display is the runtime's own short description of the schedule, such as
	// "every 15m", when it has one.
	Display string `json:"display,omitempty"`
}

// AutomationJob is one scheduled job's definition: its name and when it runs.
// Its instructions, model, options and delivery target are never included.
type AutomationJob struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Enabled  bool               `json:"enabled"`
	Schedule AutomationSchedule `json:"schedule"`
}

// AutomationState is one job's current state. A nil field is not known.
type AutomationState struct {
	ID         string     `json:"id"`
	NextRunAt  *time.Time `json:"next_run_at"`
	LastRunAt  *time.Time `json:"last_run_at"`
	LastStatus *string    `json:"last_status"`
	// LastDurationMs is how long the last run took.
	LastDurationMs    *int64 `json:"last_duration_ms"`
	ConsecutiveErrors int64  `json:"consecutive_errors"`
	// LastDelivered: the last run's result reached where it was sent.
	LastDelivered *bool `json:"last_delivered"`
	// ErrorCode classifies the last run's error; see ClassifyError.
	ErrorCode *string `json:"error_code"`
}

// AutomationRun is one finished run of a job.
type AutomationRun struct {
	// ID is the runtime's own id for the run, or one built from the job's id
	// and the run's start, so the same run always has the same id.
	ID         string    `json:"id"`
	JobID      string    `json:"job_id"`
	StartedAt  time.Time `json:"started_at"`
	DurationMs *int64    `json:"duration_ms"`
	// Status is one of the Run outcomes above.
	Status    string  `json:"status"`
	Delivered *bool   `json:"delivered"`
	ErrorCode *string `json:"error_code"`
}

// AutomationReport is what an adapter found about the runtime's scheduled
// jobs.
type AutomationReport struct {
	Jobs   []AutomationJob
	States []AutomationState
	// Runs are the runs that finished since the cursor the adapter was given.
	Runs []AutomationRun
	// Cursor is the adapter's own record of which runs it has reported. The
	// agent keeps it, on disk too, and hands it back on the next call. Its
	// shape belongs to the adapter.
	Cursor []byte
}

// Automationer is implemented by an adapter with CapAutomations. Listing may
// take several seconds, so it is called rarely and never on the heartbeat
// path. It reports false when it could not find out, for instance because the
// runtime has no scheduler or did not answer; nothing is then sent.
//
// cursor is the Cursor of the last report the agent accepted, or nil. With no
// record of a job, an adapter reports only that job's latest run.
type Automationer interface {
	Automations(ctx context.Context, cursor []byte) (AutomationReport, bool)
}

var (
	rateLimitPattern = regexp.MustCompile(`rate[ _-]?limit|\b429\b|too many requests`)
	timeoutPattern   = regexp.MustCompile(`timeout|timed out|deadline`)
	authPattern      = regexp.MustCompile(`\b401\b|\b403\b|unauthori|forbidden|auth|credential|api[ _-]?key`)
	modelPattern     = regexp.MustCompile(`model|overloaded|\b503\b|upstream`)
	scriptPattern    = regexp.MustCompile(`exit status|exit code|non-zero|nonzero|script`)
)

// ClassifyError turns an error's text into a short code. It runs on the
// machine; the text itself is never sent.
func ClassifyError(text string) string {
	text = strings.ToLower(text)
	switch {
	case rateLimitPattern.MatchString(text):
		return ErrorRateLimit
	case timeoutPattern.MatchString(text):
		return ErrorTimeout
	case authPattern.MatchString(text):
		return ErrorAuth
	case modelPattern.MatchString(text):
		return ErrorModel
	case scriptPattern.MatchString(text):
		return ErrorScript
	default:
		return ErrorOther
	}
}

// maxAutomationName is the longest job name kept, in characters.
const maxAutomationName = 120

// CleanAutomationName makes a job's name safe to send: one line, no control
// characters, and not too long. An empty result means there is no usable name.
func CleanAutomationName(name string) string {
	var b strings.Builder
	count := 0
	for _, r := range strings.TrimSpace(name) {
		if count >= maxAutomationName {
			break
		}
		if unicode.IsControl(r) {
			r = ' '
		}
		b.WriteRune(r)
		count++
	}
	return strings.TrimSpace(b.String())
}

// CleanTimezone keeps a time zone name that looks like one, such as
// "America/New_York" or "UTC", and returns "" for anything else.
func CleanTimezone(tz string) string {
	tz = strings.TrimSpace(tz)
	if tz == "" || len(tz) > 64 {
		return ""
	}
	for _, r := range tz {
		if !(r == '/' || r == '_' || r == '-' || r == '+' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
			return ""
		}
	}
	return tz
}

// AutomationIDPattern is what a job's id must look like to be reported. It also
// keeps an id from being read as an option when it is passed to a command.
var AutomationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// Ptr returns a pointer to v, for the optional fields above.
func Ptr[T any](v T) *T { return &v }
