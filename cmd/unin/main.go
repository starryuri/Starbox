//go:build windows

// Starbox uninstaller — a standalone native Win32 GUI (unins.exe). It runs the
// uninstall flow (confirm -> remove -> done) and is deployed by setup.exe, then
// invoked from Control Panel / the Start-Menu "卸载 STARBOX" shortcut. It is a
// distinct binary from the installer.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	uninstallKey   = `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\STARBOX`
	createNoWindow = 0x08000000
)

// Win32 constants.
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
	IDUninstall = 101
	IDCancel    = 102
	IDDone      = 103
	IDMsg       = 104
	IDStatus    = 105
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
)

var (
	hwndMain                            uintptr
	hwndFont                            uintptr
	hUninstallBtn, hCancelBtn, hDoneBtn uintptr
	hMsg, hStatus                       uintptr
	wndProc                             = syscall.NewCallback(wndProcMain)
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

func runNoWindow(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return cmd.Run()
}

func reg(args ...string) error { return runNoWindow("reg", args...) }

func shortLinkPaths() (sm, desktop string) {
	sm = filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "STARBOX.lnk")
	desktop = filepath.Join(os.Getenv("USERPROFILE"), "Desktop", "STARBOX.lnk")
	return
}

// uninstall removes shortcuts, registry, and files (self scheduled for reboot).
func uninstall(dir string) {
	if dir == "" {
		if self, err := os.Executable(); err == nil {
			dir = filepath.Dir(self)
		}
	}
	sm, dd := shortLinkPaths()
	_ = os.Remove(sm)
	_ = os.Remove(filepath.Join(filepath.Dir(sm), "卸载 STARBOX.lnk"))
	_ = os.Remove(dd)
	_ = reg("delete", uninstallKey, "/f")

	self, _ := os.Executable()
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if e.IsDir() {
				_ = os.RemoveAll(p)
				continue
			}
			if self == p {
				continue
			}
			_ = os.Remove(p)
		}
	}
	if self != "" {
		var k32 = syscall.NewLazyDLL("kernel32.dll")
		moveFileEx := k32.NewProc("MoveFileExW")
		if p, err := syscall.UTF16PtrFromString(self); err == nil {
			moveFileEx.Call(uintptr(unsafe.Pointer(p)), 0, 0x4) // MOVEFILE_DELAY_UNTIL_REBOOT
		}
	}
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

func setStatus(text string) {
	setText(hStatus, text)
	pInvalidateRect.Call(hwndMain, 0, 1)
}

func doUninstall() {
	setStatus("正在卸载…")
	pShowWindow.Call(hUninstallBtn, 0)
	pShowWindow.Call(hCancelBtn, 0)
	uninstall("")
	// done state
	setText(hMsg, "已卸载。文件将在重启后彻底清理。")
	pShowWindow.Call(hDoneBtn, 1)
	setStatus("")
}

func curIcon(hInst uintptr) uintptr {
	r, _, _ := pLoadIcon.Call(hInst, 1)
	if r == 0 {
		r, _, _ = pLoadIcon.Call(0, 32512)
	}
	return r
}

func wndProcMain(hwnd uintptr, msg uint32, wParam uintptr, lParam uintptr) uintptr {
	switch msg {
	case 0x0100: // WM_COMMAND
		id := uintptr(0xFFFF) & wParam
		switch id {
		case IDUninstall:
			doUninstall()
			return 0
		case IDDone:
			pDestroyWindow.Call(hwndMain)
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
	_, _, _ = user32.NewProc("SetProcessDPIAware").Call()

	mod, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	hInst := mod

	clsName := utf16("STARBOXUninWnd")
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
		uintptr(unsafe.Pointer(utf16("卸载 STARBOX"))),
		uintptr(wsOverlappedWindow),
		0x80000000, 0x80000000, 460, 260,
		0, 0, hInst, 0)

	hwndFont = createWin32Font(16, false)
	hMsg = createChild("STATIC", "确定要卸载 STARBOX 吗？", ssLeft, IDMsg, 24, 32, 412, 44)
	hStatus = createChild("STATIC", "", ssLeft, IDStatus, 24, 90, 412, 40)
	hUninstallBtn = createChild("BUTTON", "卸载", bsPushButton, IDUninstall, 140, 150, 100, 34)
	hCancelBtn = createChild("BUTTON", "取消", bsPushButton, IDCancel, 260, 150, 90, 34)
	hDoneBtn = createChild("BUTTON", "完成", bsPushButton, IDDone, 330, 150, 90, 34)
	pShowWindow.Call(hDoneBtn, 0)

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
