package daemon

// Status represents the current state of the daemon service.
type Status struct {
	Installed bool
	Running   bool
	PID       int
}

// Install registers the daemon with the OS service manager.
// The execPath should be the absolute path to the beaconclaw binary.
func Install(execPath string) error {
	return install(execPath)
}

// Uninstall removes the daemon from the OS service manager.
func Uninstall() error {
	return uninstall()
}

// Start starts the daemon via the OS service manager.
func Start() error {
	return start()
}

// Stop stops the daemon via the OS service manager.
func Stop() error {
	return stop()
}

// Restart stops and starts the daemon via the OS service manager.
func Restart() error {
	return restart()
}

// GetStatus returns the current daemon status.
func GetStatus() Status {
	return getStatus()
}
