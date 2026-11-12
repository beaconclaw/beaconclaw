package hermes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
	"github.com/beaconclaw/beaconclaw/internal/heartbeat"
)

// The outputs below copy the layout of Hermes 0.23's text. Names, ids and
// versions are invented.

const pairingWithPending = `
  Pending Pairing Requests (3):
  Platform     Request ID         User ID              Name                 Age
  --------     ----------         -------              ----                 ---
  telegram     req-0001           user-998877          Someone Example      4m ago
  telegram     -                  user-112233          Another Person       90m ago
  discord      req-0002           user-445566                               1m ago

  Approve with: hermes pairing approve <platform> <request-id>
  The code the bot DM'd the user also works if they relay it.

  Approved Users (1):
  Platform     User ID              Name
  --------     -------              ----
  slack        user-778899          Approved Person

`

const pairingOnlyApproved = `
  No pending pairing requests.

  Approved Users (1):
  Platform     User ID              Name
  --------     -------              ----
  slack        user-778899          Approved Person

`

func TestPairingListText(t *testing.T) {
	got, ok := parsePairingList([]byte(pairingWithPending))
	if !ok || len(got) != 2 || got[0] != (adapter.PairingSignal{Channel: "discord", Pending: 1}) || got[1] != (adapter.PairingSignal{Channel: "telegram", Pending: 2}) {
		t.Errorf("pending = %+v, %v", got, ok)
	}

	for name, text := range map[string]string{
		"nothing yet":   "No pairing data found. No one has tried to pair yet~\n",
		"only approved": pairingOnlyApproved,
	} {
		if got, ok := parsePairingList([]byte(text)); !ok || len(got) != 0 || got == nil {
			t.Errorf("%s = %+v, %v; want an empty list", name, got, ok)
		}
	}

	// A total that does not match the rows, a row that does not start with a
	// platform, and text from some other command are all left out.
	for name, text := range map[string]string{
		"total off": strings.Replace(pairingWithPending, "(3)", "(4)", 1),
		"odd row":   strings.Replace(pairingWithPending, "  discord ", "  Some Name ", 1),
		"unknown":   "Usage: hermes pairing {list|approve|revoke|clear-pending}\n",
		"no rows":   "  Pending Pairing Requests (2):\n",
		"no age":    strings.Replace(pairingWithPending, "1m ago", "", 1),
	} {
		if got, ok := parsePairingList([]byte(text)); ok {
			t.Errorf("%s = %+v, want left out", name, got)
		}
	}
}

func TestUpdateCheckText(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		available bool
		latest    string
		channel   string
	}{
		{"release available", "→ Update channel: stable\n→ Selected release available: stable v0.24.0 (0123456789ab)\n  Run `hermes update` to install it.\n", true, "0.24.0", "stable"},
		{"up to date with release", "→ Update channel: stable\n✓ Up to date with the latest release (stable v0.23.1 (0123456789ab)).\n", false, "0.23.1", "stable"},
		{"newer than release", "→ Update channel: stable\n✓ No newer release: stable v0.23.1; this checkout is newer and waits for the next release (`hermes update --set-channel main` follows every commit).\n", false, "0.23.1", "stable"},
		{"behind main", "→ Update channel: main\n→ Fetching from origin...\n⚕ Update available: 12 commits behind origin/main.\n  Run 'hermes update' to install.\n", true, "", "main"},
		{"main up to date", "→ Update channel: main\n→ Fetching from origin...\n✓ Already up to date.\n", false, "", "main"},
		{"coloured", "\x1b[32m✓ Already up to date.\x1b[0m\n", false, "", ""},
	}
	for _, c := range cases {
		got, ok := parseUpdateCheck([]byte(c.text), "0.23.1")
		if !ok || got.Available != c.available || got.Current == nil || *got.Current != "0.23.1" || got.Security != nil {
			t.Errorf("%s = %+v, %v", c.name, got, ok)
			continue
		}
		if (got.Latest == nil && c.latest != "") || (got.Latest != nil && *got.Latest != c.latest) {
			t.Errorf("%s latest = %v, want %q", c.name, got.Latest, c.latest)
		}
		if (got.Channel == nil && c.channel != "") || (got.Channel != nil && *got.Channel != c.channel) {
			t.Errorf("%s channel = %v, want %q", c.name, got.Channel, c.channel)
		}
	}

	// A check Hermes could not make is not reported.
	for name, text := range map[string]string{
		"container":   "✗ ``hermes update`` doesn't apply inside the Docker container.\n\nHermes Agent runs as a published image.\n",
		"no git":      "✗ Not a git repository — cannot check for updates.\n",
		"unreachable": "→ Update channel: stable\n✗ Could not resolve the stable source channel: timed out\n",
	} {
		if got, ok := parseUpdateCheck([]byte(text), "0.23.1"); ok {
			t.Errorf("%s = %+v, want left out", name, got)
		}
	}
}

func TestBackupsFolder(t *testing.T) {
	home := t.TempDir()
	a := &Adapter{home: home}
	if got, ok := a.backups(); ok {
		t.Errorf("no backup folders = %+v, want left out", got)
	}

	// Copies of the config file alone are not a backup.
	if err := os.MkdirAll(filepath.Join(home, "backups", "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, ok := a.backups(); ok {
		t.Errorf("config copies only = %+v, want left out", got)
	}

	older := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC)
	newest := time.Date(2026, 2, 5, 0, 0, 0, 0, time.UTC)
	write := func(path string, at time.Time, dir bool) {
		t.Helper()
		if dir {
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(path, []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, "backups", "pre-update-2026-02-01.zip"), older, false)
	write(filepath.Join(home, "state-snapshots", "20260203-000000"), newer, true)
	// A salvage from a failed backup is not a backup.
	write(filepath.Join(home, "backups", "hermes-backup-x.incomplete.zip"), newest, false)

	got, ok := a.backups()
	if !ok || !got.Configured || got.LastAt == nil || !got.LastAt.Equal(newer) || got.LastOK != nil {
		t.Errorf("backups = %+v, %v", got, ok)
	}
}

func TestHermesSignalsNeverCarryNames(t *testing.T) {
	pairing, _ := parsePairingList([]byte(pairingWithPending))
	update, _ := parseUpdateCheck([]byte("→ Update channel: stable\n→ Selected release available: stable v0.24.0 (0123456789ab)\n"), "0.23.1")
	report := adapter.SignalReport{Pairing: &pairing, Update: &update}
	data, err := json.Marshal(heartbeat.NewSignals(report, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"user-998877", "Someone Example", "req-0001", "Approved Person", "user-778899", "slack", "0123456789ab"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("sent %q: %s", secret, data)
		}
	}
}
