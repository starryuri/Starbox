//go:build windows

package main

import (
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"fmt"
	"unsafe"


	"butler/internal/settings"

)
func highlightNav() {
	for i, p := range pages {
		label := pageLabels[p]
		if p == page {
			label = "● " + label
		}
		setText(hNav[i], label)
	}
}

func moveWin(h uintptr, x, y, w, hh int) {
	pMoveWindow.Call(h, uintptr(x), uintptr(y), uintptr(w), uintptr(hh), 1)
}

// uiScale is the effective UI density (100 = base design). It starts at the
// monitor DPI scale but is capped so the minimum window (1180x700 logical)
// still fits the real desktop work area; that keeps fonts, spacing and
// layout shrinking together on small hi-DPI screens.
var uiScale int = 100

func computeUiScale() {
	uiScale = dpiScale
	if uiScale <= 100 || uiScale > 500 {
		uiScale = 100
		return
	}
	var wa rect
	user32.NewProc("SystemParametersInfoW").Call(0x0030, 0, uintptr(unsafe.Pointer(&wa)), 0)
	if wa.Right <= 0 || wa.Bottom <= 0 {
		return
	}
	// design min window at 100% density is 1180x700
	ww, wh := int(wa.Right)-int(wa.Left), int(wa.Bottom)-int(wa.Top)
	fitW := ww * 100 / 1180
	fitH := wh * 100 / 700
	fit := fitW
	if fitH < fit {
		fit = fitH
	}
	if fit < 100 {
		fit = 100
	}
	if uiScale > fit {
		uiScale = fit
	}
	// density cap: never magnify for DPI - the layout is adaptive and at 200%
	// the user saw giant fonts + clipped info.
	if uiScale > 100 {
		uiScale = 100
	}
}

// scale adjusts a logical pixel value for the effective UI density.
func scale(n int) int {
	if uiScale <= 100 || uiScale > 500 {
		return n
	}
	return n * uiScale / 100
}

var fontsScaled int = 100

// ensureFonts rebuilds all fonts when the DPI scale changed (avoids GDI
// churn on every resize) and pushes them onto every control.
func ensureFonts() {
	if fontsScaled == uiScale {
		return
	}
	if fontsScaled != 100 {
		pDeleteObject.Call(fontTitle)
		pDeleteObject.Call(fontNav)
		pDeleteObject.Call(fontCard)
		pDeleteObject.Call(fontBody)
		pDeleteObject.Call(fontTiny)
	}
	fontTitle = createWin32Font(scale(38), true)
	fontNav = createWin32Font(scale(24), false)
	fontCard = createWin32Font(scale(28), false)
	fontBody = createWin32Font(scale(23), false)
	fontTiny = createWin32Font(scale(18), false)
	fontsScaled = uiScale
	setFont := func(h, fnt uintptr) {
		if h != 0 {
			pSendMessage.Call(h, 0x0030, fnt, 1)
		}
	}
	setFont(hBrand, fontTitle)
	setFont(hTag, fontNav)
	for i := range hNav {
		setFont(hNav[i], fontNav)
	}
	setFont(hTitle, fontTitle)
	for i := range hCards {
		setFont(hCards[i], fontCard)
	}
	setFont(hBody, fontBody)
	for i := range hPlat {
		setFont(hPlat[i], fontNav)
	}
	setFont(hAcc, fontBody)
	setFont(hPass, fontBody)
	setFont(hSave, fontNav)
	setFont(hReff, fontNav)
	setFont(hReffMine, fontNav)
	setFont(hHint, fontNav)
	setFont(hInfo, fontBody)
	setFont(hAuto, fontNav)
	setFont(hAutoSave, fontNav)
	for i := range hKbTab {
		setFont(hKbTab[i], fontNav)
	}
	setFont(hKbToA, fontBody)
	setFont(hKbAddBtn, fontNav)
	setFont(hKbSearchBtn, fontNav)
}

// relayout positions all controls based on the current client size.
func relayout() {
	ensureFonts()
	var rc rect
	pGetClientRect.Call(hwndMain, uintptr(unsafe.Pointer(&rc)))
	w, h := int(rc.Right), int(rc.Bottom)
	if w <= 0 || h <= 0 {
		return
	}
	sidebarW := scale(320)
	contentX := sidebarW + scale(30)
	contentW := w - contentX - scale(30)
	if contentW < scale(260) {
		contentW = 260
	}
	moveWin(hBrand, 26, 26, sidebarW-44, 60)
	moveWin(hTag, 26, 96, sidebarW-44, 36)
	navH := 64
	for i := range pages {
		moveWin(hNav[i], 26, 138+i*navH, sidebarW-54, navH-8)
	}
	moveWin(hTitle, contentX, 30, contentW, 54)
	cardGap := 18
	cardW := (contentW - 3*cardGap) / 4
	if cardW < 120 {
		cardW = 120
	}
	cardH := 185
	for i := 0; i < 4; i++ {
		moveWin(hCards[i], contentX+i*(cardW+cardGap), 106, cardW, cardH)
	}
	bodyY := 106 + cardH + 30
	bodyH := h - bodyY - 34
	if bodyH < 60 {
		bodyH = 60
	}
	moveWin(hBody, contentX, bodyY, contentW, bodyH)
	// insight controls
	platGap := 8
	platW := (contentW - 3*platGap) / 4
	if platW < 120 {
		platW = 120
	}
	for i := 0; i < 4; i++ {
		moveWin(hPlat[i], contentX+i*(platW+platGap), 106, platW, 52)
	}
	accW := (contentW - 260) / 2
	if accW < 200 {
		accW = 200
	}
	moveWin(hAcc, contentX, 182, accW, 42)
	moveWin(hPass, contentX+accW+10, 182, accW, 42)
	moveWin(hSave, contentX+2*accW+20, 180, contentW-2*accW-20, 46)
	moveWin(hReff, contentX, 246, 140, 44)
	moveWin(hReffMine, contentX+150, 246, 140, 44)
	moveWin(hHint, contentX+150, 248, contentW-150, 36)
	moveWin(hInfo, contentX, 306, contentW, h-306-34)
	// settings page: fluid grid inside contentW (no fixed pixel offsets that
	// can overflow a 1180px-min window and get clipped)
	half := (contentW - scale(16)) / 2
	if half < scale(300) {
		half = scale(300)
	}
	moveWin(hAuto, contentX, 106, half, 50)
	moveWin(hSilent, contentX+half+scale(16), 106, contentW-half-scale(16), 50)
	moveWin(hAutoSave, contentX, 166, scale(150), 44)
	third := (contentW - 2*scale(12)) / 3
	if third < scale(130) {
		third = scale(130)
	}
	moveWin(hThN, contentX, 222, third, 44)
	moveWin(hThS, contentX+third+scale(12), 222, third, 44)
	moveWin(hThD, contentX+2*(third+scale(12)), 222, third, 44)
	moveWin(hQuitE, contentX, 286, half, 44)
	moveWin(hQuitT, contentX+half+scale(16), 286, contentW-half-scale(16), 44)
	moveWin(hProfLabel, contentX, 366, contentW, 36)
	qw := scale(130)
	nw := scale(130)
	ew := scale(220)
	gap2 := scale(10)
	used := 2*(qw+gap2) + ew + gap2 + nw + gap2
	if used > contentW {
		// narrow window: stack name row into two lines
		moveWin(hProfPrev, contentX, 412, qw, 42)
		moveWin(hProfNext, contentX+qw+gap2, 412, qw, 42)
		nameW := contentW - 2*(qw+gap2)
		if nameW < scale(120) {
			nameW = scale(120)
		}
		moveWin(hProfName, contentX, 462, nameW, 40)
		moveWin(hProfNew, contentX, 512, nw, 42)
		moveWin(hProfDel, contentX+nw+gap2, 512, scale(130), 42)
	} else {
		nameW := contentW - used - nw - gap2 - scale(130)
		if nameW < scale(120) {
			nameW = scale(120)
		}
		moveWin(hProfPrev, contentX, 412, qw, 42)
		moveWin(hProfNext, contentX+qw+gap2, 412, qw, 42)
		moveWin(hProfName, contentX+2*(qw+gap2), 414, nameW, 40)
		nx := contentX + 2*(qw+gap2) + nameW + gap2
		moveWin(hProfNew, nx, 412, nw, 42)
		moveWin(hProfDel, nx+nw+gap2, 412, scale(130), 42)
	}
	kbgap := 8
	kbN := len(kbCols)
	kbw := (contentW - (kbN-1)*(kbgap+1)) / kbN
	if kbw < 110 {
		kbw = 110
	}
	for i := range kbCols {
		moveWin(hKbTab[i], contentX+i*(kbw+kbgap), 106, kbw, 52)
	}
	inputW := contentW - 340
	if inputW < 220 {
		inputW = 220
	}
	moveWin(hKbToA, contentX, 182, inputW, 44)
	btn2x := contentX + inputW + 10
	if btn2x+320 > contentX+contentW {
		btn2x = contentX + contentW - 320
	}
	if btn2x < contentX+inputW+10 {
		btn2x = contentX + inputW + 10
	}
	moveWin(hKbAddBtn, btn2x, 178, 150, 48)
	moveWin(hKbSearchBtn, btn2x+160, 178, 160, 48)
	kbScroll = 0
	if kbCardMode() {
		refreshKB()
	}
	pInvalidateRect.Call(hwndMain, 0, 1)
}

// updateProfLabel refreshes the identity line on the settings page.
func updateProfLabel() {
	if hProfLabel != 0 {
		setText(hProfLabel, "当前身份： "+currentProfileName()+"  （身份共 "+fmt.Sprintf("%d", len(profiles))+" 个，每个身份拥有独立的番剧库/收藏/主题/设置）")
	}
}

func renderPage() {
	title := pageLabels[page]
	overview := page == "overview"
	insight := page == "insight"
	kbon := page == "kb"
	setSet := page == "settings"
	for i := range hCards {
		pShowWindow.Call(hCards[i], pBool(overview))
	}
	for i := range hPlat {
		pShowWindow.Call(hPlat[i], pBool(insight))
	}
	pShowWindow.Call(hAcc, pBool(insight))
	pShowWindow.Call(hPass, pBool(insight))
	pShowWindow.Call(hSave, pBool(insight))
	pShowWindow.Call(hReff, pBool(insight))
	pShowWindow.Call(hReffMine, pBool(insight))
	pShowWindow.Call(hHint, pBool(insight))
	pShowWindow.Call(hInfo, pBool(insight))
	pShowWindow.Call(hAuto, pBool(setSet))
	pShowWindow.Call(hSilent, pBool(setSet))
	pShowWindow.Call(hAutoSave, pBool(setSet))
	pShowWindow.Call(hThN, pBool(setSet))
	pShowWindow.Call(hThS, pBool(setSet))
	pShowWindow.Call(hThD, pBool(setSet))
	pShowWindow.Call(hQuitE, pBool(setSet))
	pShowWindow.Call(hQuitT, pBool(setSet))
	pShowWindow.Call(hProfLabel, pBool(setSet))
	pShowWindow.Call(hProfPrev, pBool(setSet))
	pShowWindow.Call(hProfNext, pBool(setSet))
	pShowWindow.Call(hProfName, pBool(setSet))
	pShowWindow.Call(hProfNew, pBool(setSet))
	pShowWindow.Call(hProfDel, pBool(setSet))
	for i := range kbCols {
		pShowWindow.Call(hKbTab[i], pBool(kbon))
	}
	pShowWindow.Call(hKbToA, pBool(kbon))
	pShowWindow.Call(hKbAddBtn, pBool(kbon))
	pShowWindow.Call(hKbSearchBtn, pBool(kbon && kbCol == "anime")) // 网络搜索仅番剧栏目

	cm := kbCardMode()
	lm := listMode()
	pShowWindow.Call(hBody, pBool(!insight && !setSet && !cm && !lm))
	if cm || lm {
		setText(hBody, "")
	}

	var body string
	switch {
	case overview:
		loadOverview()
		body = "（正在获取系统信息…）"
	case insight:
		loadBind()
		loadInsight()
	case setSet:
		stt := settings.Load(dataDir)
		pSendMessage.Call(hAuto, 0x00F1, pBool(stt.AutoStart), 0) // BM_SETCHECK
		pSendMessage.Call(hSilent, 0x00F1, pBool(stt.SilentStart), 0)
		updateProfLabel()
		if stt.QuitAction == "exit" {
			pSendMessage.Call(hQuitE, 0x00F1, 1, 0)
			pSendMessage.Call(hQuitT, 0x00F1, 0, 0)
		} else {
			pSendMessage.Call(hQuitE, 0x00F1, 0, 0)
			pSendMessage.Call(hQuitT, 0x00F1, 1, 0)
		}
		body = ""
	case kbon:
		if cm {
			refreshKB()
			pInvalidateRect.Call(hwndMain, 0, 1)
		} else {
			body = kbText()
		}
	case page == "disk":
		loadDisk()
		body = "（正在扫描磁盘…）"
	case page == "rss":
		loadRSS()
		body = "（正在获取订阅…）"
	case page == "notify":
		collectNotifsOnce()
	case lm:
		listPage = page
		listScroll = 0
		favDetailID = ""
		refreshList()
		pInvalidateRect.Call(hwndMain, 0, 1)
	default:
		body = "「" + pageLabels[page] + "」页面移植中，将逐个接入后台数据。"
	}
	setText(hTitle, title)
	if !cm && !lm {
		setText(hBody, body)
	}
}

// ---------- message handling ----------

