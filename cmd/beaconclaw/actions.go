package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/beaconclaw/beaconclaw/internal/actions"
	"github.com/beaconclaw/beaconclaw/internal/config"
	"github.com/beaconclaw/beaconclaw/internal/daemon"
	"github.com/beaconclaw/beaconclaw/internal/profile"
)

// actionsCmd switches remote actions on and off on this machine. It is the
// only way to switch one on: there is no setting for it on the manager and no
// environment variable.
func actionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "actions",
		Short: "Choose what the manager may ask this machine to do",
		Long: "Remote actions are an add-on, off unless you switch them on here. Each one is a\n" +
			"fixed thing this agent can do when the manager asks: there is no way for the\n" +
			"manager to send a command of its own, or to switch an action on.\n\n" +
			"Every request is recorded on this machine, whether it was carried out or refused.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			showActions()
			return nil
		},
	}

	allow := &cobra.Command{
		Use:     "allow <action>...",
		Short:   "Switch an action on, on this machine",
		Example: "  beaconclaw actions allow diagnostics\n  beaconclaw actions allow diagnostics restart_gateway",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !actions.Enabled {
				return fmt.Errorf("this copy was built without remote actions; none can be switched on")
			}
			for _, name := range args {
				if !actions.IsKnown(name) {
					return fmt.Errorf("%q is not an action. The actions are: %v", name, actions.Known())
				}
			}
			return saveActions(append(config.LoadActions(), args...))
		},
	}

	var all bool
	disallow := &cobra.Command{
		Use:     "disallow [<action>...]",
		Short:   "Switch an action off, or all of them with --all",
		Example: "  beaconclaw actions disallow restart_gateway\n  beaconclaw actions disallow --all",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !all && len(args) == 0 {
				return fmt.Errorf("say which action to switch off, or --all")
			}
			if all {
				return saveActions(nil)
			}
			drop := map[string]bool{}
			for _, name := range args {
				drop[name] = true
			}
			var keep []string
			for _, name := range config.LoadActions() {
				if !drop[name] {
					keep = append(keep, name)
				}
			}
			return saveActions(keep)
		},
	}
	disallow.Flags().BoolVar(&all, "all", false, "Switch every action off")

	cmd.AddCommand(allow, disallow)
	return cmd
}

// saveActions records the choice and restarts the service, because the agent
// tells the manager what is switched on when it connects.
func saveActions(names []string) error {
	if err := config.SaveActions(names); err != nil {
		return err
	}
	showActions()
	if daemon.GetStatus().Running {
		if err := daemon.Restart(); err != nil {
			return fmt.Errorf("saved, but the service could not be restarted: %w. Run '%s restart' for it to take effect", err, profile.BinaryName())
		}
		fmt.Println("\nThe service was restarted, so this is now in effect.")
	} else {
		fmt.Printf("\nThe service is not running. This takes effect when it starts.\n")
	}
	return nil
}

func showActions() {
	if !actions.Enabled {
		fmt.Println("This copy was built without remote actions. The manager can ask it for nothing.")
		return
	}
	on := map[string]bool{}
	for _, name := range config.LoadActions() {
		on[name] = true
	}
	fmt.Println("What the manager may ask this machine to do:")
	for _, name := range actions.Known() {
		state := "off"
		if on[name] {
			state = "ON "
		}
		fmt.Printf("  %s  %-16s %s\n", state, name, actions.Describe(name))
	}
	if len(on) == 0 {
		fmt.Println("\nNothing is switched on. The manager cannot ask this machine to do anything.")
	}
	fmt.Printf("\nSwitch one on:   %s actions allow <action>\n", profile.BinaryName())
	fmt.Printf("Switch all off:  %s actions disallow --all\n", profile.BinaryName())
	if _, err := os.Stat(config.ActionsLogPath()); err == nil {
		fmt.Printf("Record of requests on this machine: %s\n", config.ActionsLogPath())
	}
}
