//go:build windows

// STARBOX — native Win32 desktop app (no WebView2, no Gio). Reliable clicks.
// Dark theme (navy + cyan accent), owner-drawn sidebar nav, responsive layout.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"butler/internal/config"
	"butler/internal/monitor"
	"butler/internal/settings"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	ssLeft             = 0x00000000
	bsOwnerDraw        = 0x0000000B
)

const (
	IDBrand = 1
	navBase = 100
	IDTitle = 301
	IDBody  = 302
	K_CARD  = 401
)

const dataDirName = "data"

var pages = []string{"overview", "disk", "rss", "insight", "kb", "favs", "notify", "rules", "settings"}

var pageLabels = map[string]string{
	"overview": "概况", "disk": "磁盘", "rss": "订阅", "insight": "情报",
	"kb": "知识库", "favs": "收藏", "notify": "通知", "rules": "规则", "settings": "设置",
}

// colors (COLORREF 0x00BBGGRR)
const (
	colBg    = 0x20100c // #0c1020
	colSide  = 0x2b1610 // #10162b
	colAcc   = 0xeed322 // #22d3ee
	colFg    = 0xf7ece7 // #e7ecf7
	colOnAcc = 0x170e0b
	colCard  = 0x4a3c20 // #203c4a
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")

	pCreateWindowEx   = user32.NewProc("CreateWindowExW")
	pDefWindowProc    = user32.NewProc("DefWindowProcW")
	pDestroyWindow    = user32.NewProc("DestroyWindow")
	pRegisterClassEx  = user32.NewProc("RegisterClassExW")
	pCreateFont       = gdi32.NewProc("CreateFontW")
	pSendMessage      = user32.NewProc("SendMessageW")
	pSetWindowText    = user32.NewProc("SetWindowTextW")
	pShowWindow       = user32.NewProc("ShowWindow")
	pUpdateWindow     = user32.NewProc("UpdateWindow")
	pInvalidateRect   = user32.NewProc("InvalidateRect")
	pDeleteObject     = gdi32.NewProc("DeleteObject")
	pLoadIcon         = user32.NewProc("LoadIconW")
	pPostQuitMessage  = user32.NewProc("PostQuitMessage")
	pCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	pSetTextColor     = gdi32.NewProc("SetTextColor")
	pSetBkMode        = gdi32.NewProc("SetBkMode")
	pSetBkColor       = gdi32.NewProc("SetBkColor")
	pFillRect         = user32.NewProc("FillRect")
	pDrawText         = user32.NewProc("DrawTextW")
	pGetDlgCtrlID     = user32.NewProc("GetDlgCtrlID")
	pGetClientRect    = user32.NewProc("GetClientRect")
	pMoveWindow       = user32.NewProc("MoveWindow")
)

var (
	hwndMain           uintptr
	fontTitle, fontNav uintptr
	fontCard, fontBody uintptr
	brushBg, brushCard uintptr
	hBrand, hTag       uintptr
	hNav               [20]uintptr
	hCards             [4]uintptr
	hTitle, hBody      uintptr
	page               string
	mgr                *monitor.State
	dataDir            string
	wndProc            = syscall.NewCallback(wndProcMain)
)

type wndClassEx struct {
	Size          uint32
	Style         uint32
	WndProc       uintptr
	ClsExtra      int32
	WndExtra      int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	MenuName      *uint16
	ClassName     *uint16
	HIconSm       uintptr
}

type msgStruct struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
}

type rect struct{ Left, Top, Right, Bottom int32 }

type drawItemStruct struct {
	CtlType    uint32
	CtlID      uint32
	ItemID     uint32
	ItemAction uint32
	ItemState  uint32
	HwndItem   uintptr
	HDC        uintptr
	RcItem     rect
	ItemData   uintptr
}

func utf16(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }

func setText(h uintptr, s string) {
	sp, _ := windows.UTF16PtrFromString(s)
	pSetWindowText.Call(h, uintptr(unsafe.Pointer(sp)))
}

func getText(h uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := pSendMessage.Call(h, 0x000D, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	return windows.UTF16ToString(buf[:n])
}

func createWin32Font(size int, bold bool) uintptr {
	w := uintptr(400)
	if bold {
		w = 700
	}
	h, _, _ := pCreateFont.Call(uintptr(size), 0, 0, 0, w, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(utf16("Microsoft YaHei"))), 0)
	return h
}

func createChild(class, text string, style uint32, id, x, y, w, h int, font uintptr) uintptr {
	r, _, _ := pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16(class))),
		uintptr(unsafe.Pointer(utf16(text))),
		uintptr(wsChild|wsVisible|style),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		hwndMain, uintptr(id), 0, 0)
	if id != 0 && font != 0 {
		pSendMessage.Call(r, 0x0030, font, 1)
	}
	return r
}

func curIcon(hInst uintptr) uintptr {
	r, _, _ := pLoadIcon.Call(hInst, 1)
	if r == 0 {
		r, _, _ = pLoadIcon.Call(0, 32512)
	}
	return r
}

func humanBytes(n uint64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	val := float64(n)
	i := -1
	for val >= u && i < len(units)-1 {
		val /= u
		i++
	}
	prec := 1
	if val >= 100 {
		prec = 0
	}
	return fmt.Sprintf("%.*f %s", prec, val, units[i])
}

func fmtDuration(sec uint64) string {
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%d 天 %d 小时", d, h)
	}
	if h > 0 {
		return fmt.Sprintf("%d 小时 %d 分", h, m)
	}
	return fmt.Sprintf("%d 分钟", m)
}

func boolStr(b bool) string {
	if b {
		return "开"
	}
	return "关"
}

func isCard(id uintptr) bool { return id >= K_CARD && id < uintptr(K_CARD+4) }

func updateCards() {
	var c0, m0, u0, d0 string
	if c, err := cpu.Percent(0, false); err == nil && len(c) > 0 {
		c0 = fmt.Sprintf("%.0f%%", c[0])
	}
	if m, err := mem.VirtualMemory(); err == nil {
		m0 = fmt.Sprintf("%.0f%%", m.UsedPercent)
	}
	if up, err := host.Uptime(); err == nil {
		u0 = fmtDuration(up)
	}
	if parts, err := disk.Partitions(false); err == nil && len(parts) > 0 {
		if u, err := disk.Usage(parts[0].Mountpoint); err == nil && u.Total > 0 {
			d0 = fmt.Sprintf("%.0f%%", u.UsedPercent)
		}
	}
	setText(hCards[0], "CPU\n"+c0)
	setText(hCards[1], "内存\n"+m0)
	setText(hCards[2], "运行\n"+u0)
	setText(hCards[3], "磁盘\n"+d0)
}

func diskText() string {
	var sb strings.Builder
	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range parts {
			if u, err := disk.Usage(p.Mountpoint); err == nil && u.Total > 0 {
				sb.WriteString(fmt.Sprintf("%s    %.1f%%    %s / %s\n", p.Mountpoint, u.UsedPercent, humanBytes(u.Used), humanBytes(u.Total)))
			}
		}
	}
	if sb.Len() == 0 {
		return "（未能获取磁盘信息）"
	}
	return strings.TrimRight(sb.String(), "\n")
}

func renderPage() {
	title, body := pageLabels[page], ""
	if page == "overview" {
		for i := range hCards {
			pShowWindow.Call(hCards[i], 1)
		}
		updateCards()
		body = diskText()
	} else {
		for i := range hCards {
			pShowWindow.Call(hCards[i], 0)
		}
		switch page {
		case "settings":
			body = "开机自启动: " + boolStr(settings.Load(dataDir).AutoStart) + "\n\n（设置页后续接入）"
		default:
			body = "「" + pageLabels[page] + "」页面移植中，将逐个接入后台数据。"
		}
	}
	setText(hTitle, title)
	setText(hBody, body)
}

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

// relayout positions all controls based on the current client size.
func relayout() {
	var rc rect
	pGetClientRect.Call(hwndMain, uintptr(unsafe.Pointer(&rc)))
	w, h := int(rc.Right), int(rc.Bottom)
	if w <= 0 || h <= 0 {
		return
	}
	sidebarW := 280
	contentX := sidebarW + 30
	contentW := w - contentX - 30
	if contentW < 240 {
		contentW = 240
	}
	moveWin(hBrand, 30, 30, sidebarW-50, 42)
	moveWin(hTag, 30, 86, sidebarW-50, 26)
	navH := 48
	for i := range pages {
		moveWin(hNav[i], 26, 126+i*navH, sidebarW-52, navH-6)
	}
	moveWin(hTitle, contentX, 30, contentW, 46)
	cardGap := 16
	cardW := (contentW - 3*cardGap) / 4
	if cardW < 100 {
		cardW = 100
	}
	cardH := 150
	for i := 0; i < 4; i++ {
		moveWin(hCards[i], contentX+i*(cardW+cardGap), 96, cardW, cardH)
	}
	bodyY := 96 + cardH + 26
	bodyH := h - bodyY - 30
	if bodyH < 60 {
		bodyH = 60
	}
	moveWin(hBody, contentX, bodyY, contentW, bodyH)
	pInvalidateRect.Call(hwndMain, 0, 1)
}

func wndProcMain(hwnd uintptr, msg uint32, wParam uintptr, lParam uintptr) uintptr {
	switch msg {
	case 0x0111: // WM_COMMAND
		id := uintptr(0xFFFF) & wParam
		if id >= navBase && id < uintptr(navBase+len(pages)) {
			page = pages[id-navBase]
			highlightNav()
			renderPage()
			pInvalidateRect.Call(hwndMain, 0, 1)
			return 0
		}
	case 0x002B: // WM_DRAWITEM
		return drawItem(uintptr(lParam))
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

func drawItem(diPtr uintptr) uintptr {
	di := (*drawItemStruct)(unsafe.Pointer(diPtr))
	if di.CtlID < navBase || di.CtlID >= uint32(navBase+len(pages)) {
		r, _, _ := pDefWindowProc.Call(hwndMain, 0x002B, 0, 0)
		return r
	}
	idx := di.CtlID - navBase
	fill := colSide
	tc := colFg
	if pages[idx] == page {
		fill = colAcc
		tc = colOnAcc
	}
	brush, _, _ := pCreateSolidBrush.Call(uintptr(fill))
	pFillRect.Call(di.HDC, uintptr(unsafe.Pointer(&di.RcItem)), brush)
	pDeleteObject.Call(brush)
	pSetBkMode.Call(di.HDC, 1)
	pSetTextColor.Call(di.HDC, uintptr(tc))
	tp, _ := windows.UTF16PtrFromString(getText(di.HwndItem))
	rc := di.RcItem
	pDrawText.Call(di.HDC, uintptr(unsafe.Pointer(tp)), uintptr(0xFFFFFFFF), uintptr(unsafe.Pointer(&rc)), 0x25)
	return 1
}

func main() {
	runtime.LockOSThread()
	user32.NewProc("SetProcessDPIAware").Call()
	mod, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	hInst := mod

	exe, _ := os.Executable()
	dataDir = filepath.Join(filepath.Dir(exe), dataDirName)
	_, _ = config.Load(filepath.Join(filepath.Dir(exe), "config.json"))
	mgr = monitor.New()
	page = "overview"

	brushBg, _, _ = pCreateSolidBrush.Call(uintptr(colBg))
	brushCard, _, _ = pCreateSolidBrush.Call(uintptr(colCard))

	clsName := utf16("STARBOXMainWnd")
	wc := wndClassEx{
		Size:          uint32(unsafe.Sizeof(wndClassEx{})),
		Style:         0,
		WndProc:       wndProc,
		HInstance:     hInst,
		HIcon:         curIcon(hInst),
		HCursor:       0,
		HbrBackground: brushBg,
		ClassName:     clsName,
	}
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))

	hwndMain, _, _ = pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(utf16("星匣 STARBOX"))),
		uintptr(wsOverlappedWindow),
		0x80000000, 0x80000000, 1280, 820,
		0, 0, hInst, 0)

	fontTitle = createWin32Font(26, true)
	fontNav = createWin32Font(18, false)
	fontCard = createWin32Font(20, false)
	fontBody = createWin32Font(18, false)

	hBrand = createChild("STATIC", "星匣 STARBOX", ssLeft, IDBrand, 30, 30, 230, 42, fontTitle)
	hTag = createChild("STATIC", "你的次元 · 收于一匣", ssLeft, 0, 30, 86, 230, 26, fontNav)
	for i, p := range pages {
		label := pageLabels[p]
		if p == page {
			label = "● " + label
		}
		hNav[i] = createChild("BUTTON", label, bsOwnerDraw, navBase+i, 26, 126+i*48, 228, 42, fontNav)
	}
	hTitle = createChild("STATIC", "", ssLeft, IDTitle, 310, 30, 900, 46, fontTitle)
	for i := 0; i < 4; i++ {
		hCards[i] = createChild("STATIC", "", ssLeft, K_CARD+i, 310+i*210, 96, 200, 150, fontCard)
	}
	hBody = createChild("STATIC", "", ssLeft, IDBody, 310, 280, 900, 480, fontBody)
	renderPage()

	pShowWindow.Call(hwndMain, 5)
	pUpdateWindow.Call(hwndMain)
	relayout()

	var msg msgStruct
	for {
		r, _, _ := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
		user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}
}
