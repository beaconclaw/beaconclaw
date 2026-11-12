package heartbeat

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

var collected = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func sampleReport() adapter.AutomationReport {
	next := time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC)
	return adapter.AutomationReport{
		Jobs: []adapter.AutomationJob{
			{ID: "b-job", Name: "Weekly digest", Enabled: true, Schedule: adapter.AutomationSchedule{Kind: adapter.ScheduleCron, Expr: "0 9 * * 1", TZ: "Europe/Paris"}},
			{ID: "a-job", Name: "Inbox sweep", Enabled: false, Schedule: adapter.AutomationSchedule{Kind: adapter.ScheduleEvery, EveryMs: 900000, Display: "every 15m"}},
		},
		States: []adapter.AutomationState{
			{ID: "b-job", NextRunAt: &next},
			{ID: "a-job", ConsecutiveErrors: 2, LastStatus: adapter.Ptr(adapter.RunError), ErrorCode: adapter.Ptr(adapter.ErrorTimeout)},
		},
		Runs: []adapter.AutomationRun{
			{ID: "a-job:2000", JobID: "a-job", StartedAt: time.UnixMilli(2000), Status: adapter.RunError, ErrorCode: adapter.Ptr(adapter.ErrorTimeout)},
			{ID: "a-job:1000", JobID: "a-job", StartedAt: time.UnixMilli(1000), Status: adapter.RunOK},
		},
	}
}

func TestDefinitionsHashChangesOnlyWithDefinitions(t *testing.T) {
	base := NewAutomations(sampleReport(), false, collected)
	if len(base.DefinitionsHash) != 64 {
		t.Fatalf("hash %q is not SHA-256 hex", base.DefinitionsHash)
	}

	same := sampleReport()
	// State, runs and the order jobs are listed in do not change the hash.
	same.Jobs[0], same.Jobs[1] = same.Jobs[1], same.Jobs[0]
	same.States[0].ConsecutiveErrors = 9
	same.Runs = nil
	if got := NewAutomations(same, false, collected.Add(time.Hour)).DefinitionsHash; got != base.DefinitionsHash {
		t.Error("the hash changed although no definition did")
	}

	changes := map[string]func(*adapter.AutomationReport){
		"name":     func(r *adapter.AutomationReport) { r.Jobs[0].Name = "Renamed" },
		"enabled":  func(r *adapter.AutomationReport) { r.Jobs[1].Enabled = true },
		"schedule": func(r *adapter.AutomationReport) { r.Jobs[0].Schedule.Expr = "0 10 * * 1" },
		"tz":       func(r *adapter.AutomationReport) { r.Jobs[0].Schedule.TZ = "UTC" },
		"added": func(r *adapter.AutomationReport) {
			r.Jobs = append(r.Jobs, adapter.AutomationJob{ID: "c-job", Name: "New", Schedule: adapter.AutomationSchedule{Kind: adapter.ScheduleAt}})
		},
		"removed": func(r *adapter.AutomationReport) { r.Jobs = r.Jobs[:1] },
	}
	for what, change := range changes {
		r := sampleReport()
		change(&r)
		if NewAutomations(r, false, collected).DefinitionsHash == base.DefinitionsHash {
			t.Errorf("changing the %s did not change the hash", what)
		}
	}
}

func TestHiddenNames(t *testing.T) {
	got := NewAutomations(sampleReport(), true, collected)
	if len(*got.Jobs) != 2 || (*got.Jobs)[0].ID != "a-job" || (*got.Jobs)[0].Name != "Automation 1" || (*got.Jobs)[1].Name != "Automation 2" {
		t.Fatalf("names not replaced in order of id: %+v", *got.Jobs)
	}
	data, _ := json.Marshal(got)
	for _, name := range []string{"Weekly digest", "Inbox sweep"} {
		if strings.Contains(string(data), name) {
			t.Errorf("hidden name %q was sent: %s", name, data)
		}
	}

	// The hash is of the names as sent: renaming a job changes nothing.
	renamed := sampleReport()
	renamed.Jobs[0].Name = "Something else"
	if NewAutomations(renamed, true, collected).DefinitionsHash != got.DefinitionsHash {
		t.Error("a hidden rename changed the hash")
	}
	if NewAutomations(sampleReport(), false, collected).DefinitionsHash == got.DefinitionsHash {
		t.Error("shown and hidden names give the same hash")
	}
}

func TestJobsAreOmittedWhenUnchanged(t *testing.T) {
	first := NewAutomations(sampleReport(), false, collected)
	first.OmitUnchangedJobs("")
	data, _ := json.Marshal(first)
	if !strings.Contains(string(data), `"jobs":[`) {
		t.Fatalf("the first report has no jobs: %s", data)
	}

	second := NewAutomations(sampleReport(), false, collected)
	second.OmitUnchangedJobs(first.DefinitionsHash)
	data, _ = json.Marshal(second)
	if strings.Contains(string(data), `"jobs"`) {
		t.Fatalf("unchanged jobs were sent again: %s", data)
	}
	if !strings.Contains(string(data), `"states":[`) || !strings.Contains(string(data), `"runs":[`) {
		t.Fatalf("states and runs must always be present: %s", data)
	}

	// No jobs at all is an empty list, not a missing one.
	none := NewAutomations(adapter.AutomationReport{}, false, collected)
	none.OmitUnchangedJobs(first.DefinitionsHash)
	data, _ = json.Marshal(none)
	if !strings.Contains(string(data), `"jobs":[]`) || !strings.Contains(string(data), `"states":[]`) || !strings.Contains(string(data), `"runs":[]`) {
		t.Fatalf("an empty job list must be sent as []: %s", data)
	}
}

func TestAutomationsWireShape(t *testing.T) {
	got := NewAutomations(sampleReport(), false, collected)
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		CollectedAt string                       `json:"collected_at"`
		Hash        string                       `json:"definitions_hash"`
		Jobs        []map[string]any             `json:"jobs"`
		States      []map[string]json.RawMessage `json:"states"`
		Runs        []map[string]json.RawMessage `json:"runs"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.CollectedAt != "2026-01-02T03:04:05Z" {
		t.Errorf("collected_at = %q", wire.CollectedAt)
	}
	schedule := wire.Jobs[0]["schedule"].(map[string]any)
	if schedule["kind"] != "every" || schedule["every_ms"] != float64(900000) || schedule["display"] != "every 15m" || schedule["expr"] != nil {
		t.Errorf("every schedule = %v", schedule)
	}
	state := wire.States[1] // b-job: nothing known but the next run
	for _, field := range []string{"last_run_at", "last_status", "last_duration_ms", "last_delivered", "error_code"} {
		if string(state[field]) != "null" {
			t.Errorf("state %s = %s, want null", field, state[field])
		}
	}
	if string(state["next_run_at"]) != `"2026-01-02T04:00:00Z"` || string(state["consecutive_errors"]) != "0" {
		t.Errorf("state = %s", data)
	}
	if len(wire.Runs) != 2 || string(wire.Runs[0]["id"]) != `"a-job:1000"` {
		t.Errorf("runs are not oldest first: %s", data)
	}
	for _, field := range []string{"id", "job_id", "started_at", "duration_ms", "status", "delivered", "error_code"} {
		if _, ok := wire.Runs[1][field]; !ok {
			t.Errorf("run has no %s", field)
		}
	}
}

func TestRunsAreLimitedMergedAndDeduplicated(t *testing.T) {
	var runs []adapter.AutomationRun
	for i := 0; i < MaxAutomationRuns+50; i++ {
		runs = append(runs, adapter.AutomationRun{ID: fmt.Sprintf("r%d", i), JobID: "a", StartedAt: time.UnixMilli(int64(1000 + i)), Status: adapter.RunOK})
	}
	got := NewAutomations(adapter.AutomationReport{Runs: runs}, false, collected)
	if len(got.Runs) != MaxAutomationRuns || got.Runs[0].ID != "r50" || got.Runs[len(got.Runs)-1].ID != fmt.Sprintf("r%d", MaxAutomationRuns+49) {
		t.Fatalf("want the newest %d runs, oldest first; got %d from %s to %s", MaxAutomationRuns, len(got.Runs), got.Runs[0].ID, got.Runs[len(got.Runs)-1].ID)
	}

	older := NewAutomations(adapter.AutomationReport{Runs: []adapter.AutomationRun{{ID: "x", StartedAt: time.UnixMilli(1)}}}, false, collected)
	newer := NewAutomations(adapter.AutomationReport{Runs: []adapter.AutomationRun{{ID: "x", StartedAt: time.UnixMilli(1)}, {ID: "y", StartedAt: time.UnixMilli(2)}}}, false, collected.Add(time.Minute))
	merged := older.Merge(newer)
	if len(merged.Runs) != 2 || merged.Runs[0].ID != "x" || merged.Runs[1].ID != "y" || !merged.CollectedAt.Equal(newer.CollectedAt) {
		t.Fatalf("merged = %+v", merged)
	}
}
