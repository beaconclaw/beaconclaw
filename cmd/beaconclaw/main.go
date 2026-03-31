package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/beaconclaw/beaconclaw/internal/agent"
	"github.com/beaconclaw/beaconclaw/internal/config"
	"github.com/beaconclaw/beaconclaw/internal/daemon"
	"github.com/beaconclaw/beaconclaw/internal/openclaw"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[beaconclaw] ")

	// Internal flag: when the binary is re-invoked by the service manager,
	// it passes --_daemon to run the agent loop directly.
	if len(os.Args) > 1 && os.Args[1] == "--_daemon" {
		runDaemonProcess()
		return
	}

	root := &cobra.Command{
		Use:   "beaconclaw",
		Short: "BeaconClaw agent — fleet monitoring sidecar for OpenClaw",
		Long:  "Lightweight daemon that monitors a local OpenClaw instance and reports health to the BeaconClaw fleet manager.",
		Run:   runRoot,
	}

	root.AddCommand(authCmd())
	root.AddCommand(installCmd())
	root.AddCommand(uninstallCmd())
	root.AddCommand(startCmd())
	root.AddCommand(stopCmd())
	root.AddCommand(restartCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(versionCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// runRoot displays version, daemon status, and help when invoked with no subcommand.
func runRoot(cmd *cobra.Command, args []string) {
	fmt.Printf("BeaconClaw %s\n", agent.Version)
	fmt.Println("Fleet monitoring sidecar for OpenClaw.")
	fmt.Println()

	s := daemon.GetStatus()
	if s.Installed {
		if s.Running {
			fmt.Printf("Daemon:       running (PID %d)\n", s.PID)
		} else {
			fmt.Println("Daemon:       installed, not running")
		}
	} else {
		fmt.Println("Daemon:       not installed")
	}
	fmt.Println()
	cmd.Help()
}

func authCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate this instance with an instance token",
		RunE: func(cmd *cobra.Command, args []string) error {
			token, _ := cmd.Flags().GetString("token")
			if token == "" {
				return fmt.Errorf("token required: beaconclaw auth --token <your-fleet-token>")
			}

			if err := config.SaveToken(token); err != nil {
				return fmt.Errorf("save token: %w", err)
			}

			fmt.Println("Token saved. Run 'beaconclaw install' to register the service, then 'beaconclaw start'.")
			return nil
		},
	}

	cmd.Flags().String("token", "", "Instance token (starts with bc_)")
	_ = cmd.MarkFlagRequired("token")

	return cmd
}

func installCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Register BeaconClaw as a system service (auto-starts on boot)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate config before installing
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if err := config.Validate(cfg); err != nil {
				return err
			}

			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve executable path: %w", err)
			}

			if err := daemon.Install(exe); err != nil {
				return fmt.Errorf("install service: %w", err)
			}

			fmt.Println("BeaconClaw service installed and started.")
			fmt.Println("The agent will auto-start on boot and restart on failure.")
			return nil
		},
	}
}

func uninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the BeaconClaw system service",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := daemon.Uninstall(); err != nil {
				return fmt.Errorf("uninstall service: %w", err)
			}

			fmt.Println("BeaconClaw service removed.")
			return nil
		},
	}
}

func startCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the BeaconClaw daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := daemon.Start(); err != nil {
				return err
			}
			fmt.Println("BeaconClaw daemon started.")
			return nil
		},
	}
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the BeaconClaw daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := daemon.Stop(); err != nil {
				return err
			}
			fmt.Println("BeaconClaw daemon stopped.")
			return nil
		},
	}
}

func restartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the BeaconClaw daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := daemon.Restart(); err != nil {
				return err
			}
			fmt.Println("BeaconClaw daemon restarted.")
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon and OpenClaw health status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			fmt.Printf("Instance ID:  %s\n", cfg.InstanceID)
			fmt.Printf("Manager:      %s\n", cfg.Endpoint)
			fmt.Printf("OpenClaw:     http://127.0.0.1:%d\n", cfg.OpenClawPort)

			s := daemon.GetStatus()
			if s.Installed {
				if s.Running {
					fmt.Printf("Daemon:       running (PID %d)\n", s.PID)
				} else {
					fmt.Println("Daemon:       installed, not running")
				}
			} else {
				fmt.Println("Daemon:       not installed")
			}
			fmt.Println()

			prober := openclaw.NewProber(cfg.OpenClawPort)
			status := prober.Probe(context.Background())

			fmt.Printf("Status:       %s\n", status.State)
			if status.Version != "unknown" {
				fmt.Printf("OC Version:   %s\n", status.Version)
			}
			if status.UptimeMs > 0 {
				fmt.Printf("Uptime:       %s\n", formatDuration(status.UptimeMs))
			}
			if len(status.FailingChannels) > 0 {
				fmt.Printf("Failing:      %v\n", status.FailingChannels)
			}

			return nil
		},
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the agent version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("beaconclaw %s\n", agent.Version)
		},
	}
}

// runDaemonProcess is the actual daemon entry point, invoked via --_daemon by the service manager.
func runDaemonProcess() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := config.Validate(cfg); err != nil {
		log.Fatalf("validate config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("received %s, shutting down...", sig)
		cancel()
	}()

	a := agent.New(cfg)
	if err := a.Run(ctx); err != nil {
		log.Fatalf("agent: %v", err)
	}
}

func formatDuration(ms int64) string {
	d := ms / 1000
	if d < 60 {
		return fmt.Sprintf("%ds", d)
	}
	if d < 3600 {
		return fmt.Sprintf("%dm%ds", d/60, d%60)
	}
	h := d / 3600
	m := (d % 3600) / 60
	return fmt.Sprintf("%dh%dm", h, m)
}
