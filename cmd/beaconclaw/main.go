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

	"github.com/beaconclaw/beaconclaw/internal/adapter"
	"github.com/beaconclaw/beaconclaw/internal/agent"
	"github.com/beaconclaw/beaconclaw/internal/catalogue"
	"github.com/beaconclaw/beaconclaw/internal/config"
	"github.com/beaconclaw/beaconclaw/internal/daemon"
	"github.com/beaconclaw/beaconclaw/internal/hermes"
	"github.com/beaconclaw/beaconclaw/internal/maintenance"
	"github.com/beaconclaw/beaconclaw/internal/openclaw"
	"github.com/beaconclaw/beaconclaw/internal/outbox"
	"github.com/beaconclaw/beaconclaw/internal/profile"
	"github.com/beaconclaw/beaconclaw/internal/runtimes"
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
		Short: "BeaconClaw agent — fleet monitoring sidecar for OpenClaw and Hermes",
		Long:  "Lightweight daemon that monitors a local agent runtime (OpenClaw or Hermes) and reports health to the BeaconClaw fleet manager.",
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
	root.AddCommand(inventoryCmd())
	root.AddCommand(maintenanceCmd())
	root.AddCommand(actionsCmd())
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
	fmt.Println("Fleet monitoring sidecar for OpenClaw and Hermes.")
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
		Long: "Save this copy's agent token, and optionally the manager address it reports to.\n\n" +
			"The token can be given with --token, or in the BEACONCLAW_TOKEN environment\n" +
			"variable to keep it out of the command line.",
		RunE: func(cmd *cobra.Command, args []string) error {
			token, _ := cmd.Flags().GetString("token")
			endpoint, _ := cmd.Flags().GetString("endpoint")
			if token == "" {
				token = os.Getenv("BEACONCLAW_TOKEN")
			}
			if token == "" {
				return fmt.Errorf("token required: %s auth --token <your-agent-token>", profile.BinaryName())
			}

			if err := config.SaveToken(token); err != nil {
				return fmt.Errorf("save token: %w", err)
			}
			if endpoint != "" {
				if err := config.SaveEndpoint(endpoint); err != nil {
					return err
				}
				fmt.Printf("Manager address saved: %s\n", endpoint)
			}

			fmt.Printf("Token saved. Run '%[1]s install' to register the service, then '%[1]s start'.\n", profile.BinaryName())
			return nil
		},
	}

	cmd.Flags().String("token", "", "Agent token (starts with bc_)")
	cmd.Flags().String("endpoint", "", "Manager WebSocket address to report to (default "+config.DefaultManagerEndpoint+")")

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

// maintenanceCmd tells the manager this agent is being worked on. It uses the
// agent's own token, so it can only ever affect this agent.
func maintenanceCmd() *cobra.Command {
	client := func() (*maintenance.Client, error) {
		cfg, err := config.Load()
		if err != nil {
			return nil, fmt.Errorf("load config: %w", err)
		}
		if err := config.Validate(cfg); err != nil {
			return nil, err
		}
		return maintenance.NewClient(cfg.Endpoint, cfg.Token, agent.Version)
	}

	cmd := &cobra.Command{
		Use:   "maintenance",
		Short: "Tell the manager this agent is being worked on",
		Long: "While an agent is in maintenance the manager holds back its alerts and leaves\n" +
			"the time out of its uptime. Start it before working on the agent and end it after.",
	}

	var reason string
	var length time.Duration
	start := &cobra.Command{
		Use:     "start",
		Short:   "Put this agent into maintenance from now",
		Example: "  beaconclaw maintenance start --reason \"runtime upgrade\" --for 2h",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client()
			if err != nil {
				return err
			}
			window, err := c.Start(cmd.Context(), reason, length)
			if err != nil {
				return err
			}
			fmt.Printf("In maintenance until %s (%s from now).\n",
				window.EndsAt.Local().Format("15:04 on 2 Jan"), time.Until(window.EndsAt).Round(time.Minute))
			fmt.Println("Run 'beaconclaw maintenance end' when the work is done.")
			return nil
		},
	}
	start.Flags().StringVar(&reason, "reason", "", "what the work is (required)")
	start.Flags().DurationVar(&length, "for", maintenance.DefaultFor, "how long it will take, such as 30m or 2h")
	start.MarkFlagRequired("reason")

	end := &cobra.Command{
		Use:   "end",
		Short: "Take this agent out of maintenance now",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client()
			if err != nil {
				return err
			}
			ended, err := c.End(cmd.Context())
			if err != nil {
				return err
			}
			if ended {
				fmt.Println("Maintenance ended.")
			} else {
				fmt.Println("This agent was not in maintenance.")
			}
			return nil
		},
	}

	cmd.AddCommand(start, end)
	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the daemon's and the agent runtime's health",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			fmt.Printf("Profile:      %s\n", profile.DisplayName())
			fmt.Printf("Version:      %s (track: %s)\n", agent.Version, cfg.Channel)
			fmt.Printf("Agent ID:     %s\n", cfg.InstanceID)
			fmt.Printf("Manager:      %s\n", cfg.Endpoint)

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
			// Heartbeats collected but not yet confirmed by the manager. One or two
			// is normal: the newest is usually still on its way.
			fmt.Printf("Waiting:      %s\n", waitingHeartbeats(outbox.Open(config.OutboxPath(), cfg.BufferCapacity).Len()))
			fmt.Println()

			runtime := runtimes.New(cfg)
			report := runtime.Probe(context.Background())
			fmt.Printf("Runtime:      %s (%s)\n", runtime.Name(), runtime.Target())

			// The lines below keep their original wording. The detail lines come
			// from the adapter's own data.
			if status, ok := report.Raw.(openclaw.Status); ok {
				fmt.Printf("Status:       %s\n", status.State)
				if report.Version != "" {
					fmt.Printf("OC Version:   %s\n", report.Version)
				}
				if status.UptimeMs > 0 {
					fmt.Printf("Uptime:       %s\n", formatDuration(status.UptimeMs))
				}
				if len(status.FailingChannels) > 0 {
					fmt.Printf("Failing:      %v\n", status.FailingChannels)
				}
			}
			if status, ok := report.Raw.(hermes.Status); ok {
				fmt.Printf("Status:       %s\n", status.State)
				if report.Version != "" {
					fmt.Printf("Version:      %s\n", report.Version)
				}
				if status.UptimeMs > 0 {
					fmt.Printf("Uptime:       %s\n", formatDuration(status.UptimeMs))
				}
				if len(status.Failing) > 0 {
					fmt.Printf("Failing:      %v\n", status.Failing)
				}
			}
			if !report.Present {
				fmt.Printf("Note:         %s was not found on this machine\n", runtime.Name())
			}

			return nil
		},
	}
}

// waitingHeartbeats says how many heartbeats the manager has not confirmed yet.
func waitingHeartbeats(count int) string {
	switch {
	case count == 0:
		return "none, the manager has confirmed every heartbeat"
	case count == 1:
		return "1 heartbeat not yet confirmed by the manager"
	default:
		return fmt.Sprintf("%d heartbeats not yet confirmed by the manager", count)
	}
}

// inventoryCmd shows, on the machine, what the inventory finds about connected
// systems, including what is deliberately not sent.
func inventoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inventory",
		Short: "Show which connected systems are reported, and what stays on this machine",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if !cfg.Inventory {
				fmt.Println("The inventory is switched off on this machine. Nothing is collected or sent for it.")
				return nil
			}

			runtime := runtimes.New(cfg)
			if runtime.Name() != runtimes.OpenClaw {
				inventorier, ok := runtime.(adapter.Inventorier)
				if !ok {
					fmt.Println("This runtime cannot report what it has loaded.")
					return nil
				}
				found := inventorier.Inventory(cmd.Context())
				fmt.Printf("BeaconClaw reads the names of what %s has set up. It never opens its environment file, and it never sees a credential.\n\n", runtime.Name())
				fmt.Printf("Connections (%d):\n", len(found.Connections))
				for _, connection := range found.Connections {
					fmt.Printf("  %-15s %s\n", connection.Kind, connection.Name)
				}
				fmt.Printf("Skills: %d\n", len(found.Skills))
				return nil
			}

			found := openclaw.NewAdapter(cfg.OpenClawPort).Inventory(cmd.Context())
			if found.Credentials == nil {
				fmt.Println("OpenClaw could not be asked which credentials its skills declare.")
				return nil
			}
			systems, unrecognised := catalogue.Sort(found.Credentials)

			fmt.Println("BeaconClaw asks OpenClaw which credentials its usable skills declare and have.")
			fmt.Println("It never opens your environment file, and it never sees a value.")
			fmt.Println()
			fmt.Printf("Sent as connected systems (%d):\n", len(systems))
			for _, system := range systems {
				fmt.Printf("  %s\n", system)
			}
			if len(systems) == 0 {
				fmt.Println("  none")
			}
			fmt.Println()
			if cfg.InventoryAllNames {
				fmt.Printf("Not recognised (%d). Their names ARE sent, because inventory_all_names is on:\n", len(unrecognised))
			} else {
				fmt.Printf("Not recognised (%d). Only this number is sent; the names stay here:\n", len(unrecognised))
			}
			for _, name := range unrecognised {
				fmt.Printf("  %s\n", name)
			}
			if len(unrecognised) == 0 {
				fmt.Println("  none")
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
