package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/beaconclaw/beaconclaw/internal/agent"
	"github.com/beaconclaw/beaconclaw/internal/config"
	"github.com/beaconclaw/beaconclaw/internal/daemon"
	"github.com/beaconclaw/beaconclaw/internal/openclaw"
	"github.com/beaconclaw/beaconclaw/internal/profile"
	"github.com/beaconclaw/beaconclaw/internal/update"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[beaconclaw] ")

	// Internal flag: when the binary is re-invoked by the service manager,
	// it passes --_daemon to run the agent loop directly.
	if len(os.Args) > 1 && os.Args[1] == "--_daemon" {
		name, set := "", false
		if len(os.Args) > 3 && os.Args[2] == "--profile" {
			name, set = os.Args[3], true
		}
		if err := profile.Resolve(name, set, executablePath()); err != nil {
			log.Fatalf("profile: %v", err)
		}
		runDaemonProcess()
		return
	}

	var profileFlag string
	root := &cobra.Command{
		Use:   "beaconclaw",
		Short: "BeaconClaw agent — fleet monitoring sidecar for OpenClaw",
		Long:  "Lightweight daemon that monitors a local OpenClaw gateway and reports health to the BeaconClaw fleet manager.",
		Run:   runRoot,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return profile.Resolve(profileFlag, cmd.Flags().Changed("profile"), executablePath())
		},
		SilenceUsage: true,
	}
	root.PersistentFlags().StringVar(&profileFlag, "profile", "",
		"Act on a named profile: a separate copy with its own config, identity, service and logs (env: "+profile.EnvVar+")")

	root.AddCommand(authCmd())
	root.AddCommand(installCmd())
	root.AddCommand(uninstallCmd())
	root.AddCommand(startCmd())
	root.AddCommand(stopCmd())
	root.AddCommand(restartCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(updateCmd())
	root.AddCommand(channelCmd())
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
		Short: "Authenticate this agent with an agent token",
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

	cmd.Flags().String("token", "", "Agent token (starts with bc_)")
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

			fmt.Printf("Profile:      %s\n", profile.DisplayName())
			fmt.Printf("Version:      %s (track: %s)\n", agent.Version, cfg.Channel)
			fmt.Printf("Agent ID:     %s\n", cfg.InstanceID)
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
		Short: "Print the agent version and release track",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(versionLine())
		},
	}
}

func versionLine() string {
	line := fmt.Sprintf("beaconclaw %s (track: %s", agent.Version, config.LoadChannel())
	if name := profile.Name(); name != "" {
		line += ", profile: " + name
	}
	return line + ")"
}

func channelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "channel [stable|beta]",
		Short: "Show or set the release track this copy updates from",
		Long: "Show or set the release track this copy updates from.\n\n" +
			"stable gets official releases only. beta also gets pre-releases.\n" +
			"Setting the track does not change the installed version; run 'beaconclaw update' for that.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if err := config.SaveChannel(args[0]); err != nil {
					return err
				}
			}
			fmt.Println(config.LoadChannel())
			return nil
		},
	}
}

func updateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update this copy to the newest release on its track",
		Long: "Update this copy to the newest release on its track.\n\n" +
			"The download is checked against the release's published checksum, the binary is\n" +
			"replaced in one step, and this profile's service is restarted if it was running.\n" +
			"If anything fails, the old binary stays in place. Updates never run automatically.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			channelFlag, _ := cmd.Flags().GetString("channel")
			pin, _ := cmd.Flags().GetString("version")
			checkOnly, _ := cmd.Flags().GetBool("check")

			channel := config.LoadChannel()
			if channelFlag != "" {
				if !config.ValidChannel(channelFlag) {
					return fmt.Errorf("unknown channel %q: use %s or %s", channelFlag, config.ChannelStable, config.ChannelBeta)
				}
				channel = channelFlag
			}

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			client := update.NewClient()

			target := pin
			if target == "" {
				latest, err := client.Latest(ctx, channel)
				if err != nil {
					return err
				}
				target = latest
			} else if _, err := update.ParseVersion(target); err != nil {
				return fmt.Errorf("--version: %w (expected a tag such as v0.1.0 or v0.1.0-beta.1)", err)
			}

			current := agent.Version
			fmt.Printf("Profile:   %s\n", profile.DisplayName())
			fmt.Printf("Track:     %s\n", channel)
			fmt.Printf("Installed: %s\n", current)
			if pin != "" {
				fmt.Printf("Requested: %s\n", target)
			} else {
				fmt.Printf("Latest:    %s\n", target)
			}

			upToDate := current == target
			if !upToDate && pin == "" {
				// Never move backwards unless a version was asked for by name.
				cv, cerr := update.ParseVersion(current)
				tv, terr := update.ParseVersion(target)
				if cerr == nil && terr == nil && cv.Compare(tv) > 0 {
					fmt.Printf("\nThe installed version is newer than the latest on the %s track. Nothing to do.\n", channel)
					fmt.Printf("To install it anyway: %s update --version %s\n", profile.BinaryName(), target)
					upToDate = true
				}
			} else if upToDate {
				fmt.Println("\nAlready up to date.")
			}

			if checkOnly {
				if !upToDate {
					fmt.Println("\nAn update is available. Run 'beaconclaw update' to install it.")
				}
				return nil
			}

			if !upToDate {
				exe := executablePath()
				if exe == "" {
					return fmt.Errorf("cannot find the path of the running binary")
				}
				fmt.Printf("\nUpdating %s ...\n", exe)
				opts := update.Options{
					ExecPath: exe,
					Warn:     func(msg string) { fmt.Println("Warning: " + msg) },
				}
				if s := daemon.GetStatus(); s.Installed && s.Running {
					opts.Restart = restartAndConfirm
				}
				if err := client.Apply(ctx, target, opts); err != nil {
					return fmt.Errorf("update failed, nothing was changed: %w", err)
				}
				fmt.Printf("Updated to %s.\n", target)
				if opts.Restart != nil {
					fmt.Println("Service restarted.")
				}
			}

			// Remember an explicitly chosen track once the update has succeeded.
			if channelFlag != "" && channelFlag != config.LoadChannel() {
				if err := config.SaveChannel(channelFlag); err != nil {
					return fmt.Errorf("save channel: %w", err)
				}
				fmt.Printf("This copy now follows the %s track.\n", channelFlag)
			}
			return nil
		},
	}

	cmd.Flags().String("channel", "", "Switch this copy to a release track: stable or beta")
	cmd.Flags().String("version", "", "Install an exact version, for example v0.1.0-beta.1")
	cmd.Flags().Bool("check", false, "Only report what is available; change nothing")
	cmd.MarkFlagsMutuallyExclusive("channel", "version")

	return cmd
}

// restartAndConfirm restarts this profile's service and checks that it is
// still running a few seconds later.
func restartAndConfirm() error {
	if err := daemon.Restart(); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		if !daemon.GetStatus().Running {
			return fmt.Errorf("service stopped shortly after restarting")
		}
	}
	return nil
}

// executablePath returns the real path of the running binary.
func executablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
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
