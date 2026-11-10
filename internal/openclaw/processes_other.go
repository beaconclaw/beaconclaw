//go:build !linux && !darwin

package openclaw

// runningCommands is not implemented on this system. Version detection uses
// the installed command instead.
func runningCommands() [][]string { return nil }
