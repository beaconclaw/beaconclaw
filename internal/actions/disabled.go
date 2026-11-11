//go:build noactions

package actions

// Enabled is false in a copy built with the "noactions" tag: it refuses every
// remote action and reports none as switched on, whatever its settings say.
const Enabled = false
