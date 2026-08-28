//go:build windows

package main

import (
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
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

// scale adjusts a logical pixel value for the current monitor DPI.
func scale(n int) int {
	if dpiScale <= 100 || dpiScale > 500 {
		return n
	}
	return n * dpiScale / 100
}

var fontsScaled int = 100

// ensureFonts rebuilds all fonts when the DPI scale changed (avoids GDI
// churn on every resize) and pushes them onto every control.
func ensureFonts() {
	if fontsScaled == dpiScale {
		return
	}
	if fontsScaled != 100 {
		pDeleteObject.Call(fontTitle)
		pDeleteObject.Call(fontNav)
		pDeleteObject.Call(fontCard)
		pDeleteObject.Call(fontBody)
		pDeleteObject.Call(fontTiny)
	}
	fontTitle = createWin32Font(scale(34), true)
	fontNav = createWin32Font(scale(22), false)
	fontCard = createWin32Font(scale(26), false)
	fontBody = createWin32Font(scale(22), false)
	fontTiny = createWin32Font(scale(17), false)
	fontsScaled = dpiScale
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
	sidebarW := 320
	contentX := sidebarW + 30
	contentW := w - contentX - 30
	if contentW < 260 {
		contentW = 260
	}
	moveWin(hBrand, 30, 30, sidebarW-50, 52)
	moveWin(hTag, 30, 94, sidebarW-50, 32)
	navH := 58
	for i := range pages {
		moveWin(hNav[i], 26, 132+i*navH, sidebarW-54, navH-6)
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
	moveWin(hAuto, contentX, 106, 300, 50)
	moveWin(hAutoSave, contentX+310, 104, 130, 52)
	kbgap := 8
	kbw := (contentW - 4*kbgap) / 5
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
	moveWin(hKbAddBtn, contentX+inputW+10, 178, 150, 48)
	moveWin(hKbSearchBtn, contentX+inputW+170, 178, 160, 48)
	kbScroll = 0
	if kbCardMode() {
		refreshKB()
	}
	pInvalidateRect.Call(hwndMain, 0, 1)
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
	pShowWindow.Call(hAutoSave, pBool(setSet))
	for i := range kbCols {
		pShowWindow.Call(hKbTab[i], pBool(kbon))
	}
	pShowWindow.Call(hKbToA, pBool(kbon))
	pShowWindow.Call(hKbAddBtn, pBool(kbon))
	pShowWindow.Call(hKbSearchBtn, pBool(kbon))

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
		pSendMessage.Call(hAuto, 0x00F1, pBool(settings.Load(dataDir).AutoStart), 0) // BM_SETCHECK
		body = "设置：\n\n（设置页其余选项后续接入）"
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

