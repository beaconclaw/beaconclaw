package openclaw

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestARestartIsStartedOnceEvenWhenItFails(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var ran []string
	err := restartOnce(context.Background(), []string{filepath.Join(dir, "missing"), first, second}, func(_ context.Context, command string) error {
		ran = append(ran, command)
		return errors.New("timed out")
	})

	if err == nil {
		t.Fatal("a failed restart was reported as done")
	}
	// The second copy of the command must not be tried: that would restart the gateway again.
	if len(ran) != 1 || ran[0] != first {
		t.Fatalf("ran %v, want only %s", ran, first)
	}
}

func TestARestartUsesTheFirstCopyOfTheCommandThatExists(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present")
	if err := os.WriteFile(present, nil, 0o755); err != nil {
		t.Fatal(err)
	}

	var ran []string
	err := restartOnce(context.Background(), []string{filepath.Join(dir, "missing"), present}, func(_ context.Context, command string) error {
		ran = append(ran, command)
		return nil
	})
	if err != nil || len(ran) != 1 || ran[0] != present {
		t.Fatalf("err=%v ran=%v", err, ran)
	}

	if err := restartOnce(context.Background(), []string{filepath.Join(dir, "missing")}, func(context.Context, string) error { return nil }); err == nil {
		t.Fatal("a restart with no command to run was reported as done")
	}
}
