//go:build windows

package main

// tray.go — system tray integration (task 6 finale).
//
// Shell_NotifyIcon with the embedded app icon; left-click toggles the window,
// right-click opens a menu (打开/主题/退出). Honours settings:
//   - QuitAction "exit"  → closing the window exits
//   - QuitAction "tray"  → closing the window hides to tray instead
//   - SilentStart true   → starting with -tray keeps the window hidden
// The icon is destroyed on exit; balloon text greets on first minimize.

import (
	_ "embed"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"

	"butler/internal/settings"
)

//go:embed starbox.ico
var trayIcoData []byte

const (
	wmTrayIcon  = 0x8000 // WM_APP-based custom message for tray callbacks
	wmTrayMenu  = 0x8001
	idTray      = 1
	idMenuOpen  = 2001
	idMenuExit  = 2002
	idMenuNight = 2003
	idMenuSakura= 2004
	idMenuDay   = 2005
)

const (
	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002
	nifMessage= 0x00000001
	nifIcon   = 0x00000002
	nifTip    = 0x00000004
)

type notifyIconData struct {
	CbSize           uint32
	Hwnd             uintptr
UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
}

var (
	shell32t        = windows.NewLazySystemDLL("shell32.dll")
	pShellNotify    = shell32t.NewProc("Shell_NotifyIconW")
	pCreatePopupMenu= user32.NewProc("CreatePopupMenu")
	pAppendMenu     = user32.NewProc("AppendMenuW")
	pTrackPopupMenu = user32.NewProc("TrackPopupMenu")
	pDestroyMenu    = user32.NewProc("DestroyMenu")
	pGetCursorPos   = user32.NewProc("GetCursorPos")
	pLoadImage      = user32.NewProc("LoadImageW")
	pSetForeground = user32.NewProc("SetForegroundWindow")
	pDestroyIcon    = user32.NewProc("DestroyIcon")
	pSendMessageW   = user32.NewProc("SendMessageW")

	trayAdded    bool
	trayHIcon    uintptr
	trayVisible  bool
	quitToTray   bool
	silentStart  bool
	stt          settings.Settings
)


// initTray reads settings and decides window visibility; call once at startup
// after settings are loadable. Registers the tray icon always (so the user
// can reopen the window from the tray even after "exit to tray").
func initTray() {
	stt = settings.Load(curProfDir)
	quitToTray = stt.QuitAction == "tray"
	silentStart = stt.SilentStart

	// create HICON from the embedded .ico (write to temp + LoadImage)
	tmp := filepath.Join(os.TempDir(), "starbox-tray.ico")
	if err := os.WriteFile(tmp, trayIcoData, 0o644); err == nil {
		tp, _ := windows.UTF16PtrFromString(tmp)
		h, _, _ := pLoadImage.Call(0, uintptr(unsafe.Pointer(tp)), 1 /*IMAGE_ICON*/, 0, 0, 0x00000010|0x00000040) // LR_LOADFROMFILE|LR_DEFAULTSIZE
		trayHIcon = h
		_ = os.Remove(tmp)
	}

	addTrayIcon()

	// silent start: launched with -tray or -silent → keep window hidden
	for _, a := range os.Args[1:] {
		if a == "-tray" || a == "-silent" {
			silentStart = true
		}
	}
	if silentStart {
		pShowWindow.Call(hwndMain, 0) // SW_HIDE
		trayBalloon("STARBOX 已在后台运行", "点击图标打开主界面")
	}
}

type pt struct{ X, Y int32 }

func addTrayIcon() {
	if trayAdded || trayHIcon == 0 {
		return
	}
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.Hwnd = hwndMain
	nid.UID = idTray
	nid.UFlags = nifMessage | nifIcon | nifTip
	nid.UCallbackMessage = wmTrayIcon
	nid.HIcon = trayHIcon
	tip := utf16("星匣 STARBOX")
	copy(nid.SzTip[:], unsafe.Slice(tip, len(utf16slicesafe("星匣 STARBOX"))+1))
	pShellNotify.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	trayAdded = true
	trayVisible = true
}

func utf16slicesafe(s string) []uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return unsafe.Slice(p, len(s)+1)
}

func removeTrayIcon() {
	if !trayAdded {
		return
	}
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.Hwnd = hwndMain
	nid.UID = idTray
	pShellNotify.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	trayAdded = false
	trayVisible = false
}

func trayBalloon(title, text string) {
	if !trayAdded {
		return
	}
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.Hwnd = hwndMain
	nid.UID = idTray
	nid.UFlags = nifInfo
	nid.UVersion = 0
	tt := utf16(title)
	tx := utf16(text)
	copy(nid.SzInfoTitle[:], toU16(tt, 64))
	copy(nid.SzInfo[:], toU16(tx, 256))
	pShellNotify.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

const nifInfo = 0x00000010

func toU16(p *uint16, max int) []uint16 {
	if p == nil {
		return nil
	}
	out := make([]uint16, 0, max)
	for i := 0; i < max; i++ {
		c := *(*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + uintptr(i)*2))
		if c == 0 {
			break
		}
		out = append(out, c)
	}
	out = append(out, 0)
	return out
}

// trayToggleWindow shows/hides the main window from the tray.
func trayToggleWindow() {
	if isWindowVisible() {
		pShowWindow.Call(hwndMain, 0) // hide
	} else {
		pShowWindow.Call(hwndMain, 9) // SW_RESTORE
		pSetForeground.Call(hwndMain)
	}
}


func isWindowVisible() bool {
	r, _, _ := user32.NewProc("IsWindowVisible").Call(hwndMain)
	return r != 0
}

// trayMenu builds and tracks the right-click menu; returns the chosen id (0 = dismissed).
func trayMenu() int {
	menu, _, _ := pCreatePopupMenu.Call(0)
	if menu == 0 {
		return 0
	}
	defer pDestroyMenu.Call(menu)
	pAppendMenu.Call(menu, 0x00000000, idMenuOpen, uintptr(unsafe.Pointer(utf16("打开 主界面"))))           // MF_STRING
	pAppendMenu.Call(menu, 0x00000800, 0, 0)                                                            // MF_SEPARATOR
	pAppendMenu.Call(menu, 0x00000000, idMenuNight, uintptr(unsafe.Pointer(utf16("主题：暗夜"+themeMark("night")))))
	pAppendMenu.Call(menu, 0x00000000, idMenuSakura, uintptr(unsafe.Pointer(utf16("主题：樱夜"+themeMark("sakura")))))
	pAppendMenu.Call(menu, 0x00000000, idMenuDay, uintptr(unsafe.Pointer(utf16("主题：白天"+themeMark("day")))))
	pAppendMenu.Call(menu, 0x00000800, 0, 0)
	pAppendMenu.Call(menu, 0x00000000, idMenuExit, uintptr(unsafe.Pointer(utf16("退出"))))
	var p pt
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	pSetForeground.Call(hwndMain)
	chosen, _, _ := pTrackPopupMenu.Call(menu, 0x0180 /*TPM_RETURNCMD|TPM_RIGHTBUTTON*/, uintptr(p.X), uintptr(p.Y), 0, hwndMain, 0)
	return int(chosen)
}

func themeMark(id string) string {
	if id == activeThemeID {
		return " ✓"
	}
	return ""
}

// handleTrayMessage processes WM_USER tray callbacks.
func handleTrayMessage(lParam uintptr) {
	switch lParam {
	case 0x0202: // WM_LBUTTONUP → toggle
		trayToggleWindow()
	case 0x0205: // WM_RBUTTONUP → menu
		switch id := trayMenu(); id {
		case idMenuOpen:
			trayToggleWindow()
		case idMenuExit:
			removeTrayIcon()
			pPostMessage.Call(hwndMain, 0x0012 /*WM_QUIT via Destroy*/, 0, 0)
			pDestroyWindow.Call(hwndMain)
		case idMenuNight:
			switchTheme("night")
		case idMenuSakura:
			switchTheme("sakura")
		case idMenuDay:
			switchTheme("day")
		}
	}
}

// closeBehavior decides what WM_CLOSE does based on the current setting.
func closeBehavior() {
	if quitToTray {
		pShowWindow.Call(hwndMain, 0)
		trayBalloon("STARBOX 收纳到托盘", "点击图标打开；右键退出")
		return
	}
	removeTrayIcon()
	pDestroyWindow.Call(hwndMain)
}

// applyTraySettings rereads settings (called after settings save).
func applyTraySettings() {
	stt = settings.Load(curProfDir)
	quitToTray = stt.QuitAction == "tray"
	silentStart = stt.SilentStart
}

