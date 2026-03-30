//go:build windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	taskName = "BeaconClaw"
)

func stateDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".beaconclaw")
}

func scriptPath() string {
	return filepath.Join(stateDir(), "agent.cmd")
}

func logDir() string {
	return filepath.Join(stateDir(), "logs")
}

func buildScript(execPath string) string {
	logs := logDir()
	return fmt.Sprintf("@echo off\r\n\"%s\" --_daemon >> \"%s\\agent.log\" 2>> \"%s\\agent.err.log\"\r\n",
		execPath, logs, logs)
}

func schtasks(args ...string) ([]byte, error) {
	return exec.Command("schtasks", args...).CombinedOutput()
}

func install(execPath string) error {
	// Ensure directories exist
	if err := os.MkdirAll(logDir(), 0700); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	// Write launcher script
	if err := os.WriteFile(scriptPath(), []byte(buildScript(execPath)), 0644); err != nil {
		return fmt.Errorf("write script: %w", err)
	}

	// Register scheduled task to run at logon
	out, err := schtasks(
		"/Create", "/F",
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
		"/TN", taskName,
		"/TR", fmt.Sprintf(`"%s"`, scriptPath()),
	)
	if err != nil {
		return fmt.Errorf("schtasks create: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Start immediately
	if out, err := schtasks("/Run", "/TN", taskName); err != nil {
		return fmt.Errorf("schtasks run: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

func uninstall() error {
	if !isInstalled() {
		return fmt.Errorf("service is not installed")
	}

	// Delete scheduled task
	schtasks("/End", "/TN", taskName)
	if out, err := schtasks("/Delete", "/F", "/TN", taskName); err != nil {
		return fmt.Errorf("schtasks delete: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Remove script
	os.Remove(scriptPath())

	return nil
}

func start() error {
	if !isInstalled() {
		return fmt.Errorf("service not installed. Run 'beaconclaw install' first")
	}
	if out, err := schtasks("/Run", "/TN", taskName); err != nil {
		return fmt.Errorf("schtasks run: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func stop() error {
	if out, err := schtasks("/End", "/TN", taskName); err != nil {
		return fmt.Errorf("schtasks end: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func restart() error {
	stop() // ignore error — may not be running
	return start()
}

func isInstalled() bool {
	_, err := schtasks("/Query", "/TN", taskName)
	return err == nil
}

func getStatus() Status {
	s := Status{Installed: isInstalled()}
	if !s.Installed {
		return s
	}

	out, err := schtasks("/Query", "/TN", taskName, "/V", "/FO", "LIST")
	if err != nil {
		return s
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// Status field varies by locale; check for "Running"
		if strings.HasPrefix(line, "Status:") {
			if strings.Contains(line, "Running") {
				s.Running = true
			}
		}
	}

	return s
}
