// Package window shows Kendra in a native window of its own: on macOS a WebKit
// (WKWebView) window, so Kendra has its own name in the menu bar, its own Dock icon
// and its own Cmd+Q, and is not a Chrome window. Builds without it (cgo off, or
// other systems) fall back to the browser; see Available.
package window

// Options is what the window needs to know.
type Options struct {
	Title   string
	URL     string
	Width   int
	Height  int
	Version string // shown in About
	Tagline string // one line under it in About
}
