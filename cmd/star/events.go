//go:build windows

package main

import (
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"
	"unsafe"


	"butler/internal/settings"

)
func mouseXY(lParam uintptr) (int, int) {
	x := int(int16(uint16(lParam & 0xFFFF)))
	y := int(int16(uint16((lParam >> 16) & 0xFFFF)))
	return x, y
}

func hitTestKB(x, y int) string {
	if !kbCardMode() {
		return ""
	}
	if searchMode {
		for _, h := range detHits {
			if x >= h.x && x < h.x+h.w && y >= h.y && y < h.y+h.h {
				return h.action + "|" + h.id
			}
		}
		return ""
	}
	if detailID != "" {
		for _, h := range detHits {
			if x >= h.x && x < h.x+h.w && y >= h.y && y < h.y+h.h {
				return h.action + "|" + h.id
			}
		}
		return ""
	}
	kbCards = kbs2cards()
	for _, c := range kbCards {
		if x >= c.x && x < c.x+c.w && y >= c.y && y < c.y+c.h {
			return "card|" + c.id
		}
	}
	return ""
}

func paintFragment(dc uintptr) {
	// status strip under the page title (errors / notices)
	cx, cw, top, _ := kbGeom()
	paintStatusStrip(dc, cx, top-52, cw)
	if kbCardMode() {
		if detailID != "" {
			paintKBDetail(dc)
		} else {
			paintKBCards(dc)
		}
		return
	}
	if listMode() {
		paintListPage(dc)
	}
}

func wndProcMain(hwnd uintptr, msg uint32, wParam uintptr, lParam uintptr) uintptr {
	switch msg {
	case 0x0111: // WM_COMMAND
		id := uintptr(0xFFFF) & wParam
		if id >= navBase && id < uintptr(navBase+len(pages)) {
			page = pages[id-navBase]
			detailID = ""
			favDetailID = ""
			searchMode = false
			highlightNav()
			renderPage()
			pInvalidateRect.Call(hwndMain, 0, 1)
			return 0
		}
		if id >= IDPlat && id < IDPlat+4 {
			curPlat = int(id - IDPlat)
			loadBind()
			return 0
		}
		if id == IDSave {
			// GitHub tab: verify via API first; other tabs stay local-only saves
			if curPlat == 0 {
				verifyBind(getText(hPass))
			} else {
				saveBind()
			}
			return 0
		}
		if id == IDReff {
			refreshInsight()
			return 0
		}
		if id == IDReffMine {
			refreshMyRepos()
			return 0
		}
		if id == IDThN {
			switchTheme("night")
			return 0
		}
		if id == IDThS {
			switchTheme("sakura")
			return 0
		}
		if id == IDThD {
			switchTheme("day")
			return 0
		}
		if id == IDQuitE {
			pSendMessage.Call(hQuitT, 0x00F1, 0, 0) // uncheck tray
			return 0
		}
		if id == IDQuitT {
			pSendMessage.Call(hQuitE, 0x00F1, 0, 0) // uncheck exit
			return 0
		}
		if id == IDSaveS {
			on := uintptr(0)
			r, _, _ := pSendMessage.Call(hAuto, 0x00F0, 0, 0) // BM_GETCHECK
			if r == 1 {
				on = 1
			}
			silent := uintptr(0)
			pSendMessage.Call(hSilent, 0x00F0, 0, 0) // BM_GETCHECK
			silent = func() uintptr { v, _, _ := pSendMessage.Call(hSilent, 0x00F0, 0, 0); return v }()
			stt := settings.Load(dataDir)
			stt.AutoStart = on == 1
			stt.SilentStart = silent == 1
			qe, _, _ := pSendMessage.Call(hQuitE, 0x00F0, 0, 0)
			if qe == 1 {
				stt.QuitAction = "exit"
			} else {
				stt.QuitAction = "tray"
			}
			exe, _ := os.Executable()
			if err := settings.SetAutoStart(stt.AutoStart, exe); err != nil {
				SetError("自启动设置失败：%v", err)
			} else if stt.AutoStart {
				SetStatus("已开启开机自启动")
			} else {
				SetStatus("已关闭开机自启动")
			}
			if err := settings.Save(dataDir, stt); err != nil {
				SetError("保存设置失败：%v", err)
			}
			pInvalidateRect.Call(hwndMain, 0, 1)
			return 0
		}
		if id >= KBTab && id < uintptr(KBTab+5) {
			kbCol = kbCols[id-KBTab]
			detailID = ""
			kbScroll = 0
			searchMode = false
			if kbCardMode() {
				refreshKB()
				pInvalidateRect.Call(hwndMain, 0, 1)
			} else {
				setText(hBody, kbText())
			}
			return 0
		}
		if id == KBAdd {
			kbAdd()
			return 0
		}
		if id == KBSearch {
			runAnimeSearch()
			return 0
		}
	case 0x0202: // WM_LBUTTONUP
		if kbCardMode() {
			x, y := mouseXY(lParam)
			if h := hitTestKB(x, y); h != "" {
				parts := strings.SplitN(h, "|", 2)
				id := ""
				if len(parts) == 2 {
					id = parts[1]
				}
				onKBHit(parts[0], id)
				return 0
			}
		}
		if listMode() {
			x, y := mouseXY(lParam)
			if h := hitTestList(x, y); h != "" {
				parts := strings.SplitN(h, "|", 2)
				id := ""
				if len(parts) == 2 {
					id = parts[1]
				}
				onListHit(parts[0], id)
				return 0
			}
		}
	case 0x0200: // WM_MOUSEMOVE
		x, y := mouseXY(lParam)
		trackHover(hwnd)
		if updateHover(x, y) {
			pInvalidateRect.Call(hwnd, 0, 1)
		}
		return 0
	case 0x02A2: // WM_MOUSELEAVE
		hoverTrk = false
		if hoverAct != "" {
			hoverAct, hoverID = "", ""
			pInvalidateRect.Call(hwnd, 0, 1)
		}
		return 0
	case 0x020A: // WM_MOUSEWHEEL
		if kbCardMode() && detailID == "" && !searchMode {
			delta := int(int16(uint16((lParam >> 16) & 0xFFFF)))
			wheelAccum += delta
			step := 0
			for wheelAccum <= -120 {
				step += 90
				wheelAccum += 120
			}
			for wheelAccum >= 120 {
				step -= 90
				wheelAccum -= 120
			}
			kbScroll -= step
			if kbScroll < 0 {
				kbScroll = 0
			}
			kbCards = kbs2cards()
			clampKbScroll()
			pInvalidateRect.Call(hwndMain, 0, 1)
			return 0
		}
		if listMode() {
			delta := int(int16(uint16((lParam >> 16) & 0xFFFF)))
			listScroll -= delta / 120 * 90
			if listScroll < 0 {
				listScroll = 0
			}
			pInvalidateRect.Call(hwndMain, 0, 1)
			return 0
		}
	case 0x0113: // WM_TIMER
		if wParam == 1 && page == "overview" {
			loadOverview() // periodic refresh; guards itself on ovBusy
		}
		return 0
	case 0x000F: // WM_PAINT
		var ps paintStruct
		dc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if dc != 0 {
			w, h := clientSize()
			// draw into a memory DC, then blit once — kills repaint flicker
			mem, _, _ := pCreateCompatibleDC.Call(dc)
			if mem != 0 {
				var bi bitmapInfo
				bi.Size = 40
				bi.Width = int32(w)
				bi.Height = int32(-h)
				bi.Planes = 1
				bi.BitCount = 32
				bi.Compression = biRGB
				var bits *byte
				bmp, _, _ := pCreateDIBSection.Call(mem, uintptr(unsafe.Pointer(&bi)), dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
				if bmp != 0 && bits != nil {
					oldBmp, _, _ := pSelectObject.Call(mem, bmp)
					fillRectColor(mem, 0, 0, w, h, colBg)
					sidebarW := scale(320)
					if w > sidebarW {
						fillRectColor(mem, 0, 0, sidebarW, h, colSide)
					}
					paintFragment(mem)
					pBitBlt.Call(dc, 0, 0, uintptr(w), uintptr(h), mem, 0, 0, srcCopy)
					pSelectObject.Call(mem, oldBmp)
					pDeleteObject.Call(bmp)
				}
				pDeleteDC.Call(mem)
			}
			pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		}
		return 0
	case wmAppCover:
		// cover bitmap arrived -> repaint
		pInvalidateRect.Call(hwnd, 0, 1)
		return 0
	case wmAppRefresh:
		// record data changed on a worker thread -> reload slice here (UI thread)
		refreshKB()
		pInvalidateRect.Call(hwnd, 0, 1)
		return 0
	case wmOverview:
		if page == "overview" {
			setText(hCards[0], "CPU:\n"+ovStat[0])
			setText(hCards[1], "内存:\n"+ovStat[1])
			setText(hCards[2], "运行:\n"+ovStat[2])
			setText(hCards[3], "磁盘:\n"+ovStat[3])
			setText(hBody, ovBody)
		}
		return 0
	case wmAppRefreshNow:
		if page == "insight" {
			setText(hInfo, insText)
		}
		return 0
	case wmInsight:
		if page == "insight" {
			setText(hInfo, insText)
		}
		return 0
	case wmDisk:
		if page == "disk" {
			setText(hBody, dskBody)
		}
		return 0
	case wmRss:
		if page == "rss" {
			setText(hBody, rssText)
		}
		return 0
	case wmFavWorks:
		if page == "favs" {
			pInvalidateRect.Call(hwnd, 0, 1)
		}
		return 0
	case wmBindDone:
		if page == "insight" {
			setText(hHint, bindStatus)
		}
		return 0
	case wmDetail:
		if kbCardMode() && detailID != "" {
			pInvalidateRect.Call(hwnd, 0, 1)
		}
		return 0
	case wmStatusTick:
		pInvalidateRect.Call(hwnd, 0, 1)
		return 0
	case wmSearchDone:
		if kbCardMode() {
			pInvalidateRect.Call(hwnd, 0, 1)
		}
		return 0
	case 0x002B: // WM_DRAWITEM
		return drawItem(uintptr(lParam))
	case 0x0134: // WM_CTLCOLOREDIT
		pSetTextColor.Call(wParam, colFg)
		pSetBkMode.Call(wParam, 1)
		pSetBkColor.Call(wParam, colSide)
		return brushBg
	case 0x0138: // WM_CTLCOLORSTATIC
		id := uintptr(0)
		if lParam != 0 {
			r, _, _ := pGetDlgCtrlID.Call(lParam)
			id = r
		}
		switch {
		case id == IDBody:
			pSetTextColor.Call(wParam, colFg)
			pSetBkMode.Call(wParam, 0)
			pSetBkColor.Call(wParam, colSide)
			return brushBg
		case isCard(id):
			pSetTextColor.Call(wParam, colFg)
			pSetBkMode.Call(wParam, 0)
			pSetBkColor.Call(wParam, colCard)
			return brushCard
		default:
			pSetTextColor.Call(wParam, colFg)
			pSetBkMode.Call(wParam, 1)
			return brushBg
		}
	case 0x0005: // WM_SIZE
		// enforce a sane minimum so fixed-width controls never clip
		minW, minH := 1180, 700
		wCur := int(int16(uint16(lParam & 0xFFFF)))
		hCur := int(int16(uint16((lParam >> 16) & 0xFFFF)))
		if wParam == 0 && (wCur < minW || hCur < minH) { // SIZE_RESTORED
			rc := rect{}
			pGetWindowRect.Call(hwndMain, uintptr(unsafe.Pointer(&rc)))
			nx, ny := int(rc.Left), int(rc.Top)
			nw, nh := wCur, hCur
			if nw < minW {
				nw = minW
			}
			if nh < minH {
				nh = minH
			}
			pMoveWindow.Call(hwndMain, uintptr(nx), uintptr(ny), uintptr(nw), uintptr(nh), 1)
			return 0
		}
		relayout()
		r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
		return r
	case 0x0010: // WM_CLOSE
		pDestroyWindow.Call(hwnd)
		return 0
	case 0x0002: // WM_DESTROY
		if fontTitle != 0 {
			pDeleteObject.Call(fontTitle)
		}
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

