//go:build windows

// Starbox setup — a native Windows GUI installer (no WebView2, no console).
//
//	setup.exe -> native Win32 wizard: pick install dir / start menu / desktop,
//	            install payload, then a Done screen with "run now" and "finish".
//
// The uninstaller is a separate, standalone binary (unins.exe, cmd/unin) and is
// deployed by this installer, then invoked from Control Panel / Start Menu.
package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

//go:embed payload/starbox.exe
var payloadExe []byte

//go:embed payload/unins.exe
var uninsExe []byte

const (
	runKey         = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	uninstallKey   = `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\STARBOX`
	createNoWindow = 0x08000000
)

// ---- Win32 constants ----
const (
	wsOverlappedWindow = 0x00CF0000
	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	wsTabStop          = 0x00010000
	wsGroup            = 0x00020000
	ssLeft             = 0x00000000
	bsPushButton       = 0x00000000
	bsCheckBox         = 0x00000002
	bsAutoCheckBox     = 0x00000003
	esAutoHScroll      = 0x00000080
	esLeft             = 0x00000000
	esPassword         = 0x0020
	colorWindow        = 5
)

// Control IDs.
const (
	IDDirEdit   = 100
	IDBrowse    = 101
	IDSMCheck   = 102
	IDDesktopCh = 103
	IDInstall   = 104
	IDRun       = 105
	IDDone      = 106
	IDStatus    = 107
	IDMsgStatic = 108
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
	pSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	pGetWindowLongPtr = user32.NewProc("GetWindowLongPtrW")
	pShowWindow       = user32.NewProc("ShowWindow")
	pUpdateWindow     = user32.NewProc("UpdateWindow")
	pInvalidateRect   = user32.NewProc("InvalidateRect")
	pDeleteObject     = gdi32.NewProc("DeleteObject")
	shell32           = windows.NewLazyDLL("shell32.dll")
	pBrowse           = shell32.NewProc("SHBrowseForFolderW")
	pPath             = shell32.NewProc("SHGetPathFromIDListW")
	ole32             = windows.NewLazyDLL("ole32.dll")
	pCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")
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

var (
	hwndMain  uintptr
	hwndFont  uintptr
	installed bool
	// control handles
	hDirEdit, hBrowseBtn, hSMChk, hDeskChk, hInstallBtn, hRunBtn, hDoneBtn, hStatus, hMsgStatic uintptr
)

var wndProc = syscall.NewCallback(wndProcMain)

func utf16(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

func setText(h uintptr, s string) {
	sp, _ := windows.UTF16PtrFromString(s)
	pSetWindowText.Call(h, uintptr(unsafe.Pointer(sp)))
}

func getText(h uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := pSendMessage.Call(h, 0x000D /*WM_GETTEXT*/, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	return windows.UTF16ToString(buf[:n])
}

func checkState(h uintptr) bool {
	r, _, _ := pSendMessage.Call(h, 0x00F0 /*BM_GETCHECK*/, 0, 0) // BST_CHECKED=1
	return r == 1
}

// runNoWindow runs a command without flashing a console window.
func runNoWindow(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return cmd.Run()
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func defaultDir() string {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "STARBOX")
	}
	return filepath.Join(os.Getenv("USERPROFILE"), "STARBOX")
}

func shortLinkPaths() (sm, desktop string) {
	sm = filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "STARBOX.lnk")
	desktop = filepath.Join(os.Getenv("USERPROFILE"), "Desktop", "STARBOX.lnk")
	return
}

func shortcut(lnk, target, args, workdir, desc string) {
	ps := fmt.Sprintf(`$ws=New-Object -ComObject WScript.Shell;$s=$ws.CreateShortcut('%s');$s.TargetPath='%s';$s.Arguments='%s';$s.WorkingDirectory='%s';$s.Description='%s';$s.Save()`,
		strings.ReplaceAll(lnk, "'", "''"), strings.ReplaceAll(target, "'", "''"),
		strings.ReplaceAll(args, "'", "''"), strings.ReplaceAll(workdir, "'", "''"), strings.ReplaceAll(desc, "'", "''"))
	_ = runNoWindow("powershell", "-NoProfile", "-Command", ps)
}

func reg(args ...string) error {
	return runNoWindow("reg", args...)
}

// install copies the payload to dir and wires shortcuts + uninstall registry.
func install(dir string, startMenu, desktop bool) error {
	if dir == "" {
		dir = defaultDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	exePath := filepath.Join(dir, "starbox.exe")
	if err := writeFile(exePath, payloadExe); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "unins.exe"), uninsExe); err != nil {
		return err
	}
	if startMenu {
		sm, _ := shortLinkPaths()
		shortcut(sm, exePath, "", dir, "STARBOX · 你的次元 · 收于一匣")
		shortcut(filepath.Join(filepath.Dir(sm), "卸载 STARBOX.lnk"), filepath.Join(dir, "unins.exe"), "", dir, "卸载 STARBOX")
	}
	if desktop {
		_, dd := shortLinkPaths()
		shortcut(dd, exePath, "", dir, "STARBOX · 你的次元 · 收于一匣")
	}
	unins := filepath.Join(dir, "unins.exe")
	kv := func(k, ty, v string) {
		_ = reg("add", uninstallKey, "/v", k, "/t", ty, "/d", v, "/f")
	}
	kv("DisplayName", "REG_SZ", "STARBOX")
	kv("DisplayVersion", "REG_SZ", "1.0.0")
	kv("Publisher", "REG_SZ", "starryuri")
	kv("InstallLocation", "REG_SZ", dir)
	kv("UninstallString", "REG_SZ", fmt.Sprintf(`"%s"`, unins))
	kv("DisplayIcon", "REG_SZ", exePath)
	kv("NoModify", "REG_DWORD", "1")
	kv("NoRepair", "REG_DWORD", "1")
	return nil
}

func pickFolder() string {
	title, _ := windows.UTF16PtrFromString("选择安装位置")
	bi := struct {
		HwndOwner      uintptr
		PidlRoot       uintptr
		PszDisplayName uintptr
		LpszTitle      *uint16
		UlFlags        uint32
		Lpfn           uintptr
		LParam         uintptr
		IImage         int32
	}{HwndOwner: hwndMain, PidlRoot: 0, PszDisplayName: 0, LpszTitle: title, UlFlags: 0x1 | 0x40, Lpfn: 0, LParam: 0, IImage: 0}
	r, _, _ := pBrowse.Call(uintptr(unsafe.Pointer(&bi)))
	if r == 0 {
		return ""
	}
	defer pCoTaskMemFree.Call(r)
	var buf [windows.MAX_PATH]uint16
	if ok, _, _ := pPath.Call(r, uintptr(unsafe.Pointer(&buf[0]))); ok == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:])
}

func createWin32Font(size int, bold bool) uintptr {
	const (
		fwNormal       = 400
		fwBold         = 700
		defaultCharset = 1
		outDefault     = 0
		clipDefault    = 0
		antialias      = 5
	)
	w := uintptr(fwNormal)
	if bold {
		w = fwBold
	}
	// MS YaHei for CJK; falls back to system default if unavailable.
	h, _, _ := pCreateFont.Call(uintptr(size), 0, 0, 0, w, 0, 0, 0, defaultCharset, outDefault, clipDefault, antialias, 0, uintptr(unsafe.Pointer(utf16("Microsoft YaHei"))), 0)
	return h
}

func createChild(class string, text string, style uint32, id int, x, y, w, h int) uintptr {
	r, _, _ := pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16(class))),
		uintptr(unsafe.Pointer(utf16(text))),
		uintptr(wsChild|wsVisible|style),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		hwndMain,
		uintptr(id),
		0,
		0)
	if id != 0 {
		// send WM_SETFONT so controls use our CJK-capable font
		pSendMessage.Call(r, 0x0030 /*WM_SETFONT*/, hwndFont, 1)
	}
	return r
}

func setStatus(text string) {
	setText(hStatus, text)
	pInvalidateRect.Call(hwndMain, 0, 1)
}

func enterDoneState() {
	// Hide install controls, show done controls.
	pShowWindow.Call(hDirEdit, 0)
	pShowWindow.Call(hBrowseBtn, 0)
	pShowWindow.Call(hSMChk, 0)
	pShowWindow.Call(hDeskChk, 0)
	pShowWindow.Call(hInstallBtn, 0)
	pShowWindow.Call(hMsgStatic, 1)
	pShowWindow.Call(hRunBtn, 1)
	pShowWindow.Call(hDoneBtn, 1)
	setText(hMsgStatic, "安装完成！")
	setStatus(filepath.Dir(getText(hDirEdit)))
	pUpdateWindow.Call(hwndMain)
}

func doInstall() {
	dir := strings.TrimSpace(getText(hDirEdit))
	if dir == "" {
		dir = defaultDir()
		setText(hDirEdit, dir)
	}
	setStatus("正在安装…")
	pShowWindow.Call(hInstallBtn, 0)
	go func() {
		err := install(dir, checkState(hSMChk), checkState(hDeskChk))
		if err != nil {
			pShowWindow.Call(hInstallBtn, 1)
			setStatus("安装失败：" + err.Error())
			return
		}
		setStatus("安装完成")
		enterDoneState()
	}()
}

func wndProcMain(hwnd uintptr, msg uint32, wParam uintptr, lParam uintptr) uintptr {
	switch msg {
	case 0x0111: // WM_COMMAND
		id := uintptr(0xFFFF) & wParam
		switch id {
		case IDBrowse:
			if p := pickFolder(); p != "" {
				setText(hDirEdit, p)
			}
			return 0
		case IDInstall:
			doInstall()
			return 0
		case IDRun:
			dir := strings.TrimSpace(getText(hDirEdit))
			if dir == "" {
				dir = defaultDir()
			}
			exe := filepath.Join(dir, "starbox.exe")
			if _, err := os.Stat(exe); err == nil {
				_ = exec.Command(exe, "-desktop").Start()
			}
			pDestroyWindow.Call(hwndMain)
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
		postQuitMessage()
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

var (
	pPostQuitMessage = user32.NewProc("PostQuitMessage")
)

func postQuitMessage() {
	pPostQuitMessage.Call(0)
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

func curIcon(hInst uintptr) uintptr {
	r, _, _ := user32.NewProc("LoadIconW").Call(hInst, 1)
	if r == 0 {
		r, _, _ = user32.NewProc("LoadIconW").Call(0, 32512)
	}
	return r
}

func main() {
	runtime.LockOSThread()
	_, _, _ = user32.NewProc("SetProcessDPIAware").Call()

	// Get instance/module handle.
	mod, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	hInst := mod

	clsName := utf16("STARBOXSetupWnd")
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
		uintptr(unsafe.Pointer(utf16("星匣 STARBOX 安装器"))),
		uintptr(wsOverlappedWindow),
		0x80000000, 0x80000000, 720, 600, // CW_USEDEFAULT
		0, 0, hInst, 0)

	hwndFont = createWin32Font(21, false)
	// Title static
	hMsgStatic = createChild("STATIC", "安装 STARBOX", ssLeft, IDMsgStatic, 24, 34, 672, 30)
	// Install dir row
	createChild("STATIC", "安装位置:", ssLeft, 0, 24, 96, 100, 26)
	hDirEdit = createChild("EDIT", defaultDir(), wsTabStop, IDDirEdit, 124, 92, 380, 34)
	hBrowseBtn = createChild("BUTTON", "浏览…", bsPushButton, IDBrowse, 514, 90, 110, 36)
	// Options
	hSMChk = createChild("BUTTON", "创建开始菜单快捷方式", bsAutoCheckBox, IDSMCheck, 24, 152, 420, 30)
	pSendMessage.Call(hSMChk, 0x00F1 /*BM_SETCHECK*/, 1, 0)
	hDeskChk = createChild("BUTTON", "创建桌面快捷方式", bsAutoCheckBox, IDDesktopCh, 24, 188, 420, 30)
	pSendMessage.Call(hDeskChk, 0x00F1, 1, 0)
	// Install button
	hInstallBtn = createChild("BUTTON", "安装", bsPushButton, IDInstall, 24, 252, 150, 44)
	// Status
	hStatus = createChild("STATIC", "", ssLeft, IDStatus, 24, 324, 672, 48)
	// Done controls (hidden initially)
	hRunBtn = createChild("BUTTON", "立即运行 STARBOX", bsPushButton, IDRun, 24, 252, 200, 44)
	hDoneBtn = createChild("BUTTON", "完成", bsPushButton, IDDone, 240, 252, 120, 44)
	pShowWindow.Call(hRunBtn, 0)
	pShowWindow.Call(hDoneBtn, 0)

	pShowWindow.Call(hwndMain, 5) // SW_SHOW
	pUpdateWindow.Call(hwndMain)

	// Message loop
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
