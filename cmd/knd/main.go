// Command knd is Kendra: a local window onto every Kubernetes cluster in your
// kubeconfig, with first-class support for Carvel kapp apps.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sandeeplamb/kendra/internal/kube"
	"github.com/sandeeplamb/kendra/internal/version"
	"github.com/sandeeplamb/kendra/internal/web"
	"github.com/sandeeplamb/kendra/internal/window"
)

const usage = `Kendra %s: a read-only console for your Kubernetes clusters and kapp apps.

Usage:
  knd [ui] [flags]     start Kendra and open it (default)
  knd version          print the version

Flags:
`

func main() {
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "version" || args[0] == "--version") {
		fmt.Println("kendra", version.Number)
		return
	}
	if len(args) > 0 && args[0] == "ui" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("knd", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:7799", "listen address (must be 127.0.0.1)")
	noOpen := fs.Bool("no-open", false, "print the address instead of opening a window")
	fs.Usage = func() { fmt.Fprintf(os.Stderr, usage, version.Number); fs.PrintDefaults() }
	_ = fs.Parse(args)
	mgr, err := kube.NewManager()
	if err != nil {
		fmt.Fprintln(os.Stderr, "knd:", err)
		os.Exit(1)
	}
	opts := web.Options{Addr: *addr, Open: !*noOpen}
	if opts.Open && window.Available() && os.Getenv("KND_BROWSER") != "1" {
		// Kendra's own window (macOS). Closing it quits Kendra.
		web.SaveFile = window.SaveText
		opts.Window = func(url string) {
			window.Run(window.Options{Title: "Kendra", URL: url, Width: 1440, Height: 900,
				Version: version.Number, Tagline: "A read-only console for your Kubernetes clusters and kapp apps."})
		}
	}
	if err := web.Serve(mgr, opts); err != nil {
		fmt.Fprintln(os.Stderr, "knd:", err)
		os.Exit(1)
	}
}
