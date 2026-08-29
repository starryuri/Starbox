//go:build windows

package main

import (
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"


)
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

func clientSize() (int, int) {
	var rc rect
	pGetClientRect.Call(hwndMain, uintptr(unsafe.Pointer(&rc)))
	if rc.Right <= rc.Left {
		return 800, 600
	}
	return int(rc.Right - rc.Left), int(rc.Bottom - rc.Top)
}

// textWidth measures a string with the given font (in logical px).

type sizeStruct struct {
	Cx int32
	Cy int32
}


func textWidth(dc uintptr, text string, font uintptr) int {
	if font != 0 {
		pSelectObject.Call(dc, font)
	}
	tp, _ := windows.UTF16PtrFromString(text)
	n := 0
	for _, c := range text {
		_ = c
		n++
	}
	var sz sizeStruct
	pGetTextExtentPoint.Call(dc, uintptr(unsafe.Pointer(tp)), uintptr(n), uintptr(unsafe.Pointer(&sz)))
	return int(sz.Cx)
}

// drawTextRectFit draws text at drawX/drawY inside the given rect; if the
// text is wider than the rect at the requested font it retries with
// progressively smaller fonts (down to 12px) so nothing is clipped.
func drawTextRectFit(dc uintptr, x, y, w, h int, text string, size int, bold bool, rgb uintptr, flags uintptr) {
	for s := size; s >= 12; s -= 2 {
		f := createWin32Font(s, bold)
		defer pDeleteObject.Call(f)
		if textWidth(dc, text, f) <= w-8 {
			drawTextRect(dc, x, y, w, h, text, f, rgb, flags)
			return
		}
	}
	drawTextRect(dc, x, y, w, h, text, createWin32Font(12, bold), rgb, flags|0x00008000)
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

// --- edit subclass: forward WM_MOUSEWHEEL to the main window ---
// Without this, wheel scrolling dies once an EDIT control gains focus
// (classic Win32 focus trap: the focused edit swallows the wheel).

var (
	origEditProc    uintptr
	editWheelProcCb = syscall.NewCallback(editWheelProc)
)

func subclassEditWheel(h uintptr) {
	if h == 0 {
		return
	}
	r, _, _ := pSetWindowLongPtr.Call(h, ^uintptr(3), editWheelProcCb) // GWLP_WNDPROC = -4
	if origEditProc == 0 {
		origEditProc = r
	}
}

func editWheelProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	if msg == 0x020A && hwndMain != 0 { // WM_MOUSEWHEEL -> let the main window scroll cards/lists
		pSendMessage.Call(hwndMain, uintptr(msg), wp, lp)
		return 0
	}
	r, _, _ := pCallWindowProc.Call(origEditProc, hwnd, uintptr(msg), wp, lp)
	return r
}

// acquireSingleInstance creates a named mutex; false means another instance
// is already running (a second one could corrupt the JSON stores under data/).
// The existing window is raised to the foreground instead — professional apps
// never pop a modal "already running" box for a simple second launch.
func acquireSingleInstance() bool {
	CreateMutexW := kernel32.NewProc("CreateMutexW")
	name, _ := windows.UTF16PtrFromString("Local\\STARBOX.SingleInstance")
	_, _, err := CreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if e, ok := err.(syscall.Errno); ok && e == windows.ERROR_ALREADY_EXISTS {
		cls, _ := windows.UTF16PtrFromString("STARBOXMainWnd")
		if hwnd, _, _ := user32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(cls)), 0); hwnd != 0 {
			user32.NewProc("ShowWindow").Call(hwnd, 9) // SW_RESTORE
			user32.NewProc("SetForegroundWindow").Call(hwnd)
		}
		return false
	}
	return true
}

// enableDarkTitleBar switches the window title bar to the dark theme
// (DWMWA_USE_IMMERSIVE_DARK_MODE) so it matches the dark content.
func enableDarkTitleBar(hwnd uintptr) {
	dwm := windows.NewLazySystemDLL("dwmapi.dll")
	setAttr := dwm.NewProc("DwmSetWindowAttribute")
	on := int32(1)
	setAttr.Call(hwnd, 20, uintptr(unsafe.Pointer(&on)), 4)
}

func openURL(u string) {
	u = strings.TrimSpace(u)
	if u == "" || !(strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")) {
		return
	}
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	ShellExecuteW := shell32.NewProc("ShellExecuteW")
	op, _ := windows.UTF16PtrFromString("open")
	up, err := windows.UTF16PtrFromString(u)
	if err != nil {
		return
	}
	ShellExecuteW.Call(0, uintptr(unsafe.Pointer(op)), uintptr(unsafe.Pointer(up)), 0, 0, 5)
}

// msgBox shows a native message box owned by the main window.
func msgBox(text, caption string, flags uintptr) int {
	MessageBoxW := user32.NewProc("MessageBoxW")
	tp, _ := windows.UTF16PtrFromString(text)
	cp, _ := windows.UTF16PtrFromString(caption)
	r, _, _ := MessageBoxW.Call(hwndMain, uintptr(unsafe.Pointer(tp)), uintptr(unsafe.Pointer(cp)), flags)
	return int(r)
}

// confirmBox asks a yes/no question (used before destructive actions).
func confirmBox(text, caption string) bool {
	return msgBox(text, caption, 0x00000004|0x00000030) == 6 // MB_YESNO|MB_ICONWARNING, IDYES
}

// noticeBox shows an informational message.
func noticeBox(text, caption string) {
	msgBox(text, caption, 0x00000040) // MB_ICONINFORMATION
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
		return fmt.Sprintf("%d天%02d时", d, h)
	}
	if h > 0 {
		return fmt.Sprintf("%d:%02d时", h, m)
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

var platLabels = []string{"GitHub", "CSDN", "Bangumi", "AniList"}
var bindKeys = map[int]string{0: "github", 1: "csdn", 2: "bgmUser", 3: "anilistUser"}

