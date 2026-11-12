package adapter

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"
)

// CapSignals: the adapter can report the runtime's housekeeping signals:
// credential health, pending approvals and pairing requests, backups, updates
// and the runtime's own audits. Such an adapter also implements Signaler.
const CapSignals Capability = "signals"

// Credential kinds, in terms that fit any runtime.
const (
	CredentialOAuth  = "oauth"
	CredentialToken  = "token"
	CredentialAPIKey = "api_key"
	CredentialEnv    = "env"
	CredentialOther  = "other"
)

// Credential statuses, from best to worst.
const (
	CredentialOK       = "ok"
	CredentialExpiring = "expiring"
	CredentialFailing  = "failing"
	CredentialExpired  = "expired"
	CredentialMissing  = "missing"
)

// CredentialExpiringWithin is how soon a sign-in must expire to count as
// expiring.
const CredentialExpiringWithin = 7 * 24 * time.Hour

// MaxSignalItems is the most entries sent in any one list or map of a signal.
const MaxSignalItems = 50

// CredentialSignal is the state of one provider's credentials. It never
// carries a credential, its label, its profile or where it is kept.
type CredentialSignal struct {
	Provider string `json:"provider"`
	// Kind is one of the Credential kinds above.
	Kind string `json:"kind"`
	// Status is one of the Credential statuses above.
	Status string `json:"status"`
	// ExpiresAt is when the soonest sign-in expires, for a credential that
	// expires.
	ExpiresAt *time.Time `json:"expires_at"`
}

// ApprovalsSignal counts the requests waiting for someone to approve them.
// What is being approved is never part of it.
type ApprovalsSignal struct {
	Pending  int        `json:"pending"`
	OldestAt *time.Time `json:"oldest_at"`
}

// PairingSignal counts the pairing requests waiting on one chat service. Who
// asked is never part of it.
type PairingSignal struct {
	// Channel is the kind of chat service, such as "telegram".
	Channel string `json:"channel"`
	Pending int    `json:"pending"`
}

// BackupsSignal says whether the runtime's data is backed up.
type BackupsSignal struct {
	Configured bool       `json:"configured"`
	LastAt     *time.Time `json:"last_at"`
	// LastOK is whether the last backup succeeded, when that is known.
	LastOK *bool `json:"last_ok"`
}

// UpdateSignal says whether a newer version of the runtime is available.
type UpdateSignal struct {
	Current   *string `json:"current"`
	Latest    *string `json:"latest"`
	Available bool    `json:"available"`
	Channel   *string `json:"channel"`
	// Security is whether the update fixes a security problem, when the
	// runtime says.
	Security *bool `json:"security"`
}

// SecretsAudit counts what the runtime's own secrets audit found.
type SecretsAudit struct {
	Plaintext     int            `json:"plaintext"`
	Unresolved    int            `json:"unresolved"`
	Shadowed      int            `json:"shadowed"`
	StoreResidue  int            `json:"store_residue"`
	LegacyResidue int            `json:"legacy_residue"`
	Codes         map[string]int `json:"codes"`
}

// ConfigAudit counts what the runtime's own security audit found, by
// severity and by the runtime's short check code.
type ConfigAudit struct {
	Critical int            `json:"critical"`
	Warn     int            `json:"warn"`
	Info     int            `json:"info"`
	Codes    map[string]int `json:"codes"`
}

// SecuritySignal is the runtime's own audits, as counts and codes.
type SecuritySignal struct {
	Secrets *SecretsAudit `json:"secrets,omitempty"`
	Audit   *ConfigAudit  `json:"audit,omitempty"`
}

// DoctorSignal counts what the runtime's own read-only health checks found.
type DoctorSignal struct {
	Warnings int            `json:"warnings"`
	Codes    map[string]int `json:"codes"`
}

// SignalReport is what an adapter could find out. A nil part was not read:
// the runtime does not support it, or its answer could not be read this time.
// Every part holds counts, dates, provider names, kinds of chat service,
// versions and short codes only.
type SignalReport struct {
	Credentials *[]CredentialSignal `json:"credentials,omitempty"`
	Approvals   *ApprovalsSignal    `json:"approvals,omitempty"`
	Pairing     *[]PairingSignal    `json:"pairing,omitempty"`
	Backups     *BackupsSignal      `json:"backups,omitempty"`
	Update      *UpdateSignal       `json:"update,omitempty"`
	Security    *SecuritySignal     `json:"security,omitempty"`
	Doctor      *DoctorSignal       `json:"doctor,omitempty"`
}

// Empty reports whether no part could be read.
func (r SignalReport) Empty() bool {
	return r.Credentials == nil && r.Approvals == nil && r.Pairing == nil && r.Backups == nil &&
		r.Update == nil && r.Security == nil && r.Doctor == nil
}

// Signaler is implemented by an adapter with CapSignals. Collecting runs
// several of the runtime's own commands and may take minutes, so it is called
// rarely and never on the heartbeat path. Each part is read on its own: one
// that fails is left out. It reports false when no part could be read.
type Signaler interface {
	Signals(ctx context.Context) (SignalReport, bool)
}

// codePattern is what a code must look like to be sent: a short identifier,
// never a sentence or a path.
var codePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

// CleanCode returns a short check or finding code fit to send, or "" for
// anything else. A "/" separating the parts of a code becomes ":".
func CleanCode(code string) string {
	code = strings.ReplaceAll(strings.TrimSpace(code), "/", ":")
	if !codePattern.MatchString(code) {
		return ""
	}
	return code
}

// namePattern is what a provider or chat service name must look like.
var signalNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

// CleanSignalName returns a provider or chat service name fit to send, in
// lowercase, or "" for anything else.
func CleanSignalName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if !signalNamePattern.MatchString(name) {
		return ""
	}
	return name
}

var signalVersionPattern = regexp.MustCompile(`^v?[0-9][0-9A-Za-z.+_-]{0,63}$`)

// CleanSignalVersion returns a version number fit to send, such as
// "2026.9.9" or "0.23.1", or "" for anything else.
func CleanSignalVersion(v string) string {
	v = strings.TrimSpace(v)
	if !signalVersionPattern.MatchString(v) {
		return ""
	}
	return v
}

// CountCodes counts codes, dropping any that are not fit to send. At most
// MaxSignalItems are kept, the most frequent first. It never returns nil.
func CountCodes(codes []string) map[string]int {
	counts := map[string]int{}
	for _, code := range codes {
		if code = CleanCode(code); code != "" {
			counts[code]++
		}
	}
	return LimitCodes(counts)
}

// LimitCodes keeps the MaxSignalItems most frequent codes that are fit to
// send. It never returns nil.
func LimitCodes(counts map[string]int) map[string]int {
	type entry struct {
		code  string
		count int
	}
	entries := make([]entry, 0, len(counts))
	for code, count := range counts {
		if clean := CleanCode(code); clean == code && count > 0 {
			entries = append(entries, entry{code, count})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].code < entries[j].code
	})
	if len(entries) > MaxSignalItems {
		entries = entries[:MaxSignalItems]
	}
	out := make(map[string]int, len(entries))
	for _, e := range entries {
		out[e.code] = e.count
	}
	return out
}

// credentialRank orders statuses from best to worst.
var credentialRank = map[string]int{
	CredentialOK:       0,
	CredentialExpiring: 1,
	CredentialFailing:  2,
	CredentialExpired:  3,
	CredentialMissing:  4,
}

// WorseCredentialStatus returns the worse of two statuses.
func WorseCredentialStatus(a, b string) string {
	if credentialRank[b] > credentialRank[a] {
		return b
	}
	return a
}

// ValidCredentialStatus reports whether s is one of the Credential statuses.
func ValidCredentialStatus(s string) bool {
	_, ok := credentialRank[s]
	return ok
}

// ValidCredentialKind reports whether k is one of the Credential kinds.
func ValidCredentialKind(k string) bool {
	switch k {
	case CredentialOAuth, CredentialToken, CredentialAPIKey, CredentialEnv, CredentialOther:
		return true
	}
	return false
}
