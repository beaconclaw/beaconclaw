package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/beaconclaw/agent/internal/agent"
	"github.com/beaconclaw/agent/internal/config"
	"github.com/beaconclaw/agent/internal/openclaw"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[beaconclaw] ")

	root := &cobra.Command{
		Use:   "beaconclaw",
		Short: "BeaconClaw agent — fleet monitoring sidecar for OpenClaw",
		Long:  "Lightweight daemon that monitors a local OpenClaw instance and reports health to the BeaconClaw fleet manager.",
		RunE:  runDaemon,
	}

	root.Flags().String("token", "", "Fleet token (overrides BEACONCLAW_TOKEN)")
	root.Flags().String("endpoint", "", "Manager WebSocket endpoint (overrides BEACONCLAW_ENDPOINT)")

	root.AddCommand(versionCmd())
	root.AddCommand(statusCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func runDaemon(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// CLI flags override env/config
	if v, _ := cmd.Flags().GetString("token"); v != "" {
		cfg.Token = v
	}
	if v, _ := cmd.Flags().GetString("endpoint"); v != "" {
		cfg.Endpoint = v
	}

	if err := config.Validate(cfg); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown on SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("received %s, shutting down...", sig)
		cancel()
	}()

	a := agent.New(cfg)
	return a.Run(ctx)
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

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show agent connection status and OpenClaw health",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			fmt.Printf("Instance ID:  %s\n", cfg.InstanceID)
			fmt.Printf("Manager:      %s\n", cfg.Endpoint)
			fmt.Printf("OpenClaw:     http://127.0.0.1:%d\n", cfg.OpenClawPort)
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
