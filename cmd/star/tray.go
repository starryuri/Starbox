//go:build windows

package main

// tray.go — system tray integration (task 6 finale).
//
// Shell_NotifyIcon with the embedded app icon; left-click toggles the window,
// right-click opens a menu (open / themes / exit). Behavior:
//   - QuitAction "exit"  → closing the window exits
//   - QuitAction "tray"  → closing the window hides to tray with a balloon
//   - -tray / -silent    → starting hidden in the tray
// The icon is removed on exit.

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
	wmTrayIcon  = 0x8000
	idTray      = 1
	idMenuOpen  = 2001
	idMenuExit  = 2002
	idMenuNight = 2003
	idMenuSakura = 2004
	idMenuDay   = 2005

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010
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
	shell32t     = windows.NewLazySystemDLL("shell32.dll")
	pShellNotify = shell32t.NewProc("Shell_NotifyIconW")
	pCreatePopup = user32.NewProc("CreatePopupMenu")
	pAppendMenu  = user32.NewProc("AppendMenuW")
	pTrackMenu   = user32.NewProc("TrackPopupMenu")
	pDestroyMenu = user32.NewProc("DestroyMenu")
	pSetFG       = user32.NewProc("SetForegroundWindow")
	pGetCursorP  = user32.NewProc("GetCursorPos")
	pLoadImageI  = user32.NewProc("LoadImageW")

	trayAdded   bool
	trayHIcon   uintptr
	quitToTray  bool
	silentStart bool
	stt         settings.Settings
)

type trayPt struct{ X, Y int32 }

func initTray() {
	stt = settings.Load(curProfDir)
	quitToTray = stt.QuitAction == "tray"
	silentStart = stt.SilentStart

	tmp := filepath.Join(os.TempDir(), "starbox-tray.ico")
	if err := os.WriteFile(tmp, trayIcoData, 0o644); err == nil {
		tp, _ := windows.UTF16PtrFromString(tmp)
		h, _, _ := pLoadImageI.Call(0, uintptr(unsafe.Pointer(tp)), 1, 0, 0, 0x00000010|0x00000040)
		trayHIcon = h
		_ = os.Remove(tmp)
	}
	addTrayIcon()
	for _, a := range os.Args[1:] {
		if a == "-tray" || a == "-silent" {
			silentStart = true
		}
	}
	if silentStart {
		pShowWindow.Call(hwndMain, 0)
		trayBalloon("STARBOX 已在后台运行", "点击图标打开主界面")
	}
}

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
	fillU16(nid.SzTip[:], "星匣 STARBOX")
	pShellNotify.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	trayAdded = true
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
}

func fillU16(dst []uint16, s string) {
	for i, r := range s {
		if i >= len(dst)-1 {
			break
		}
		dst[i] = uint16(r)
	}
	dst[len(s)] = 0
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
	fillU16(nid.SzInfoTitle[:], title)
	fillU16(nid.SzInfo[:], text)
	pShellNotify.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func trayToggleWindow() {
	if isWindowVisible() {
		pShowWindow.Call(hwndMain, 0)
	} else {
		pShowWindow.Call(hwndMain, 9)
		pSetFG.Call(hwndMain)
	}
}

func isWindowVisible() bool {
	r, _, _ := user32.NewProc("IsWindowVisible").Call(hwndMain)
	return r != 0
}

func trayMenu() int {
	menu, _, _ := pCreatePopup.Call(0)
	if menu == 0 {
		return 0
	}
	defer pDestroyMenu.Call(menu)
	pAppendMenu.Call(menu, 0x00000000, idMenuOpen, uintptr(unsafe.Pointer(utf16("打开 主界面"))))
	pAppendMenu.Call(menu, 0x00000800, 0, 0)
	pAppendMenu.Call(menu, 0x00000000, idMenuNight, uintptr(unsafe.Pointer(utf16("主题：暗夜"+themeMark("night")))))
	pAppendMenu.Call(menu, 0x00000000, idMenuSakura, uintptr(unsafe.Pointer(utf16("主题：樱夜"+themeMark("sakura")))))
	pAppendMenu.Call(menu, 0x00000000, idMenuDay, uintptr(unsafe.Pointer(utf16("主题：白天"+themeMark("day")))))
	pAppendMenu.Call(menu, 0x00000800, 0, 0)
	pAppendMenu.Call(menu, 0x00000000, idMenuExit, uintptr(unsafe.Pointer(utf16("退出"))))
	var p trayPt
	pGetCursorP.Call(uintptr(unsafe.Pointer(&p)))
	pSetFG.Call(hwndMain)
	chosen, _, _ := pTrackMenu.Call(menu, 0x0180, uintptr(p.X), uintptr(p.Y), 0, hwndMain, 0)
	return int(chosen)
}

func themeMark(id string) string {
	if id == activeThemeID {
		return " ✓"
	}
	return ""
}

func handleTrayMessage(lParam uintptr) {
	switch lParam {
	case 0x0202:
		trayToggleWindow()
	case 0x0205:
		switch id := trayMenu(); id {
		case idMenuOpen:
			trayToggleWindow()
		case idMenuExit:
			removeTrayIcon()
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

func closeBehavior() {
	if quitToTray {
		pShowWindow.Call(hwndMain, 0)
		trayBalloon("STARBOX 收纳到托盘", "点击图标打开；右键退出")
		return
	}
	removeTrayIcon()
	pDestroyWindow.Call(hwndMain)
}

func applyTraySettings() {
	stt = settings.Load(curProfDir)
	quitToTray = stt.QuitAction == "tray"
	silentStart = stt.SilentStart
}
