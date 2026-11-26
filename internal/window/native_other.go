//go:build !(darwin && cgo)

package window

// Available reports whether this build has a native window. Without one, knd opens
// the UI in Chrome or Edge in app mode instead.
func Available() bool { return false }

// Run is never called when Available is false.
func Run(Options) {}

// SaveText is never called without the native window (the page downloads itself).
func SaveText(string, string) (string, error) { return "", nil }
