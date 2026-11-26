//go:build darwin && cgo

package window

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>
void kndInstallMenu(const char *appName, const char *version, const char *tagline);
void kndRememberFrame(void *window);
char *kndSaveFile(const char *name);
*/
import "C"

import (
	"os"
	"runtime"
	"unsafe"

	webview "github.com/webview/webview_go"
)

func init() {
	// Cocoa must run on the process's main thread. Locking here, in init, keeps the
	// main goroutine on it until Run is called from main().
	runtime.LockOSThread()
}

// Available reports whether this build has a native window.
func Available() bool { return true }

// Run opens the window and blocks until it is closed. Must be called from main().
func Run(o Options) {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle(o.Title)
	w.SetSize(o.Width, o.Height, webview.HintNone)
	name, ver, tag := C.CString(o.Title), C.CString(o.Version), C.CString(o.Tagline)
	defer C.free(unsafe.Pointer(name))
	defer C.free(unsafe.Pointer(ver))
	defer C.free(unsafe.Pointer(tag))
	C.kndInstallMenu(name, ver, tag)
	C.kndRememberFrame(w.Window())
	w.Navigate(o.URL)
	w.Run()
}

// SaveText shows the Save panel and writes text to the chosen file. It returns the
// path, or "" when the user cancelled. Safe to call from any goroutine while the
// window runs.
func SaveText(name, text string) (string, error) {
	n := C.CString(name)
	defer C.free(unsafe.Pointer(n))
	r := C.kndSaveFile(n)
	if r == nil {
		return "", nil
	}
	defer C.free(unsafe.Pointer(r))
	path := C.GoString(r)
	return path, os.WriteFile(path, []byte(text), 0o644)
}
