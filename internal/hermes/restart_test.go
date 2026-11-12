//go:build !windows

package hermes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beaconclaw/beaconclaw/internal/adapter"
)

// fakeHermes puts a stand-in "hermes" command first on the PATH.
func fakeHermes(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	write(t, filepath.Join(bin, "hermes"), "#!/bin/sh\n"+script+"\n")
	if err := os.Chmod(filepath.Join(bin, "hermes"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := processWait
	processWait = 2 * time.Second
	t.Cleanup(func() { processWait = old })
}

func TestRestartRefusedIsNotSuccess(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, stateFile), `{"gateway_state":"running","pid":100,"start_time":5}`)
	fakeHermes(t, `echo "live work in progress; refusing" >&2; exit 1`)

	err := testAdapter(home, true).RestartGateway(context.Background())
	if !errors.Is(err, adapter.ErrNotRestarted) {
		t.Fatalf("a refused restart must be ErrNotRestarted, got %v", err)
	}
}

func TestRestartSeenByANewProcess(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, stateFile)
	write(t, state, `{"gateway_state":"running","pid":100,"start_time":5}`)
	fakeHermes(t, `printf '{"gateway_state":"running","pid":101,"start_time":9}' > "$HERMES_HOME/`+stateFile+`"`)

	if err := testAdapter(home, true).RestartGateway(context.Background()); err != nil {
		t.Fatalf("a new gateway process means it restarted, got %v", err)
	}
}
