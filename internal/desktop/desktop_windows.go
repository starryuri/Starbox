//go:build windows

package desktop

import "github.com/jchv/go-webview2"

// Open opens a native desktop window hosting url. It blocks until the window
// is closed. The window is a real OS window (WebView2), not a browser tab.
func Open(url string) {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
	})
	defer w.Destroy()
	w.SetTitle("STARBOX")
	w.SetSize(1200, 800, webview2.HintNone)
	w.Navigate(url)
	w.Run()
}
