package openclaw

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

// signalTimeout bounds each OpenClaw command run for the signals.
const signalTimeout = 60 * time.Second

// scheduledBackupKey marks the scheduled job "openclaw backup enable" creates.
const scheduledBackupKey = "openclaw-backup-scheduled"

// pairingChannelPattern is what OpenClaw accepts as a chat service's name for
// pairing. It also keeps a name from being read as an option.
var pairingChannelPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// runFunc runs "openclaw <args>" and returns what it printed on its standard
// output, also when it exits with an error: several commands print a JSON
// answer and then exit non-zero to say it holds findings.
type runFunc func(ctx context.Context, args ...string) ([]byte, error)

// Signals runs OpenClaw's own read-only status commands and keeps counts,
// dates, provider names, kinds of chat service, versions and short codes from
// what they print. Each part is read on its own; one whose command fails, or
// whose answer cannot be read, is left out.
func (a *Adapter) Signals(ctx context.Context) (adapter.SignalReport, bool) {
	var channels []string
	if config := readConfig(); config != nil {
		for _, c := range connectionsFromConfig(config) {
			if c.Kind == kindChannel {
				channels = append(channels, c.Name)
			}
		}
	}
	report := collectSignals(ctx, runSignalCommand, a.prober.getVersion(), channels, time.Now())
	return report, !report.Empty()
}

// collectSignals reads each part with the commands run gives it.
func collectSignals(ctx context.Context, run runFunc, current string, channels []string, now time.Time) adapter.SignalReport {
	report := adapter.SignalReport{}
	output := func(args ...string) ([]byte, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return run(ctx, args...)
	}

	if out, _ := output("models", "status", "--json"); out != nil {
		if credentials, ok := parseModelsStatus(out, now); ok {
			report.Credentials = &credentials
		}
	}
	if out, _ := output("approvals", "pending", "--json"); out != nil {
		if approvals, ok := parseApprovals(out); ok {
			report.Approvals = &approvals
		}
	}
	if pairing, ok := collectPairing(output, channels); ok {
		report.Pairing = &pairing
	}
	if out, err := output("cron", "list", "--json", "--all"); err == nil {
		if backups, ok := parseBackups(out); ok {
			report.Backups = &backups
		}
	}
	if out, _ := output("update", "status", "--json"); out != nil {
		if update, ok := parseUpdateStatus(out, current); ok {
			report.Update = &update
		}
	}

	security := adapter.SecuritySignal{}
	if out, _ := output("secrets", "audit", "--json"); out != nil {
		if secrets, ok := parseSecretsAudit(out); ok {
			security.Secrets = &secrets
		}
	}
	if out, _ := output("security", "audit", "--json"); out != nil {
		if audit, ok := parseSecurityAudit(out); ok {
			security.Audit = &audit
		}
	}
	if security.Secrets != nil || security.Audit != nil {
		report.Security = &security
	}

	if out, _ := output("doctor", "--lint", "--json"); out != nil {
		if doctor, ok := parseDoctorLint(out); ok {
			report.Doctor = &doctor
		}
	}
	return report
}

// runSignalCommand runs one OpenClaw command with its own time limit. Its
// output is read here and never sent as it is.
func runSignalCommand(ctx context.Context, args ...string) ([]byte, error) {
	var lastErr error = exec.ErrNotFound
	for _, candidate := range commandCandidates() {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		runCtx, cancel := context.WithTimeout(ctx, signalTimeout)
		cmd := exec.CommandContext(runCtx, candidate, args...)
		cmd.Env = append(commandEnv(), "NO_COLOR=1")
		var stdout bytes.Buffer
		cmd.Stdout = &limitedWriter{w: &stdout, n: maxCommandOutput}
		err := cmd.Run()
		late := runCtx.Err() != nil
		cancel()
		if late {
			return nil, context.DeadlineExceeded
		}
		var exit *exec.ExitError
		if err == nil || errors.As(err, &exit) {
			// The command ran. A non-zero exit may still come with an answer.
			return stdout.Bytes(), err
		}
		lastErr = err
	}
	return nil, lastErr
}

// decodeJSON reads the first JSON object in output that starts a line and
// that accept takes, so a notice or log line printed before the answer does no
// harm. Each candidate is decoded into a fresh value.
func decodeJSON[T any](output []byte, accept func(*T) bool) (T, bool) {
	for i := 0; i < len(output); i++ {
		if output[i] != '{' || (i > 0 && output[i-1] != '\n') {
			continue
		}
		var value T
		if err := json.NewDecoder(bytes.NewReader(output[i:])).Decode(&value); err != nil {
			continue
		}
		if accept(&value) {
			return value, true
		}
	}
	var zero T
	return zero, false
}

// cliError reads the message of OpenClaw's JSON answer to a failed command,
// {"ok": false, "error": ...}. The message is matched here and never sent.
func cliError(output []byte) (string, bool) {
	type failure struct {
		OK    *bool           `json:"ok"`
		Error json.RawMessage `json:"error"`
	}
	found, ok := decodeJSON(output, func(f *failure) bool {
		return f.OK != nil && !*f.OK && len(f.Error) > 0
	})
	if !ok {
		return "", false
	}
	var text string
	if json.Unmarshal(found.Error, &text) == nil {
		return text, true
	}
	var object struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(found.Error, &object) == nil {
		return object.Message, true
	}
	return "", false
}

// modelsStatus is the part of "openclaw models status --json" read here. A
// profile's id, label and where it is stored, and every value and detail,
// are not decoded.
type modelsStatus struct {
	Auth *struct {
		MissingProvidersInUse []string `json:"missingProvidersInUse"`
		RuntimeAuthRoutes     []struct {
			Provider string `json:"provider"`
			Status   string `json:"status"`
		} `json:"runtimeAuthRoutes"`
		Providers []struct {
			Provider  string `json:"provider"`
			Effective struct {
				Kind string `json:"kind"`
			} `json:"effective"`
		} `json:"providers"`
		UnusableProfiles []struct {
			Provider string `json:"provider"`
		} `json:"unusableProfiles"`
		OAuth *struct {
			Profiles []struct {
				Provider  string   `json:"provider"`
				Type      string   `json:"type"`
				Status    string   `json:"status"`
				ExpiresAt *float64 `json:"expiresAt"`
			} `json:"profiles"`
			Providers []struct {
				Provider  string   `json:"provider"`
				Status    string   `json:"status"`
				ExpiresAt *float64 `json:"expiresAt"`
			} `json:"providers"`
		} `json:"oauth"`
	} `json:"auth"`
}

// parseModelsStatus works out one entry per provider: the kind of credential,
// the worst state of its credentials and the soonest expiry.
func parseModelsStatus(output []byte, now time.Time) ([]adapter.CredentialSignal, bool) {
	status, ok := decodeJSON(output, func(s *modelsStatus) bool { return s.Auth != nil })
	if !ok {
		return nil, false
	}
	auth := status.Auth

	entries := map[string]*adapter.CredentialSignal{}
	entry := func(provider string) *adapter.CredentialSignal {
		provider = adapter.CleanSignalName(provider)
		if provider == "" {
			return nil
		}
		if e, ok := entries[provider]; ok {
			return e
		}
		if len(entries) >= adapter.MaxSignalItems {
			return nil
		}
		e := &adapter.CredentialSignal{Provider: provider, Kind: "", Status: adapter.CredentialOK}
		entries[provider] = e
		return e
	}
	worsen := func(e *adapter.CredentialSignal, status string) {
		e.Status = adapter.WorseCredentialStatus(e.Status, status)
	}
	expiry := func(e *adapter.CredentialSignal, ms *float64) {
		if ms == nil || *ms <= 0 || *ms != *ms {
			return
		}
		t := time.UnixMilli(int64(*ms)).UTC()
		if e.ExpiresAt == nil || t.Before(*e.ExpiresAt) {
			e.ExpiresAt = &t
		}
	}

	// Configured providers, and how each is signed in.
	for _, p := range auth.Providers {
		e := entry(p.Provider)
		if e == nil {
			continue
		}
		switch p.Effective.Kind {
		case "env":
			e.Kind = adapter.CredentialEnv
		case "missing":
			worsen(e, adapter.CredentialMissing)
		case "profiles":
			// The profiles below say which kind.
		default:
			if e.Kind == "" {
				e.Kind = adapter.CredentialOther
			}
		}
	}

	// Each stored credential, with its expiry.
	hasProfileKind := map[string]string{}
	if auth.OAuth != nil {
		for _, p := range auth.OAuth.Profiles {
			e := entry(p.Provider)
			if e == nil {
				continue
			}
			kind := credentialKind(p.Type)
			if rank(kind) > rank(hasProfileKind[e.Provider]) {
				hasProfileKind[e.Provider] = kind
			}
			expiry(e, p.ExpiresAt)
		}
		// OpenClaw's own judgement per provider, over the credentials it
		// would use.
		judged := map[string]bool{}
		for _, p := range auth.OAuth.Providers {
			e := entry(p.Provider)
			if e == nil {
				continue
			}
			judged[e.Provider] = true
			switch p.Status {
			case "expired":
				worsen(e, adapter.CredentialExpired)
			case "missing":
				worsen(e, adapter.CredentialFailing)
			}
			expiry(e, p.ExpiresAt)
		}
		// Without that judgement, each credential's own state.
		for _, p := range auth.OAuth.Profiles {
			e := entry(p.Provider)
			if e == nil || judged[e.Provider] {
				continue
			}
			switch p.Status {
			case "expired":
				worsen(e, adapter.CredentialExpired)
			case "missing":
				worsen(e, adapter.CredentialFailing)
			}
		}
	}
	for provider, kind := range hasProfileKind {
		if e := entries[provider]; e != nil && e.Kind != adapter.CredentialEnv {
			e.Kind = kind
		}
	}

	for _, p := range auth.UnusableProfiles {
		if e := entry(p.Provider); e != nil {
			worsen(e, adapter.CredentialFailing)
		}
	}
	for _, route := range auth.RuntimeAuthRoutes {
		if route.Status == "usable" {
			continue
		}
		if e := entry(route.Provider); e != nil {
			worsen(e, adapter.CredentialFailing)
		}
	}
	for _, provider := range auth.MissingProvidersInUse {
		if e := entry(provider); e != nil {
			worsen(e, adapter.CredentialMissing)
		}
	}

	list := make([]adapter.CredentialSignal, 0, len(entries))
	for _, e := range entries {
		if e.Kind == "" {
			e.Kind = adapter.CredentialOther
		}
		// An expiry is kept for a sign-in that expires, and judged here: past
		// is expired, within a week is expiring.
		if e.Kind != adapter.CredentialOAuth && e.Kind != adapter.CredentialToken {
			e.ExpiresAt = nil
		}
		if e.ExpiresAt != nil {
			switch left := e.ExpiresAt.Sub(now); {
			case left <= 0:
				worsen(e, adapter.CredentialExpired)
			case left <= adapter.CredentialExpiringWithin:
				worsen(e, adapter.CredentialExpiring)
			}
		}
		list = append(list, *e)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Provider < list[j].Provider })
	return list, true
}

func credentialKind(t string) string {
	switch t {
	case "oauth":
		return adapter.CredentialOAuth
	case "token":
		return adapter.CredentialToken
	case "api_key":
		return adapter.CredentialAPIKey
	}
	return adapter.CredentialOther
}

// rank prefers the kind of credential that can expire when a provider has
// several.
func rank(kind string) int {
	switch kind {
	case adapter.CredentialOAuth:
		return 4
	case adapter.CredentialToken:
		return 3
	case adapter.CredentialAPIKey:
		return 2
	case adapter.CredentialOther:
		return 1
	}
	return 0
}

// parseApprovals reads "openclaw approvals pending --json": how many are
// waiting and when the oldest was asked for. Nothing about what is to be
// approved is decoded.
func parseApprovals(output []byte) (adapter.ApprovalsSignal, bool) {
	type pending struct {
		Approvals *[]struct {
			CreatedAtMs *float64 `json:"createdAtMs"`
		} `json:"approvals"`
	}
	listing, ok := decodeJSON(output, func(p *pending) bool { return p.Approvals != nil })
	if !ok {
		return adapter.ApprovalsSignal{}, false
	}
	signal := adapter.ApprovalsSignal{Pending: len(*listing.Approvals)}
	for _, approval := range *listing.Approvals {
		if approval.CreatedAtMs == nil {
			continue
		}
		if at := msTime(*approval.CreatedAtMs); at != nil && (signal.OldestAt == nil || at.Before(*signal.OldestAt)) {
			signal.OldestAt = at
		}
	}
	return signal, true
}

// collectPairing counts pending pairing requests on each chat service set up
// in OpenClaw's config. A service that does not use pairing is skipped. With
// none set up, OpenClaw is asked once without naming one: it answers for the
// only pairing service there is, or says there is none.
func collectPairing(output func(args ...string) ([]byte, error), channels []string) ([]adapter.PairingSignal, bool) {
	names := []string{}
	seen := map[string]bool{}
	for _, name := range channels {
		if pairingChannelPattern.MatchString(name) && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > adapter.MaxSignalItems {
		names = names[:adapter.MaxSignalItems]
	}

	if len(names) == 0 {
		out, err := output("pairing", "list", "--json")
		if out == nil {
			return nil, false
		}
		if channel, pending, ok := parsePairingList(out); ok && err == nil {
			return []adapter.PairingSignal{{Channel: channel, Pending: pending}}, true
		}
		if message, ok := cliError(out); ok && strings.Contains(message, "No chat DM pairing channels are configured") {
			return []adapter.PairingSignal{}, true
		}
		return nil, false
	}

	signals := []adapter.PairingSignal{}
	for _, name := range names {
		out, err := output("pairing", "list", name, "--json")
		if out == nil {
			return nil, false
		}
		if channel, pending, ok := parsePairingList(out); ok && err == nil && channel == name {
			signals = append(signals, adapter.PairingSignal{Channel: channel, Pending: pending})
			continue
		}
		if message, ok := cliError(out); ok && strings.Contains(message, "does not support pairing") {
			continue
		}
		// One service could not be read: the counts would be incomplete.
		return nil, false
	}
	return signals, true
}

// parsePairingList reads "openclaw pairing list --json": the service and how
// many requests wait on it. Who asked is not decoded.
func parsePairingList(output []byte) (string, int, bool) {
	type requests struct {
		Channel  string             `json:"channel"`
		Requests *[]json.RawMessage `json:"requests"`
	}
	listing, ok := decodeJSON(output, func(r *requests) bool { return r.Requests != nil })
	if !ok {
		return "", 0, false
	}
	channel := adapter.CleanSignalName(listing.Channel)
	if channel == "" {
		return "", 0, false
	}
	return channel, len(*listing.Requests), true
}

// parseBackups finds, in "openclaw cron list --json --all", the job that
// "openclaw backup enable" creates for scheduled backups, and reads whether it
// is on and how its last run went. Without that job, backups are not
// scheduled. The job's command and repository are not decoded.
func parseBackups(output []byte) (adapter.BackupsSignal, bool) {
	jobs, ok := parseCronList(output)
	if !ok {
		return adapter.BackupsSignal{}, false
	}
	for _, job := range jobs {
		if job.DeclarationKey != scheduledBackupKey {
			continue
		}
		signal := adapter.BackupsSignal{
			Configured: job.Enabled == nil || *job.Enabled,
			LastAt:     msTime(job.State.LastRunAtMs),
		}
		switch job.State.LastRunStatus {
		case "ok":
			signal.LastOK = adapter.Ptr(true)
		case "error":
			signal.LastOK = adapter.Ptr(false)
		}
		return signal, true
	}
	return adapter.BackupsSignal{Configured: false}, true
}

// parseUpdateStatus reads "openclaw update status --json". It is reported
// only when OpenClaw could compare against the newest release or the git
// upstream: otherwise "no update" would be a guess. Paths, messages about the
// service and about channels are not decoded.
func parseUpdateStatus(output []byte, current string) (adapter.UpdateSignal, bool) {
	type updateStatus struct {
		Update *struct {
			InstallKind string          `json:"installKind"`
			Error       json.RawMessage `json:"error"`
			Registry    *struct {
				LatestVersion string `json:"latestVersion"`
			} `json:"registry"`
		} `json:"update"`
		Channel struct {
			Value string `json:"value"`
		} `json:"channel"`
		Availability *struct {
			Available     *bool    `json:"available"`
			LatestVersion string   `json:"latestVersion"`
			GitBehind     *float64 `json:"gitBehind"`
		} `json:"availability"`
	}
	status, ok := decodeJSON(output, func(u *updateStatus) bool { return u.Update != nil && u.Availability != nil })
	if !ok {
		return adapter.UpdateSignal{}, false
	}
	update, availability := status.Update, status.Availability
	if availability.Available == nil || (len(update.Error) > 0 && string(update.Error) != "null") {
		return adapter.UpdateSignal{}, false
	}
	registryLatest := ""
	if update.Registry != nil {
		registryLatest = adapter.CleanSignalVersion(update.Registry.LatestVersion)
	}
	compared := registryLatest != "" || (update.InstallKind == "git" && availability.GitBehind != nil)
	if !compared {
		return adapter.UpdateSignal{}, false
	}

	signal := adapter.UpdateSignal{Available: *availability.Available}
	if v := adapter.CleanSignalVersion(current); v != "" {
		signal.Current = &v
	}
	latest := adapter.CleanSignalVersion(availability.LatestVersion)
	if latest == "" {
		latest = registryLatest
	}
	if latest != "" {
		signal.Latest = &latest
	}
	if channel := adapter.CleanCode(status.Channel.Value); channel != "" {
		signal.Channel = &channel
	}
	return signal, true
}

// parseSecretsAudit reads "openclaw secrets audit --json": the summary counts,
// and each finding's code. A finding's file, path and message are not decoded.
func parseSecretsAudit(output []byte) (adapter.SecretsAudit, bool) {
	type secretsAudit struct {
		Summary *struct {
			Plaintext     *int `json:"plaintextCount"`
			Unresolved    *int `json:"unresolvedRefCount"`
			Shadowed      *int `json:"shadowedRefCount"`
			StoreResidue  *int `json:"storeResidueCount"`
			LegacyResidue *int `json:"legacyResidueCount"`
		} `json:"summary"`
		Findings *[]struct {
			Code string `json:"code"`
		} `json:"findings"`
	}
	audit, ok := decodeJSON(output, func(a *secretsAudit) bool { return a.Summary != nil && a.Findings != nil })
	if !ok {
		return adapter.SecretsAudit{}, false
	}
	s := audit.Summary
	if s.Plaintext == nil || s.Unresolved == nil || s.Shadowed == nil {
		return adapter.SecretsAudit{}, false
	}
	codes := make([]string, 0, len(*audit.Findings))
	for _, f := range *audit.Findings {
		codes = append(codes, f.Code)
	}
	return adapter.SecretsAudit{
		Plaintext:     *s.Plaintext,
		Unresolved:    *s.Unresolved,
		Shadowed:      *s.Shadowed,
		StoreResidue:  intOr(s.StoreResidue),
		LegacyResidue: intOr(s.LegacyResidue),
		Codes:         adapter.CountCodes(codes),
	}, true
}

// parseSecurityAudit reads "openclaw security audit --json": the count of
// findings at each severity, and each finding's check id. A finding's title,
// detail and remedy are not decoded.
func parseSecurityAudit(output []byte) (adapter.ConfigAudit, bool) {
	type securityAudit struct {
		Summary *struct {
			Critical *int `json:"critical"`
			Warn     *int `json:"warn"`
			Info     *int `json:"info"`
		} `json:"summary"`
		Findings *[]struct {
			CheckID string `json:"checkId"`
		} `json:"findings"`
	}
	audit, ok := decodeJSON(output, func(a *securityAudit) bool { return a.Summary != nil && a.Findings != nil })
	if !ok {
		return adapter.ConfigAudit{}, false
	}
	s := audit.Summary
	if s.Critical == nil || s.Warn == nil || s.Info == nil {
		return adapter.ConfigAudit{}, false
	}
	codes := make([]string, 0, len(*audit.Findings))
	for _, f := range *audit.Findings {
		codes = append(codes, f.CheckID)
	}
	return adapter.ConfigAudit{Critical: *s.Critical, Warn: *s.Warn, Info: *s.Info, Codes: adapter.CountCodes(codes)}, true
}

// parseDoctorLint reads "openclaw doctor --lint --json": the findings at
// warning level or above, and each one's check id. A finding's message, path
// and suggested fix are not decoded.
func parseDoctorLint(output []byte) (adapter.DoctorSignal, bool) {
	type doctorLint struct {
		ChecksRun *int `json:"checksRun"`
		Findings  *[]struct {
			CheckID string `json:"checkId"`
		} `json:"findings"`
	}
	lint, ok := decodeJSON(output, func(l *doctorLint) bool { return l.Findings != nil && l.ChecksRun != nil })
	if !ok {
		return adapter.DoctorSignal{}, false
	}
	// A run that checked nothing is a failure, not a clean bill of health.
	if *lint.ChecksRun <= 0 {
		return adapter.DoctorSignal{}, false
	}
	codes := make([]string, 0, len(*lint.Findings))
	for _, f := range *lint.Findings {
		codes = append(codes, f.CheckID)
	}
	return adapter.DoctorSignal{Warnings: len(*lint.Findings), Codes: adapter.CountCodes(codes)}, true
}

func intOr(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}
