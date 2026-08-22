//go:build windows

package desktop

import "github.com/jchv/go-webview2"

// Open opens a native desktop window hosting url. It blocks until the window
// is closed. The window is a real OS window (WebView2), not a browser tab.
func Open(url string) {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "星匣 STARBOX",
			Width:  1200,
			Height: 800,
			IconId: 1, // app icon resource (rsrc)
			Center: true,
		},
	})
	defer w.Destroy()
	w.SetTitle("星匣 STARBOX")
	w.SetSize(1200, 800, webview2.HintNone)
	w.Navigate(url)
	w.Run()
}
