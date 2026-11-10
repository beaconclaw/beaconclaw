//go:build darwin

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/beaconclaw/beaconclaw/internal/profile"
)

// serviceLabel is ai.beaconclaw.agent, or ai.beaconclaw.agent.<profile>.
func serviceLabel() string {
	if name := profile.Name(); name != "" {
		return "ai.beaconclaw.agent." + name
	}
	return "ai.beaconclaw.agent"
}

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", serviceLabel()+".plist")
}

func logDir() string {
	return filepath.Join(profile.StateDir(), "logs")
}

func uid() string {
	return strconv.Itoa(os.Getuid())
}

func domain() string {
	return "gui/" + uid()
}

func buildPlist(execPath string) string {
	logs := logDir()
	var args strings.Builder
	for _, a := range daemonArgs() {
		args.WriteString("    <string>" + a + "</string>\n")
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
%s  </array>
  <key>StandardOutPath</key>
  <string>%s/agent.log</string>
  <key>StandardErrorPath</key>
  <string>%s/agent.err.log</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
</dict>
</plist>
`, serviceLabel(), execPath, args.String(), logs, logs)
}

func install(execPath string) error {
	// Ensure log directory exists
	if err := os.MkdirAll(logDir(), 0700); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	// Ensure LaunchAgents directory exists
	dir := filepath.Dir(plistPath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}

	// Unload existing service if present (ignore errors)
	if isInstalled() {
		exec.Command("launchctl", "bootout", domain()+"/"+serviceLabel()).Run()
	}

	// Write plist
	if err := os.WriteFile(plistPath(), []byte(buildPlist(execPath)), 0644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	// Bootstrap (register + auto-start due to RunAtLoad)
	if out, err := exec.Command("launchctl", "bootstrap", domain(), plistPath()).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

func uninstall() error {
	if !isInstalled() {
		return fmt.Errorf("service is not installed")
	}

	// Bootout (stop + unload)
	exec.Command("launchctl", "bootout", domain()+"/"+serviceLabel()).Run()

	// Remove plist
	if err := os.Remove(plistPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}

	return nil
}

func start() error {
	if !isInstalled() {
		return fmt.Errorf("service not installed. Run 'beaconclaw install' first")
	}
	out, err := exec.Command("launchctl", "kickstart", domain()+"/"+serviceLabel()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl kickstart: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func stop() error {
	out, err := exec.Command("launchctl", "bootout", domain()+"/"+serviceLabel()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootout: %s: %w", strings.TrimSpace(string(out)), err)
	}
	// Re-bootstrap so the service stays installed (just stopped)
	exec.Command("launchctl", "bootstrap", domain(), plistPath()).Run()
	return nil
}

func restart() error {
	if !isInstalled() {
		return fmt.Errorf("service not installed. Run 'beaconclaw install' first")
	}
	out, err := exec.Command("launchctl", "kickstart", "-k", domain()+"/"+serviceLabel()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl kickstart: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func isInstalled() bool {
	_, err := os.Stat(plistPath())
	return err == nil
}

func getStatus() Status {
	s := Status{Installed: isInstalled()}
	if !s.Installed {
		return s
	}

	out, err := exec.Command("launchctl", "print", domain()+"/"+serviceLabel()).CombinedOutput()
	if err != nil {
		return s
	}

	output := string(out)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "state = ") {
			if strings.Contains(line, "running") {
				s.Running = true
			}
		}
		if strings.HasPrefix(line, "pid = ") {
			if pid, err := strconv.Atoi(strings.TrimPrefix(line, "pid = ")); err == nil {
				s.PID = pid
				s.Running = true
			}
		}
	}

	return s
}
