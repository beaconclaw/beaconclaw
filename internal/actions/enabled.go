//go:build !noactions

package actions

// Enabled reports whether this build can perform remote actions at all. A
// copy built with the "noactions" tag cannot, whatever its settings say.
const Enabled = true
