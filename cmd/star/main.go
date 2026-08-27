//go:build windows

// STARBOX — native Win32 desktop app (no WebView2, no Gio). Reliable clicks on
// Windows. Sidebar navigation + page content.
package main

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"butler/internal/config"
	"butler/internal/monitor"
	"butler/internal/settings"
)

// ---- Win32 plumbing ----
const (
	wsOverlappedWindow = 0x00CF0000
	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	wsTabStop          = 0x00010000
	ssLeft             = 0x00000000
	bsPushButton       = 0x00000000
	colorWindow        = 5
)

const (
	IDBrand  = 1
	navBase  = 100
	IDTitle  = 301
	IDBody   = 302
	IDStatus = 303
)

const dataDirName = "data"

var pages = []string{"overview", "disk", "rss", "insight", "kb", "favs", "notify", "rules", "settings"}

var pageLabels = map[string]string{
	"overview": "概况", "disk": "磁盘", "rss": "订阅", "insight": "情报",
	"kb": "知识库", "favs": "收藏", "notify": "通知", "rules": "规则", "settings": "设置",
}

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")

	pCreateWindowEx  = user32.NewProc("CreateWindowExW")
	pDefWindowProc   = user32.NewProc("DefWindowProcW")
	pDestroyWindow   = user32.NewProc("DestroyWindow")
	pRegisterClassEx = user32.NewProc("RegisterClassExW")
	pCreateFont      = gdi32.NewProc("CreateFontW")
	pSendMessage     = user32.NewProc("SendMessageW")
	pSetWindowText   = user32.NewProc("SetWindowTextW")
	pShowWindow      = user32.NewProc("ShowWindow")
	pUpdateWindow    = user32.NewProc("UpdateWindow")
	pInvalidateRect  = user32.NewProc("InvalidateRect")
	pDeleteObject    = gdi32.NewProc("DeleteObject")
	pLoadIcon        = user32.NewProc("LoadIconW")
	pPostQuitMessage = user32.NewProc("PostQuitMessage")
)

var (
	hwndMain      uintptr
	hwndFont      uintptr
	hNav          [20]uintptr
	hTitle, hBody uintptr
	page          string
	mgr           *monitor.State
	dataDir       string
	wndProc       = syscall.NewCallback(wndProcMain)
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

func utf16(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }

func setText(h uintptr, s string) {
	sp, _ := windows.UTF16PtrFromString(s)
	pSetWindowText.Call(h, uintptr(unsafe.Pointer(sp)))
}

func createWin32Font(size int, bold bool) uintptr {
	w := uintptr(400)
	if bold {
		w = 700
	}
	h, _, _ := pCreateFont.Call(uintptr(size), 0, 0, 0, w, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(utf16("Microsoft YaHei"))), 0)
	return h
}

func createChild(class, text string, style uint32, id, x, y, w, h int) uintptr {
	r, _, _ := pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16(class))),
		uintptr(unsafe.Pointer(utf16(text))),
		uintptr(wsChild|wsVisible|style),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		hwndMain, uintptr(id), 0, 0)
	if id != 0 {
		pSendMessage.Call(r, 0x0030, hwndFont, 1)
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

func renderPage() {
	title, body := pageLabels[page], ""
	switch page {
	case "overview":
		body = "这是原生 Win32 界面。\n\n侧边栏导航已可点（raw Win32），页面切换正常。\n\n后台数据（CPU/内存/磁盘/知识库等）将逐步接入。"
	case "settings":
		body = "开机自启动: " + boolStr(settings.Load(dataDir).AutoStart) + "\n\n（设置页后续接入）"
	case "disk", "rss", "insight", "kb", "favs", "notify", "rules":
		body = "「" + pageLabels[page] + "」页面移植中，将逐个接入后台数据。"
	default:
		title, body = page, "（页面移植中…）"
	}
	setText(hTitle, title)
	setText(hBody, body)
}

func boolStr(b bool) string {
	if b {
		return "开"
	}
	return "关"
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
	_ = mgr

	clsName := utf16("STARBOXMainWnd")
	wc := wndClassEx{
		Size:          uint32(unsafe.Sizeof(wndClassEx{})),
		Style:         0,
		WndProc:       wndProc,
		HInstance:     hInst,
		HIcon:         curIcon(hInst),
		HCursor:       0,
		HbrBackground: uintptr(colorWindow + 1),
		ClassName:     clsName,
	}
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))

	hwndMain, _, _ = pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(utf16("星匣 STARBOX"))),
		uintptr(wsOverlappedWindow),
		0x80000000, 0x80000000, 1080, 700,
		0, 0, hInst, 0)

	hwndFont = createWin32Font(15, false)
	createChild("STATIC", "星匣 STARBOX", ssLeft, IDBrand, 18, 26, 200, 30)
	navL, navH := 220, 40
	for i, p := range pages {
		label := pageLabels[p]
		if p == page {
			label = "● " + label
		}
		hNav[i] = createChild("BUTTON", label, bsPushButton, navBase+i, 18, 70+i*navH, navL-36, navH-4)
	}
	hTitle = createChild("STATIC", "", ssLeft, IDTitle, 260, 26, 780, 30)
	hBody = createChild("STATIC", "", ssLeft, IDBody, 260, 70, 780, 560)
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
