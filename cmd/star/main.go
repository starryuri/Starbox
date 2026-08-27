//go:build windows

// STARBOX — native Win32 desktop app (no WebView2, no Gio). Reliable clicks.
// Dark modern theme (navy + cyan accent), owner-drawn sidebar nav buttons.
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
	colMuted = 0xa9b4c8 // lighter gray
	colOnAcc = 0x170e0b // #0b0e17
	colCard  = 0x2f1c11 // #111c2f panel
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
)

var (
	hwndMain           uintptr
	hwndFont, hBigFont uintptr
	brushBg, brushCard uintptr
	hNav               [20]uintptr
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

func overviewText() string {
	var sb strings.Builder
	if c, err := cpu.Percent(0, false); err == nil && len(c) > 0 {
		sb.WriteString(fmt.Sprintf("CPU: %.1f%%\n", c[0]))
	}
	if m, err := mem.VirtualMemory(); err == nil {
		sb.WriteString(fmt.Sprintf("内存: %.1f%%  (%s / %s)\n", m.UsedPercent, humanBytes(m.Used), humanBytes(m.Total)))
	}
	if up, err := host.Uptime(); err == nil {
		sb.WriteString(fmt.Sprintf("运行: %s\n", fmtDuration(up)))
	}
	sb.WriteString("\n磁盘:\n")
	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range parts {
			if u, err := disk.Usage(p.Mountpoint); err == nil && u.Total > 0 {
				sb.WriteString(fmt.Sprintf("  %s  %.1f%%  %s / %s\n", p.Mountpoint, u.UsedPercent, humanBytes(u.Used), humanBytes(u.Total)))
			}
		}
	}
	if sb.Len() == 0 {
		return "（未能获取系统信息）"
	}
	return strings.TrimRight(sb.String(), "\n")
}

func renderPage() {
	title, body := pageLabels[page], ""
	switch page {
	case "overview":
		body = overviewText()
	case "settings":
		body = "开机自启动: " + boolStr(settings.Load(dataDir).AutoStart) + "\n\n（设置页后续接入）"
	default:
		body = "「" + pageLabels[page] + "」页面移植中，将逐个接入后台数据。"
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
		pSetBkMode.Call(wParam, 1) // TRANSPARENT
		if uintptr(0xFFFF)&wParam == IDBody {
			pSetTextColor.Call(wParam, colFg)
			pSetBkColor.Call(wParam, colCard)
			return brushCard
		}
		pSetTextColor.Call(wParam, colFg)
		return brushBg
	case 0x0010: // WM_CLOSE
		pDestroyWindow.Call(hwnd)
		return 0
	case 0x0002: // WM_DESTROY
		if hwndFont != 0 {
			pDeleteObject.Call(hwndFont)
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
		0x80000000, 0x80000000, 1180, 740,
		0, 0, hInst, 0)

	hwndFont = createWin32Font(15, false)
	hBigFont = createWin32Font(21, true)

	createChild("STATIC", "星匣 STARBOX", ssLeft, IDBrand, 30, 30, 200, 34, hBigFont)
	createChild("STATIC", "你的次元 · 收于一匣", ssLeft, 0, 30, 70, 200, 20, hwndFont)

	navL, navH := 240, 42
	for i, p := range pages {
		label := pageLabels[p]
		if p == page {
			label = "● " + label
		}
		hNav[i] = createChild("BUTTON", label, bsOwnerDraw, navBase+i, 24, 108+i*navH, navL-46, navH-6, hwndFont)
	}

	hTitle = createChild("STATIC", "", ssLeft, IDTitle, 300, 30, 840, 36, hBigFont)
	hBody = createChild("STATIC", "", ssLeft, IDBody, 300, 82, 840, 600, hwndFont)
	renderPage()

	pShowWindow.Call(hwndMain, 5)
	pUpdateWindow.Call(hwndMain)

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
