package hermes

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

const (
	// signalTimeout bounds each Hermes command run for the signals.
	signalTimeout = 60 * time.Second
	// maxSignalOutput is the most of a command's output that is read.
	maxSignalOutput = 1 << 20
	// The folders under Hermes's home where it keeps its own backups: the
	// archives made before an update or migration, or from its dashboard,
	// and its quick snapshots of state.
	backupsDir   = "backups"
	snapshotsDir = "state-snapshots"
	// maxBackupEntries bounds how many entries of a backup folder are looked at.
	maxBackupEntries = 10000
)

// Signals reads what Hermes can tell reliably without changing anything
// beyond its own housekeeping: how many pairing requests wait on each
// platform, and when its newest backup was made. Hermes has no
// pending-approval queue; its credential, doctor and audit commands print text
// that mixes in labels and messages or rewrite state as they run; and its
// update check fetches from its source repository and tidies the checkout. So
// those parts are not reported. The manager compares the version Hermes
// reports with the newest release itself.
func (a *Adapter) Signals(ctx context.Context) (adapter.SignalReport, bool) {
	report := adapter.SignalReport{}
	if out, err := a.runSignalCommand(ctx, "pairing", "list"); err == nil {
		if pairing, ok := parsePairingList(out); ok {
			report.Pairing = &pairing
		}
	}
	if backups, ok := a.backups(); ok {
		report.Backups = &backups
	}
	return report, !report.Empty()
}

// runSignalCommand runs one Hermes command on this adapter's home, with its
// own time limit. Its output is read here and never sent as it is.
func (a *Adapter) runSignalCommand(ctx context.Context, args ...string) ([]byte, error) {
	command := hermesCommand()
	if command == "" {
		return nil, exec.ErrNotFound
	}
	runCtx, cancel := context.WithTimeout(ctx, signalTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, command, args...)
	cmd.Env = append(os.Environ(), "HERMES_HOME="+a.home, "NO_COLOR=1")
	var stdout bytes.Buffer
	cmd.Stdout = &limitedBuffer{b: &stdout, n: maxSignalOutput}
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	if runCtx.Err() != nil {
		return nil, runCtx.Err()
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct {
	b *bytes.Buffer
	n int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.n - l.b.Len(); room > 0 {
		if len(p) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil
}

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

func plainLines(output []byte) []string {
	text := ansiPattern.ReplaceAllString(string(output), "")
	return strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

var (
	pendingHeader = regexp.MustCompile(`^\s*Pending Pairing Requests \(([0-9]+)\):\s*$`)
	ageField      = regexp.MustCompile(`^[0-9]+m$`)
)

// parsePairingList counts, from the text of "hermes pairing list", the
// pending requests on each platform. Only the first column of each pending
// row, the platform, is read; the request id, user id and name in the rest of
// the row are not kept, and approved users are not read at all. The counts
// must add up to the total Hermes prints, or nothing is reported.
func parsePairingList(output []byte) ([]adapter.PairingSignal, bool) {
	lines := plainLines(output)
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "No pairing data found") {
			return []adapter.PairingSignal{}, true
		}
	}

	start, total := -1, 0
	for i, line := range lines {
		if m := pendingHeader.FindStringSubmatch(line); m != nil {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				return nil, false
			}
			start, total = i, n
			break
		}
	}
	if start < 0 {
		for _, line := range lines {
			if strings.TrimSpace(line) == "No pending pairing requests." {
				return []adapter.PairingSignal{}, true
			}
		}
		return nil, false
	}

	// The rows follow the column headings and a line of dashes, and end at
	// the first blank line.
	i := start + 1
	for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "--------") {
		i++
	}
	if i >= len(lines) {
		return nil, false
	}
	counts := map[string]int{}
	rows := 0
	for i++; i < len(lines); i++ {
		fields := strings.Fields(lines[i])
		if len(fields) == 0 {
			break
		}
		// A row starts with the platform, in lowercase, and ends with its
		// age, such as "4m ago".
		platform := adapter.CleanSignalName(fields[0])
		if platform == "" || platform != fields[0] || len(fields) < 4 ||
			fields[len(fields)-1] != "ago" || !ageField.MatchString(fields[len(fields)-2]) {
			return nil, false
		}
		counts[platform]++
		rows++
	}
	if rows != total {
		return nil, false
	}
	signals := make([]adapter.PairingSignal, 0, len(counts))
	for platform, n := range counts {
		signals = append(signals, adapter.PairingSignal{Channel: platform, Pending: n})
	}
	sort.Slice(signals, func(i, j int) bool { return signals[i].Channel < signals[j].Channel })
	return signals, true
}

// backups finds the newest backup Hermes keeps in its home: an archive in
// backups/ or a quick snapshot in state-snapshots/. Only names and times are
// read, never contents. With neither folder holding one, nothing is reported:
// "hermes backup" writes to the user's home folder by default, which is not
// looked at, so the absence of backups here does not prove there are none.
func (a *Adapter) backups() (adapter.BackupsSignal, bool) {
	newest := newestBackup(filepath.Join(a.home, backupsDir), func(e os.DirEntry) bool {
		name := strings.ToLower(e.Name())
		return !e.IsDir() && strings.HasSuffix(name, ".zip") && !strings.HasSuffix(name, ".incomplete.zip")
	})
	snapshot := newestBackup(filepath.Join(a.home, snapshotsDir), func(e os.DirEntry) bool {
		return !strings.HasPrefix(e.Name(), ".")
	})
	if snapshot != nil && (newest == nil || snapshot.After(*newest)) {
		newest = snapshot
	}
	if newest == nil {
		return adapter.BackupsSignal{}, false
	}
	at := newest.UTC()
	return adapter.BackupsSignal{Configured: true, LastAt: &at}, true
}

func newestBackup(dir string, keep func(os.DirEntry) bool) *time.Time {
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer f.Close()
	entries, err := f.ReadDir(maxBackupEntries)
	if err != nil && len(entries) == 0 {
		return nil
	}
	var newest *time.Time
	for _, e := range entries {
		if !keep(e) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		t := info.ModTime()
		if newest == nil || t.After(*newest) {
			newest = &t
		}
	}
	return newest
}
