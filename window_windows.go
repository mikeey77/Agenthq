//go:build windows

package main

import (
	"path/filepath"
	"runtime"

	webview2 "github.com/jchv/go-webview2"
)

// The native window must run on the main OS thread.
func init() { runtime.LockOSThread() }

// nativeWindow opens Agent HQ in its own app window (WebView2) and blocks until it is closed.
// It returns false when WebView2 is not available, so the caller can fall back to a browser.
func nativeWindow(url string) bool {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		DataPath:  filepath.Join(configDir(), "webview"),
		WindowOptions: webview2.WindowOptions{
			Title: "Agent HQ", Width: 1520, Height: 960, IconId: 1, Center: true,
		},
	})
	if w == nil {
		return false
	}
	defer w.Destroy()
	w.Navigate(url)
	w.Run()
	return true
}
