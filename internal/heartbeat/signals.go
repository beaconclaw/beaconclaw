package heartbeat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

// Signals is the runtime's housekeeping state: credential health, pending
// approvals and pairing requests, backups, updates and the runtime's own
// audits. It holds counts, dates, provider names, kinds of chat service,
// versions and short codes only. A part the runtime could not report is left
// out.
type Signals struct {
	CollectedAt time.Time `json:"collected_at"`
	adapter.SignalReport
}

// NewSignals prepares an adapter's report for sending. Whatever the adapter
// returned, names, versions and codes that are not short plain identifiers are
// dropped, lists are sorted and capped, and times are in UTC.
func NewSignals(report adapter.SignalReport, collectedAt time.Time) Signals {
	out := adapter.SignalReport{}

	if report.Credentials != nil {
		byProvider := map[string]adapter.CredentialSignal{}
		for _, c := range *report.Credentials {
			provider := adapter.CleanSignalName(c.Provider)
			if provider == "" || !adapter.ValidCredentialKind(c.Kind) || !adapter.ValidCredentialStatus(c.Status) {
				continue
			}
			c.Provider = provider
			c.ExpiresAt = utc(c.ExpiresAt)
			if seen, ok := byProvider[provider]; ok {
				c.Status = adapter.WorseCredentialStatus(seen.Status, c.Status)
				c.ExpiresAt = earlier(seen.ExpiresAt, c.ExpiresAt)
				c.Kind = seen.Kind
			}
			byProvider[provider] = c
		}
		list := make([]adapter.CredentialSignal, 0, len(byProvider))
		for _, c := range byProvider {
			list = append(list, c)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Provider < list[j].Provider })
		if len(list) > adapter.MaxSignalItems {
			list = list[:adapter.MaxSignalItems]
		}
		out.Credentials = &list
	}

	if a := report.Approvals; a != nil {
		approvals := adapter.ApprovalsSignal{Pending: nonNegative(a.Pending), OldestAt: utc(a.OldestAt)}
		if approvals.Pending == 0 {
			approvals.OldestAt = nil
		}
		out.Approvals = &approvals
	}

	if report.Pairing != nil {
		byChannel := map[string]int{}
		for _, p := range *report.Pairing {
			if channel := adapter.CleanSignalName(p.Channel); channel != "" {
				byChannel[channel] += nonNegative(p.Pending)
			}
		}
		list := make([]adapter.PairingSignal, 0, len(byChannel))
		for channel, pending := range byChannel {
			list = append(list, adapter.PairingSignal{Channel: channel, Pending: pending})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Channel < list[j].Channel })
		if len(list) > adapter.MaxSignalItems {
			list = list[:adapter.MaxSignalItems]
		}
		out.Pairing = &list
	}

	if b := report.Backups; b != nil {
		backups := *b
		backups.LastAt = utc(b.LastAt)
		out.Backups = &backups
	}

	if u := report.Update; u != nil {
		update := adapter.UpdateSignal{
			Current:   cleaned(u.Current, adapter.CleanSignalVersion),
			Latest:    cleaned(u.Latest, adapter.CleanSignalVersion),
			Available: u.Available,
			Channel:   cleaned(u.Channel, adapter.CleanCode),
			Security:  u.Security,
		}
		out.Update = &update
	}

	if s := report.Security; s != nil && (s.Secrets != nil || s.Audit != nil) {
		security := adapter.SecuritySignal{}
		if sec := s.Secrets; sec != nil {
			security.Secrets = &adapter.SecretsAudit{
				Plaintext:     nonNegative(sec.Plaintext),
				Unresolved:    nonNegative(sec.Unresolved),
				Shadowed:      nonNegative(sec.Shadowed),
				StoreResidue:  nonNegative(sec.StoreResidue),
				LegacyResidue: nonNegative(sec.LegacyResidue),
				Codes:         adapter.LimitCodes(sec.Codes),
			}
		}
		if audit := s.Audit; audit != nil {
			security.Audit = &adapter.ConfigAudit{
				Critical: nonNegative(audit.Critical),
				Warn:     nonNegative(audit.Warn),
				Info:     nonNegative(audit.Info),
				Codes:    adapter.LimitCodes(audit.Codes),
			}
		}
		out.Security = &security
	}

	if d := report.Doctor; d != nil {
		out.Doctor = &adapter.DoctorSignal{Warnings: nonNegative(d.Warnings), Codes: adapter.LimitCodes(d.Codes)}
	}

	return Signals{CollectedAt: collectedAt.UTC(), SignalReport: out}
}

// Hash identifies the signals' content, leaving out when they were collected.
// The block is sent only when this changes.
func (s Signals) Hash() string {
	data, err := json.Marshal(s.SignalReport)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func utc(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	u := t.UTC().Truncate(time.Second)
	return &u
}

func earlier(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case b.Before(*a):
		return b
	}
	return a
}

func nonNegative(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func cleaned(v *string, clean func(string) string) *string {
	if v == nil {
		return nil
	}
	if c := clean(*v); c != "" {
		return &c
	}
	return nil
}
