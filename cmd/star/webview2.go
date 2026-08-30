//go:build windows

// webview2.go — WebView2-embedded anime detail page (Chromium rendering).
// The WebView lives in its own child window (hWebViewHost) that we position
// over the detail content area; all interaction stays on the UI thread and
// the native GDI painter remains the fallback when WebView2 is unavailable.
package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jchv/go-webview2/pkg/edge"
)

var (
	wvChromium *edge.Chromium
	wvReady    bool
	wvFailed   bool
	wvVisible  bool
	hWebViewHost uintptr
	wvPage      string // which page owns the web layer: "detail" | "disk" | "insight" | "settings"
	wvNavKey    string // last navigated page+version key (avoid redundant NavigateToString)
	wvDetailID  string // record currently rendered in the web layer
	wvVer       int    // webDataVer snapshot at last navigation
	webDataVer  int    // bump when detail data/theme changed
)

// webInit creates the child host window and the Chromium controller.
// Non-fatal on failure: we keep the GDI painter.
func webInit() {
	defer func() {
		if r := recover(); r != nil {
			wvFailed = true
		}
	}()
	hWebViewHost = createChild("STATIC", "", ssLeft, 0, 0, 0, 10, 10, 0)
	pShowWindow.Call(hWebViewHost, 0) // hidden until a detail page opens

	ch := edge.NewChromium()
	ch.DataPath = filepath.Join(dataDir, "webview2")
	ch.MessageCallback = webOnMessage
	if !ch.Embed(hWebViewHost) {
		wvFailed = true
		return
	}
	if st, err := ch.GetSettings(); err == nil {
		_ = st.PutAreDefaultContextMenusEnabled(false)
		_ = st.PutIsZoomControlEnabled(false)
		_ = st.PutIsStatusBarEnabled(false)
	}
	wvChromium = ch
	wvReady = true
}

// webResize positions the host over the detail content rect (or hides it).
func webResize() {
	if hWebViewHost == 0 {
		return
	}
	if !wvVisible {
		pShowWindow.Call(hWebViewHost, 0)
		if wvReady && wvChromium != nil {
			_ = wvChromium.Hide()
		}
		return
	}
	cx, cw, top, bottom := kbGeom()
	if wvPage == "disk" || wvPage == "insight" || wvPage == "settings" {
		cx, cw, top, bottom = pageGeom()
	}
	pShowWindow.Call(hWebViewHost, 5) // SW_SHOW: MoveWindow does not unhide
	pMoveWindow.Call(hWebViewHost, uintptr(cx), uintptr(top), uintptr(cw), uintptr(bottom-top), 1)
	if wvReady && wvChromium != nil {
		wvChromium.Resize() // fills the host client area
		_ = wvChromium.Show()
	} // SW_SHOW after MoveWindow keeps parent+child in sync
}

func webHideDetail() {
	wvPage = ""
	if wvVisible {
		wvVisible = false
		webResize()
	}
}

// webShowDetail renders the current anime record through WebView2.
// Returns false when WebView2 is unavailable (caller falls back to GDI).
func webShowDetail() bool {
	if !wvReady || wvFailed || wvChromium == nil {
		return false
	}
	r := curDetailRecord()
	if r == nil {
		webHideDetail()
		return true
	}
	wvPage = "detail"
	wvVisible = true
	webResize()
	if wvDetailID != r.ID || wvVer != webDataVer {
		wvDetailID = r.ID
		wvVer = webDataVer
		key := "detail|" + r.ID
		wvNavKey = key
		wvChromium.NavigateToString(buildDetailHTML(r))
	}
	return true
}

// webRefreshDetail re-renders the page in place (theme/fav/data changed).
func webRefreshDetail() {
	webDataVer++
	if hwndMain != 0 {
		pInvalidateRect.Call(hwndMain, 0, 1)
	}
}

// webShowPage claims the web layer for a full-content page (disk/insight/
// settings) and renders html. Safe to call repeatedly: navigation happens
// only when the payload key changes.
func webShowPage(name, key, html string) bool {
	if !wvReady || wvFailed || wvChromium == nil {
		return false
	}
	wvPage = name
	wvVisible = true
	webResize()
	if wvNavKey != key {
		wvNavKey = key
		wvDetailID = ""
		wvChromium.NavigateToString(html)
	}
	return true
}

// webOwner returns the current owner page ("" when hidden).
func webOwner() string {
	if !wvVisible {
		return ""
	}
	return wvPage
}

// atoiDefault2 parses an int with fallback 0.
func atoiDefault2(s string) int {
	n := 0
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

// webRefreshPage re-renders a full page in the web layer (bumps its key).
func webRefreshPage(name string) {
	webPageVers[name]++
	if hwndMain != 0 && wvVisible && wvPage == name {
		pInvalidateRect.Call(hwndMain, 0, 1)
	}
}

// webPageVers holds per-page re-render counters.
var webPageVers = map[string]int{}

// webOnMessage handles postMessage from the detail page.
func webOnMessage(msg string) {
	var m struct {
		T   string `json:"t"`
		ID  string `json:"id"`
		Val string `json:"v"`
	}
	if json.Unmarshal([]byte(msg), &m) != nil {
		return
	}
	switch m.T {
	case "hello":
		SetStatus("详情页渲染正常")
	case "err":
		SetError("详情页脚本错误：%s", m.Val)
	case "status":
		kbSetStatus(m.ID, m.Val)
	case "fav":
		p := strings.SplitN(m.Val, "|", 3)
		if len(p) == 3 {
			favToggle(p[1], p[0], atoiDefault(p[2]))
		}
	case "link":
		openURL(m.Val)
	case "back":
		onKBHit("back", "")
	case "watch":
		kbWatchInc(m.ID)
	case "del":
		kbDelete(m.ID)
	case "openbook":
		openLocalBook(m.Val)
	case "openpath":
		openLocalFolder(m.Val)
	case "opendir":
		diskDrillInto(m.Val)
	case "setscale":
		if n, err := strconv.Atoi(m.Val); err == nil {
			applyUiScale(n)
		}
	case "refreshins":
		refreshInsight()
		webRefreshPage("insight")
	case "open":
		openURL(m.Val)
	case "set":
		webSettingsAction(m.Val)
	}
}

func atoiDefault(s string) int {
	n := 0
	fmt.Sscanf(s, "%d", &n)
	return n
}
