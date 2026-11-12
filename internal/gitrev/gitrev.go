// Package gitrev reads the commit a folder is at, for runtimes whose skills or
// settings are kept under git.
package gitrev

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

const (
	timeout   = 10 * time.Second
	maxOutput = 4 << 20
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

// Of reads the commit a folder is at, and whether it has changes beyond that
// commit. It returns nil when the folder is not a git checkout or git is not
// installed. It reads nothing about the repository's origin.
func Of(ctx context.Context, dir string) *adapter.Revision {
	if !filepath.IsAbs(dir) {
		return nil
	}
	commit, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return nil
	}
	hash := strings.TrimSpace(string(commit))
	if !commitPattern.MatchString(hash) {
		return nil
	}
	revision := &adapter.Revision{Commit: hash}
	if changes, err := git(ctx, dir, "status", "--porcelain"); err == nil {
		revision.Dirty = len(bytes.TrimSpace(changes)) > 0
	}
	return revision
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	// Never stop to ask for anything, and take no lock a running agent might want.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var stdout bytes.Buffer
	cmd.Stdout = &limited{w: &stdout, n: maxOutput}
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// limited keeps the first n bytes and quietly drops the rest, so a command
// that prints without end cannot fill memory.
type limited struct {
	w *bytes.Buffer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.n - l.w.Len(); room > 0 {
		if len(p) > room {
			l.w.Write(p[:room])
		} else {
			l.w.Write(p)
		}
	}
	return len(p), nil
}
