package heartbeat

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

func sampleSignals() adapter.SignalReport {
	expires := time.Date(2026, 1, 9, 0, 0, 0, 0, time.FixedZone("x", 3600))
	oldest := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return adapter.SignalReport{
		Credentials: &[]adapter.CredentialSignal{
			{Provider: "openai", Kind: adapter.CredentialOAuth, Status: adapter.CredentialExpiring, ExpiresAt: &expires},
			{Provider: "anthropic", Kind: adapter.CredentialAPIKey, Status: adapter.CredentialOK},
		},
		Approvals: &adapter.ApprovalsSignal{Pending: 2, OldestAt: &oldest},
		Pairing:   &[]adapter.PairingSignal{{Channel: "telegram", Pending: 1}},
		Backups:   &adapter.BackupsSignal{Configured: true},
		Update:    &adapter.UpdateSignal{Current: adapter.Ptr("2026.9.9"), Latest: adapter.Ptr("2026.11.10"), Available: true, Channel: adapter.Ptr("stable")},
		Security: &adapter.SecuritySignal{
			Secrets: &adapter.SecretsAudit{Shadowed: 3, Codes: map[string]int{"REF_SHADOWED": 3}},
			Audit:   &adapter.ConfigAudit{Warn: 1, Codes: map[string]int{"gateway.bind": 1}},
		},
		Doctor: &adapter.DoctorSignal{Warnings: 2, Codes: map[string]int{"core:doctor:x": 2}},
	}
}

func TestSignalsHashIgnoresWhenCollected(t *testing.T) {
	first := NewSignals(sampleSignals(), collected)
	later := NewSignals(sampleSignals(), collected.Add(time.Hour))
	if first.Hash() == "" || first.Hash() != later.Hash() {
		t.Error("the same signals collected later have a different hash")
	}

	changes := map[string]func(*adapter.SignalReport){
		"credential status": func(r *adapter.SignalReport) { (*r.Credentials)[1].Status = adapter.CredentialFailing },
		"approvals":         func(r *adapter.SignalReport) { r.Approvals.Pending = 3 },
		"pairing":           func(r *adapter.SignalReport) { (*r.Pairing)[0].Pending = 0 },
		"backups":           func(r *adapter.SignalReport) { r.Backups.Configured = false },
		"update":            func(r *adapter.SignalReport) { r.Update.Latest = adapter.Ptr("2026.12.1") },
		"secrets":           func(r *adapter.SignalReport) { r.Security.Secrets.Codes["REF_UNRESOLVED"] = 1 },
		"doctor":            func(r *adapter.SignalReport) { r.Doctor.Warnings = 0 },
		"part missing":      func(r *adapter.SignalReport) { r.Approvals = nil },
		"pairing emptied":   func(r *adapter.SignalReport) { *r.Pairing = []adapter.PairingSignal{} },
	}
	for what, change := range changes {
		r := sampleSignals()
		change(&r)
		if NewSignals(r, collected).Hash() == first.Hash() {
			t.Errorf("changing the %s did not change the hash", what)
		}
	}

	// The order a runtime lists things in does not matter.
	r := sampleSignals()
	(*r.Credentials)[0], (*r.Credentials)[1] = (*r.Credentials)[1], (*r.Credentials)[0]
	if NewSignals(r, collected).Hash() != first.Hash() {
		t.Error("listing credentials in another order changed the hash")
	}
}

func TestSignalsWireFormat(t *testing.T) {
	data, err := json.Marshal(NewSignals(sampleSignals(), collected))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"collected_at", "credentials", "approvals", "pairing", "backups", "update", "security", "doctor"} {
		if _, ok := got[key]; !ok {
			t.Errorf("no %q in %s", key, data)
		}
	}
	for _, want := range []string{
		`"expires_at":"2026-01-08T23:00:00Z"`, `"oldest_at":"2026-01-01T00:00:00Z"`,
		`"last_at":null`, `"last_ok":null`, `"security":null`, `"store_residue":0`, `"legacy_residue":0`,
		`"codes":{"REF_SHADOWED":3}`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("no %s in %s", want, data)
		}
	}

	// A part that was not read is left out, not sent empty.
	data, _ = json.Marshal(NewSignals(adapter.SignalReport{Pairing: &[]adapter.PairingSignal{}}, collected))
	if string(data) != `{"collected_at":"2026-01-02T03:04:05Z","pairing":[]}` {
		t.Errorf("sparse = %s", data)
	}
}

func TestSignalsAreCleaned(t *testing.T) {
	many := []adapter.PairingSignal{}
	codes := map[string]int{}
	for i := 0; i < 80; i++ {
		many = append(many, adapter.PairingSignal{Channel: fmt.Sprintf("chan%02d", i), Pending: 1})
		codes[fmt.Sprintf("CODE_%02d", i)] = i + 1
	}
	codes["a sentence, not a code"] = 1000
	codes["/home/someone/.secrets"] = 1000
	codes[strings.Repeat("X", 65)] = 1000

	report := adapter.SignalReport{
		Credentials: &[]adapter.CredentialSignal{
			{Provider: "Work Account <someone@example.com>", Kind: adapter.CredentialOAuth, Status: adapter.CredentialOK},
			{Provider: "openai", Kind: "secret", Status: adapter.CredentialOK},
			{Provider: "openai", Kind: adapter.CredentialOAuth, Status: "fine"},
			{Provider: "OpenAI", Kind: adapter.CredentialOAuth, Status: adapter.CredentialOK},
			{Provider: "openai", Kind: adapter.CredentialToken, Status: adapter.CredentialExpired},
		},
		Approvals: &adapter.ApprovalsSignal{Pending: -4, OldestAt: &collected},
		Pairing:   &many,
		Update:    &adapter.UpdateSignal{Current: adapter.Ptr("/opt/openclaw"), Latest: adapter.Ptr("2026.11.10 (security fix)"), Channel: adapter.Ptr("stable channel")},
		Doctor:    &adapter.DoctorSignal{Warnings: 3, Codes: codes},
		Security:  &adapter.SecuritySignal{},
	}
	got := NewSignals(report, collected)

	if len(*got.Credentials) != 1 || (*got.Credentials)[0].Provider != "openai" || (*got.Credentials)[0].Status != adapter.CredentialExpired {
		t.Errorf("credentials = %+v", *got.Credentials)
	}
	if got.Approvals.Pending != 0 || got.Approvals.OldestAt != nil {
		t.Errorf("approvals = %+v", got.Approvals)
	}
	if len(*got.Pairing) != adapter.MaxSignalItems {
		t.Errorf("pairing has %d entries", len(*got.Pairing))
	}
	if got.Update.Current != nil || got.Update.Latest != nil || got.Update.Channel != nil {
		t.Errorf("update = %+v", got.Update)
	}
	if len(got.Doctor.Codes) != adapter.MaxSignalItems || got.Doctor.Codes["CODE_79"] != 80 || got.Doctor.Codes["CODE_00"] != 0 {
		t.Errorf("doctor codes = %v", got.Doctor.Codes)
	}
	if got.Security != nil {
		t.Errorf("an empty security part was kept: %+v", got.Security)
	}
	data, _ := json.Marshal(got)
	for _, banned := range []string{"someone", "sentence", "/opt", "security fix", "XXXX"} {
		if strings.Contains(string(data), banned) {
			t.Errorf("sent %q: %s", banned, data)
		}
	}
}

func TestCleanCode(t *testing.T) {
	for in, want := range map[string]string{
		"REF_SHADOWED":                      "REF_SHADOWED",
		"gateway.bind_no_auth":              "gateway.bind_no_auth",
		"core/doctor/gateway-auth":          "core:doctor:gateway-auth",
		"plugin:check-1":                    "plugin:check-1",
		"has space":                         "",
		"":                                  "",
		"semi;colon":                        "",
		strings.Repeat("a", 64):             strings.Repeat("a", 64),
		strings.Repeat("a", 65):             "",
		"Plaintext key at agents.x (sk-12)": "",
	} {
		if got := adapter.CleanCode(in); got != want {
			t.Errorf("CleanCode(%q) = %q, want %q", in, got, want)
		}
	}
}
