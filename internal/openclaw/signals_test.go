package openclaw

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
	"github.com/beaconclaw/beaconclaw/internal/heartbeat"
)

// All values below are invented. The strings in secretStrings stand for what
// must never leave the machine.
var signalsNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func ms(t time.Time) int64 { return t.UnixMilli() }

var secretStrings = []string{
	"sk-test-SECRET-1234", "work-account@example.com", "/home/someone/.openclaw",
	"agents.defaults.models", "Someone Example", "user-998877", "PROFILE-ID-777",
	"Plaintext key found", "Rotate the key", "OPENAI_API_KEY", "rm -rf", "Tighten the bind",
	"pair-code-XYZ", "backup-repo-path", "drift message", "lint message text",
}

var modelsStatusFixture = `[plugins] loading 3 plugins {quietly}
{
  "configPath": "/home/someone/.openclaw/openclaw.json",
  "agentDir": "/home/someone/.openclaw/agents/main",
  "defaultModel": "openai/gpt-5",
  "auth": {
    "storePath": "/home/someone/.openclaw/agents/main/auth-profiles.json",
    "shellEnvFallback": {"enabled": true, "appliedKeys": ["OPENAI_API_KEY"]},
    "providersWithOAuth": ["anthropic (1)"],
    "missingProvidersInUse": ["mistral"],
    "modelRouteIssues": [{"kind": "missing-auth", "provider": "mistral", "model": "large", "message": "Rotate the key"}],
    "runtimeAuthRoutes": [
      {"provider": "codex-cli", "runtime": "codex", "status": "unavailable", "effective": {"kind": "profiles", "detail": "PROFILE-ID-777"}},
      {"provider": "openai", "runtime": "codex", "status": "usable", "effective": {"kind": "env", "detail": "sk-test-SECRET-1234"}}
    ],
    "providers": [
      {"provider": "anthropic", "effective": {"kind": "profiles", "detail": "/home/someone/.openclaw/agents/main/auth-profiles.json"},
       "profiles": {"count": 1, "oauth": 1, "token": 0, "apiKey": 0, "labels": ["PROFILE-ID-777=OAuth work-account@example.com"]}},
      {"provider": "openai", "effective": {"kind": "env", "detail": "sk-test-SECRET-1234"},
       "profiles": {"count": 0, "oauth": 0, "token": 0, "apiKey": 0, "labels": []},
       "env": {"value": "sk-test-SECRET-1234", "source": "env: OPENAI_API_KEY"}},
      {"provider": "groq", "effective": {"kind": "profiles", "detail": "x"},
       "profiles": {"count": 1, "oauth": 0, "token": 0, "apiKey": 1, "labels": ["groq:default=sk-test-SECRET-1234"]}},
      {"provider": "Bad Provider Name!", "effective": {"kind": "env", "detail": "x"}},
      {"provider": "openrouter", "effective": {"kind": "models.json", "detail": "sk-test-SECRET-1234"}}
    ],
    "unusableProfiles": [
      {"profileId": "PROFILE-ID-777", "provider": "groq", "kind": "cooldown", "reason": "rate_limit", "recoveryHint": "Rotate the key", "until": 1, "remainingMs": 5000}
    ],
    "oauth": {
      "warnAfterMs": 86400000,
      "profiles": [
        {"profileId": "PROFILE-ID-777", "provider": "anthropic", "type": "oauth", "source": "store", "label": "work-account@example.com", "status": "ok", "expiresAt": ` + jsonInt(ms(signalsNow.Add(72*time.Hour))) + `, "remainingMs": 1},
        {"profileId": "groq:default", "provider": "groq", "type": "api_key", "source": "store", "label": "groq:default", "status": "static"},
        {"profileId": "github-copilot:me", "provider": "github-copilot", "type": "token", "source": "store", "label": "Someone Example", "status": "expired", "expiresAt": ` + jsonInt(ms(signalsNow.Add(-time.Hour))) + `},
        {"profileId": "PROFILE-ID-777", "provider": "google", "type": "oauth", "label": "work-account@example.com", "status": "expired", "expiresAt": ` + jsonInt(ms(signalsNow.Add(-time.Hour))) + `},
        {"profileId": "PROFILE-ID-777", "provider": "openai-codex", "type": "oauth", "label": "work-account@example.com", "status": "expired", "reasonCode": "expired"},
        {"profileId": "PROFILE-ID-777", "provider": "minimax", "type": "oauth", "label": "Someone Example (saved, inactive)", "status": "missing", "reasonCode": "setup_inactive"},
        {"profileId": "PROFILE-ID-777", "provider": "zai", "type": "token", "label": "Someone Example", "status": "ok", "expiresAt": ` + jsonInt(ms(signalsNow.Add(72*time.Hour))) + `}
      ],
      "providers": [
        {"provider": "anthropic", "status": "ok", "expiresAt": ` + jsonInt(ms(signalsNow.Add(72*time.Hour))) + `},
        {"provider": "groq", "status": "static"},
        {"provider": "github-copilot", "status": "expired"}
      ]
    }
  }
}
`

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

var approvalsFixture = `{
  "approvals": [
    {"kind": "exec", "id": "a1", "agentId": "main", "sessionKey": "user-998877", "createdAtMs": ` + jsonInt(ms(signalsNow.Add(-3*time.Hour))) + `, "expiresAtMs": 1, "summary": "rm -rf /tmp/x"},
    {"kind": "plugin", "id": "a2", "createdAtMs": ` + jsonInt(ms(signalsNow.Add(-time.Hour))) + `, "summary": "Someone Example asks"},
    {"kind": "system-agent", "id": "a3", "summary": "no time"}
  ]
}`

var signalsCronFixture = `{"jobs": [
  {"id": "job-a", "name": "Inbox sweep", "enabled": true, "schedule": {"kind": "every", "everyMs": 60000}, "state": {}},
  {"id": "job-backup", "name": "openclaw-backup-scheduled", "declarationKey": "openclaw-backup-scheduled", "enabled": true,
   "schedule": {"kind": "every", "everyMs": 86400000},
   "payload": {"kind": "command", "argv": ["openclaw", "backup", "git", "create", "--repository", "backup-repo-path"]},
   "state": {"lastRunAtMs": ` + jsonInt(ms(signalsNow.Add(-2*time.Hour))) + `, "lastRunStatus": "error", "lastError": "Rotate the key"}}
]}`

var updateStatusFixture = `{
  "update": {"root": "/home/someone/.openclaw", "installKind": "package", "packageManager": "npm",
             "registry": {"latestVersion": "2026.11.10"}},
  "channel": {"value": "stable", "source": "config", "label": "stable (config)", "config": "stable"},
  "availability": {"available": true, "hasGitUpdate": false, "hasRegistryUpdate": true, "latestVersion": "2026.11.10", "gitBehind": null},
  "serviceDefinition": {"drift": [{"message": "drift message"}], "warnings": ["drift message"]},
  "channelIssues": [{"channel": "telegram", "accountId": "user-998877", "message": "drift message"}]
}`

var secretsAuditFixture = `{
  "version": 1, "status": "unresolved",
  "filesScanned": ["/home/someone/.openclaw/openclaw.json"],
  "summary": {"plaintextCount": 1, "unresolvedRefCount": 1, "shadowedRefCount": 3, "storeResidueCount": 0, "legacyResidueCount": 0},
  "findings": [
    {"code": "PLAINTEXT_FOUND", "severity": "warn", "file": "/home/someone/.openclaw/openclaw.json", "jsonPath": "agents.defaults.models", "message": "Plaintext key found", "profileId": "PROFILE-ID-777"},
    {"code": "REF_UNRESOLVED", "file": "/home/someone/.openclaw/openclaw.json", "message": "sk-test-SECRET-1234"},
    {"code": "REF_SHADOWED"}, {"code": "REF_SHADOWED"}, {"code": "REF_SHADOWED"},
    {"code": "a sentence that is not a code"}
  ]
}`

var securityAuditFixture = `{
  "ts": 1,
  "summary": {"critical": 1, "warn": 2, "info": 0},
  "findings": [
    {"checkId": "gateway.bind_no_auth", "severity": "critical", "title": "Tighten the bind", "detail": "/home/someone/.openclaw", "remediation": "Tighten the bind"},
    {"checkId": "fs.state_dir.perms", "severity": "warn", "title": "x", "detail": "work-account@example.com"},
    {"checkId": "fs.state_dir.perms", "severity": "warn"}
  ],
  "deep": null,
  "secretDiagnostics": ["sk-test-SECRET-1234"]
}`

var doctorFixture = `{"ok": false, "checksRun": 40, "checksSkipped": 2, "findings": [
  {"checkId": "core/doctor/gateway-auth", "severity": "warning", "message": "lint message text", "path": "/home/someone/.openclaw/openclaw.json", "fixHint": "Rotate the key"},
  {"checkId": "core/doctor/gateway-services/platform-notes", "severity": "error", "message": "lint message text"}
], "warnings": [{"checkId": "core/doctor/lint-state-inspection", "message": "lint message text"}]}`

// fakeRun answers commands from a table. A missing command fails as if
// OpenClaw were not installed.
type fakeRun struct {
	answers map[string]fakeAnswer
	asked   []string
}

type fakeAnswer struct {
	out string
	err error
}

var errExit = errors.New("exit status 1")

func (f *fakeRun) run(_ context.Context, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	f.asked = append(f.asked, key)
	answer, ok := f.answers[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return []byte(answer.out), answer.err
}

func fullRun() *fakeRun {
	return &fakeRun{answers: map[string]fakeAnswer{
		"models status --json":                   {out: modelsStatusFixture},
		"approvals pending --json":               {out: approvalsFixture},
		"pairing list telegram --json":           {out: `{"channel": "telegram", "requests": [{"id": "user-998877", "code": "pair-code-XYZ", "meta": {"name": "Someone Example"}}]}`},
		"pairing list discord --json":            {out: `{"channel": "discord", "requests": []}`},
		"pairing list webchat --json":            {out: `{"ok": false, "error": {"type": "cli_error", "message": "Channel \"webchat\" does not support pairing. Supported pairing channels: telegram, discord."}}`, err: errExit},
		"cron list --json --all":                 {out: signalsCronFixture},
		"update status --json":                   {out: updateStatusFixture},
		"secrets audit --json":                   {out: secretsAuditFixture, err: errors.New("exit status 2")},
		"security audit --json":                  {out: securityAuditFixture},
		"doctor --lint --json":                   {out: doctorFixture, err: errExit},
		"pairing list --json":                    {out: `{"ok": false, "error": {"type": "cli_error", "message": "Channel required (expected one of: telegram, discord)."}}`, err: errExit},
		"unused so the table has a trailing key": {},
	}}
}

func TestSignalsFromEveryCommand(t *testing.T) {
	run := fullRun()
	report := collectSignals(context.Background(), run.run, "2026.9.9", []string{"telegram", "discord", "webchat", "Not A Channel", "--evil"}, signalsNow)

	if report.Credentials == nil {
		t.Fatal("no credentials")
	}
	got := map[string]adapter.CredentialSignal{}
	for _, c := range *report.Credentials {
		got[c.Provider] = c
	}
	want := map[string]struct{ kind, status string }{
		"anthropic":      {adapter.CredentialOAuth, adapter.CredentialOK},
		"google":         {adapter.CredentialOAuth, adapter.CredentialOK},
		"openai-codex":   {adapter.CredentialOAuth, adapter.CredentialFailing},
		"minimax":        {adapter.CredentialOAuth, adapter.CredentialOK},
		"zai":            {adapter.CredentialToken, adapter.CredentialExpiring},
		"openai":         {adapter.CredentialEnv, adapter.CredentialOK},
		"groq":           {adapter.CredentialAPIKey, adapter.CredentialFailing},
		"github-copilot": {adapter.CredentialToken, adapter.CredentialExpired},
		"openrouter":     {adapter.CredentialOther, adapter.CredentialOK},
		"mistral":        {adapter.CredentialOther, adapter.CredentialMissing},
		"codex-cli":      {adapter.CredentialOther, adapter.CredentialFailing},
	}
	if len(got) != len(want) {
		t.Errorf("credentials = %+v", *report.Credentials)
	}
	for provider, w := range want {
		c, ok := got[provider]
		if !ok || c.Kind != w.kind || c.Status != w.status {
			t.Errorf("%s = %+v, want kind %s status %s", provider, c, w.kind, w.status)
		}
	}
	// An OAuth sign-in renews itself: the expiry of its access token is
	// neither sent nor judged.
	for _, provider := range []string{"anthropic", "google", "openai-codex"} {
		if got[provider].ExpiresAt != nil {
			t.Errorf("%s expires_at = %v, want null", provider, got[provider].ExpiresAt)
		}
	}
	if exp := got["zai"].ExpiresAt; exp == nil || !exp.Equal(signalsNow.Add(72*time.Hour)) {
		t.Errorf("token expires_at = %v", exp)
	}
	if got["openai"].ExpiresAt != nil || got["groq"].ExpiresAt != nil {
		t.Error("a credential that does not expire was given an expiry")
	}

	if a := report.Approvals; a == nil || a.Pending != 3 || a.OldestAt == nil || !a.OldestAt.Equal(signalsNow.Add(-3*time.Hour)) {
		t.Errorf("approvals = %+v", a)
	}

	if report.Pairing == nil || len(*report.Pairing) != 2 ||
		(*report.Pairing)[0] != (adapter.PairingSignal{Channel: "discord", Pending: 0}) ||
		(*report.Pairing)[1] != (adapter.PairingSignal{Channel: "telegram", Pending: 1}) {
		t.Errorf("pairing = %+v", report.Pairing)
	}
	for _, asked := range run.asked {
		if strings.Contains(asked, "--evil") || strings.Contains(asked, "Not A Channel") {
			t.Errorf("asked OpenClaw %q", asked)
		}
	}

	if b := report.Backups; b == nil || !b.Configured || b.LastAt == nil || !b.LastAt.Equal(signalsNow.Add(-2*time.Hour)) || b.LastOK == nil || *b.LastOK {
		t.Errorf("backups = %+v", b)
	}

	if u := report.Update; u == nil || !u.Available || *u.Current != "2026.9.9" || *u.Latest != "2026.11.10" || *u.Channel != "stable" || u.Security != nil {
		t.Errorf("update = %+v", u)
	}

	if s := report.Security; s == nil || s.Secrets == nil || s.Audit == nil {
		t.Fatalf("security = %+v", s)
	}
	secrets := report.Security.Secrets
	if secrets.Plaintext != 1 || secrets.Unresolved != 1 || secrets.Shadowed != 3 ||
		secrets.Codes["REF_SHADOWED"] != 3 || secrets.Codes["PLAINTEXT_FOUND"] != 1 || len(secrets.Codes) != 3 {
		t.Errorf("secrets = %+v", secrets)
	}
	audit := report.Security.Audit
	if audit.Critical != 1 || audit.Warn != 2 || audit.Codes["fs.state_dir.perms"] != 2 || audit.Codes["gateway.bind_no_auth"] != 1 {
		t.Errorf("audit = %+v", audit)
	}

	if d := report.Doctor; d == nil || d.Warnings != 2 ||
		d.Codes["core:doctor:gateway-auth"] != 1 || d.Codes["core:doctor:gateway-services:platform-notes"] != 1 {
		t.Errorf("doctor = %+v", d)
	}

	for _, asked := range run.asked {
		for _, banned := range []string{"--deep", "--fix", "--allow-exec", "--repair", "--probe"} {
			if strings.Contains(asked, banned) {
				t.Errorf("ran %q", asked)
			}
		}
	}
}

// TestSignalsNeverCarrySecrets marshals the block exactly as it is sent and
// looks for anything from the fixtures that must stay on the machine.
func TestSignalsNeverCarrySecrets(t *testing.T) {
	report := collectSignals(context.Background(), fullRun().run, "2026.9.9", []string{"telegram", "discord", "webchat"}, signalsNow)
	data, err := json.Marshal(heartbeat.NewSignals(report, signalsNow))
	if err != nil {
		t.Fatal(err)
	}
	sent := string(data)
	for _, secret := range secretStrings {
		if strings.Contains(sent, secret) {
			t.Errorf("sent %q: %s", secret, sent)
		}
	}
	for _, field := range []string{"profileId", "label", "storePath", "detail", "file", "jsonPath", "message", "title", "remediation", "summary", "sessionKey", "argv"} {
		if strings.Contains(sent, `"`+field+`"`) {
			t.Errorf("sent a %q field: %s", field, sent)
		}
	}
}

func TestSignalsLeaveOutWhatFails(t *testing.T) {
	gatewayDown := `{"ok": false, "error": {"type": "cli_error", "message": "gateway closed (1006): sk-test-SECRET-1234"}}`
	run := &fakeRun{answers: map[string]fakeAnswer{
		"models status --json":     {out: "OpenClaw state database uses newer schema version", err: errExit},
		"approvals pending --json": {out: gatewayDown, err: errExit},
		"pairing list --json":      {out: `{"ok": false, "error": "No chat DM pairing channels are configured. To approve a TUI request, use openclaw devices approve."}`, err: errExit},
		"cron list --json --all":   {out: gatewayDown, err: errExit},
		"update status --json":     {out: `{"update": {"installKind": "package", "registry": {"latestVersion": null, "error": "offline"}}, "channel": {"value": "stable"}, "availability": {"available": false, "latestVersion": null, "gitBehind": null}}`},
		"secrets audit --json":     {out: "not json at all"},
		"security audit --json":    {out: `{"summary": {"critical": 0}, "findings": []}`},
		"doctor --lint --json":     {out: `{"ok": false, "checksRun": 0, "checksSkipped": 0, "findings": [], "error": {"message": "x"}}`, err: errExit},
	}}
	report := collectSignals(context.Background(), run.run, "", nil, signalsNow)

	if report.Credentials != nil || report.Approvals != nil || report.Backups != nil || report.Update != nil || report.Security != nil || report.Doctor != nil {
		t.Errorf("parts that failed were reported: %+v", report)
	}
	// With no chat service set up, OpenClaw saying so is an answer: none.
	if report.Pairing == nil || len(*report.Pairing) != 0 {
		t.Errorf("pairing = %+v, want an empty list", report.Pairing)
	}
	data, _ := json.Marshal(heartbeat.NewSignals(report, signalsNow))
	if !strings.Contains(string(data), `"pairing":[]`) || strings.Contains(string(data), "credentials") {
		t.Errorf("sent %s", data)
	}

	// Nothing installed: nothing at all.
	empty := collectSignals(context.Background(), (&fakeRun{}).run, "2026.9.9", []string{"telegram"}, signalsNow)
	if !empty.Empty() {
		t.Errorf("with no OpenClaw, report = %+v", empty)
	}
}

func TestPairingIsLeftOutWhenOneServiceFails(t *testing.T) {
	run := &fakeRun{answers: map[string]fakeAnswer{
		"pairing list telegram --json": {out: `{"channel": "telegram", "requests": [{}]}`},
		"pairing list discord --json":  {out: `{"ok": false, "error": {"message": "database is locked"}}`, err: errExit},
	}}
	output := func(args ...string) ([]byte, error) { return run.run(context.Background(), args...) }
	if got, ok := collectPairing(output, []string{"telegram", "discord"}); ok {
		t.Errorf("pairing = %+v, want left out", got)
	}

	// Some other error with no channel named is not "none".
	run = &fakeRun{answers: map[string]fakeAnswer{
		"pairing list --json": {out: `{"ok": false, "error": {"message": "Channel required (expected one of: a, b)."}}`, err: errExit},
	}}
	if got, ok := collectPairing(output, nil); ok {
		t.Errorf("pairing = %+v, want left out", got)
	}

	// The only pairing service is answered for without being named.
	run = &fakeRun{answers: map[string]fakeAnswer{
		"pairing list --json": {out: `{"channel": "telegram", "requests": [{}, {}]}`},
	}}
	if got, ok := collectPairing(output, nil); !ok || len(got) != 1 || got[0].Pending != 2 {
		t.Errorf("pairing = %+v, %v", got, ok)
	}
}

func TestBackupsWithoutASchedule(t *testing.T) {
	got, ok := parseBackups([]byte(`{"jobs": [{"id": "job-a", "name": "Inbox", "state": {}}]}`))
	if !ok || got.Configured || got.LastAt != nil || got.LastOK != nil {
		t.Errorf("backups = %+v, %v", got, ok)
	}
	got, ok = parseBackups([]byte(`{"jobs": [{"id": "b", "declarationKey": "openclaw-backup-scheduled", "enabled": false, "state": {"lastRunAtMs": 1000, "lastRunStatus": "ok"}}]}`))
	if !ok || got.Configured || got.LastAt == nil || got.LastOK == nil || !*got.LastOK {
		t.Errorf("switched-off schedule = %+v, %v", got, ok)
	}
	if _, ok := parseBackups([]byte(`{"error": "x"}`)); ok {
		t.Error("an unreadable job list gave a backup state")
	}
}

func TestUpdateStatusVariants(t *testing.T) {
	// Up to date: OpenClaw gives no latest version in availability, so the
	// registry's is used.
	upToDate := `{"update": {"installKind": "package", "registry": {"latestVersion": "2026.9.9"}}, "channel": {"value": "beta"}, "availability": {"available": false, "latestVersion": null, "gitBehind": null}}`
	got, ok := parseUpdateStatus([]byte(upToDate), "2026.9.9")
	if !ok || got.Available || *got.Latest != "2026.9.9" || *got.Channel != "beta" {
		t.Errorf("up to date = %+v, %v", got, ok)
	}
	// A git checkout compared with its upstream.
	git := `{"update": {"installKind": "git", "registry": null}, "channel": {"value": "dev"}, "availability": {"available": true, "latestVersion": null, "gitBehind": 4}}`
	got, ok = parseUpdateStatus([]byte(git), "")
	if !ok || !got.Available || got.Latest != nil || got.Current != nil {
		t.Errorf("git = %+v, %v", got, ok)
	}
	// An error from the check: not reported.
	failed := `{"update": {"installKind": "package", "error": {"status": "x", "message": "/home/someone"}, "registry": {"latestVersion": "2026.9.9"}}, "channel": {"value": "stable"}, "availability": {"available": false}}`
	if got, ok := parseUpdateStatus([]byte(failed), "2026.9.9"); ok {
		t.Errorf("failed check = %+v", got)
	}
	// A version that is not a version is dropped.
	odd := `{"update": {"installKind": "package", "registry": {"latestVersion": "latest; rm -rf"}}, "channel": {"value": "stable"}, "availability": {"available": true, "latestVersion": "/home/someone"}}`
	if got, ok := parseUpdateStatus([]byte(odd), "2026.9.9"); ok {
		t.Errorf("odd versions = %+v", got)
	}
}

func TestCredentialExpiryWindows(t *testing.T) {
	status := func(expiresIn time.Duration) string {
		out := `{"auth": {"providers": [], "oauth": {"profiles": [{"provider": "copilot", "type": "token", "status": "ok", "expiresAt": ` +
			jsonInt(ms(signalsNow.Add(expiresIn))) + `}], "providers": []}}}`
		got, ok := parseModelsStatus([]byte(out), signalsNow)
		if !ok || len(got) != 1 {
			t.Fatalf("got %+v, %v", got, ok)
		}
		return got[0].Status
	}
	if s := status(30 * 24 * time.Hour); s != adapter.CredentialOK {
		t.Errorf("30 days left = %s", s)
	}
	if s := status(6 * 24 * time.Hour); s != adapter.CredentialExpiring {
		t.Errorf("6 days left = %s", s)
	}
	if s := status(-time.Minute); s != adapter.CredentialExpired {
		t.Errorf("past = %s", s)
	}
	// The same expiries on an OAuth sign-in change nothing.
	for _, expiresIn := range []time.Duration{6 * 24 * time.Hour, -time.Minute} {
		out := `{"auth": {"oauth": {"profiles": [{"provider": "openai-codex", "type": "oauth", "status": "expiring", "expiresAt": ` +
			jsonInt(ms(signalsNow.Add(expiresIn))) + `}]}}}`
		got, ok := parseModelsStatus([]byte(out), signalsNow)
		if !ok || len(got) != 1 || got[0].Status != adapter.CredentialOK || got[0].ExpiresAt != nil {
			t.Errorf("oauth expiring in %s = %+v", expiresIn, got)
		}
	}
	if _, ok := parseModelsStatus([]byte(`{"configPath": "x"}`), signalsNow); ok {
		t.Error("output without auth gave credentials")
	}
}

func TestDecodeJSONSkipsNotices(t *testing.T) {
	out := []byte("notice {not json}\n[warn] {\"partial\": \n{\"approvals\": []}\n")
	got, ok := parseApprovals(out)
	if !ok || got.Pending != 0 || got.OldestAt != nil {
		t.Errorf("got %+v, %v", got, ok)
	}
}
