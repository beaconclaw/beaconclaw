package heartbeat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

// MaxAutomationRuns is the most runs sent in one report. When more finished
// since the last one, the newest are kept.
const MaxAutomationRuns = 200

// Automations is the runtime's scheduled jobs: when each runs, how its last
// run went, and the runs that finished since the last report. A job's
// instructions, model, options, delivery target, error text and output are
// never part of it.
type Automations struct {
	CollectedAt time.Time `json:"collected_at"`
	// DefinitionsHash identifies the jobs' definitions as sent: id, name,
	// whether each is switched on, and its schedule.
	DefinitionsHash string `json:"definitions_hash"`
	// Jobs are the definitions. They are sent only when they changed since
	// the last report, and after a restart or reconnection; otherwise the
	// field is left out. An empty list means there are no jobs.
	Jobs   *[]adapter.AutomationJob  `json:"jobs,omitempty"`
	States []adapter.AutomationState `json:"states"`
	Runs   []adapter.AutomationRun   `json:"runs"`
}

// NewAutomations prepares an adapter's report for sending. Jobs and states
// are sorted by id. With hideNames, each job's name is replaced by
// "Automation 1", "Automation 2" and so on, in order of id. The jobs are
// always included; see OmitUnchangedJobs.
func NewAutomations(report adapter.AutomationReport, hideNames bool, collectedAt time.Time) Automations {
	jobs := append([]adapter.AutomationJob{}, report.Jobs...)
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	if hideNames {
		for i := range jobs {
			jobs[i].Name = fmt.Sprintf("Automation %d", i+1)
		}
	}

	states := append([]adapter.AutomationState{}, report.States...)
	sort.Slice(states, func(i, j int) bool { return states[i].ID < states[j].ID })

	return Automations{
		CollectedAt:     collectedAt.UTC(),
		DefinitionsHash: definitionsHash(jobs),
		Jobs:            &jobs,
		States:          states,
		Runs:            limitRuns(report.Runs),
	}
}

// Merge adds a newer report to one that has not been sent yet. The newer
// jobs and states replace the older; the runs of both are kept.
func (a Automations) Merge(newer Automations) Automations {
	newer.Runs = limitRuns(append(append([]adapter.AutomationRun{}, a.Runs...), newer.Runs...))
	return newer
}

// OmitUnchangedJobs leaves the jobs out when their definitions are the ones
// last sent.
func (a *Automations) OmitUnchangedJobs(lastSentHash string) {
	if a.DefinitionsHash == lastSentHash {
		a.Jobs = nil
	}
}

// limitRuns sorts runs oldest first, drops repeats and keeps the newest
// MaxAutomationRuns.
func limitRuns(runs []adapter.AutomationRun) []adapter.AutomationRun {
	out := make([]adapter.AutomationRun, 0, len(runs))
	seen := map[string]bool{}
	for _, run := range runs {
		if seen[run.ID] {
			continue
		}
		seen[run.ID] = true
		out = append(out, run)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.Before(out[j].StartedAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > MaxAutomationRuns {
		out = out[len(out)-MaxAutomationRuns:]
	}
	return out
}

// definitionsHash is the SHA-256, in hex, of the jobs' definitions as JSON, in
// the order given.
func definitionsHash(jobs []adapter.AutomationJob) string {
	data, err := json.Marshal(jobs)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
