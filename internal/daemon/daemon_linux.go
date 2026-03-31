//go:build linux

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	serviceName = "beaconclaw.service"
)

func unitPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", serviceName)
}

func logDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".beaconclaw", "logs")
}

func buildUnit(execPath string) string {
	return fmt.Sprintf(`[Unit]
Description=BeaconClaw Agent — fleet monitoring sidecar for OpenClaw
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s --_daemon
Environment="PATH=%s"
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, execPath, os.Getenv("PATH"))
}

func systemctl(args ...string) ([]byte, error) {
	return exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
}

func install(execPath string) error {
	// Ensure log directory exists
	if err := os.MkdirAll(logDir(), 0700); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	// Ensure systemd user directory exists
	dir := filepath.Dir(unitPath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create systemd user dir: %w", err)
	}

	// Write unit file
	if err := os.WriteFile(unitPath(), []byte(buildUnit(execPath)), 0644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}

	// Reload, enable, and start
	if out, err := systemctl("daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if out, err := systemctl("enable", serviceName); err != nil {
		return fmt.Errorf("enable: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if out, err := systemctl("restart", serviceName); err != nil {
		return fmt.Errorf("restart: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

func uninstall() error {
	if !isInstalled() {
		return fmt.Errorf("service is not installed")
	}

	// Disable and stop in one call
	systemctl("disable", "--now", serviceName)

	// Remove unit file
	if err := os.Remove(unitPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit file: %w", err)
	}

	// Reload daemon
	systemctl("daemon-reload")

	return nil
}

func start() error {
	if !isInstalled() {
		return fmt.Errorf("service not installed. Run 'beaconclaw install' first")
	}
	if out, err := systemctl("start", serviceName); err != nil {
		return fmt.Errorf("start: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func stop() error {
	if out, err := systemctl("stop", serviceName); err != nil {
		return fmt.Errorf("stop: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func restart() error {
	if !isInstalled() {
		return fmt.Errorf("service not installed. Run 'beaconclaw install' first")
	}
	if out, err := systemctl("restart", serviceName); err != nil {
		return fmt.Errorf("restart: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func isInstalled() bool {
	_, err := os.Stat(unitPath())
	return err == nil
}

func getStatus() Status {
	s := Status{Installed: isInstalled()}
	if !s.Installed {
		return s
	}

	out, err := systemctl("show", serviceName, "--no-page",
		"--property=ActiveState,SubState,MainPID")
	if err != nil {
		return s
	}

	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "ActiveState=active") {
			s.Running = true
		}
		if strings.HasPrefix(line, "MainPID=") {
			if pid, err := strconv.Atoi(strings.TrimPrefix(line, "MainPID=")); err == nil && pid > 0 {
				s.PID = pid
			}
		}
	}

	return s
}
