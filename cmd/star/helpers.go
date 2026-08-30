//go:build windows

package main

import (
	"fmt"
	"os"
	"strconv"
	"path/filepath"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"butler/internal/settings"


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

// htmlEscape escapes a string for HTML output.
func htmlEscape(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		case '"':
			sb.WriteString("&#34;")
		case '\'':
			sb.WriteString("&#39;")
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// fmtF formats a float with given decimals.
func fmtF(v float64, dec int) string {
	return strconv.FormatFloat(v, 'f', dec, 64)
}

// fmtI formats an int.
func fmtI(v int64) string {
	return fmt.Sprintf("%d", v)
}

// windowsNewLazy wraps windows.NewLazySystemDLL.
func windowsNewLazy(name string) *windows.LazyDLL { return windows.NewLazySystemDLL(name) }

// windowsUTF16Ptr wraps windows.UTF16PtrFromString.
func windowsUTF16Ptr(s string) (*uint16, error) { return windows.UTF16PtrFromString(s) }

// unsafePointerOf converts a *uint16 to uintptr-able unsafe.Pointer.
func unsafePointerOf(p *uint16) unsafe.Pointer { return unsafe.Pointer(p) }

// shellExecuteOpen opens a path (file or folder) with the shell.
func shellExecuteOpen(path string) {
	op, _ := windowsUTF16Ptr("open")
	fp, err := windowsUTF16Ptr(path)
	if err != nil {
		return
	}
	shell32 := windowsNewLazy("shell32.dll")
	ShellExecuteW := shell32.NewProc("ShellExecuteW")
	ShellExecuteW.Call(0, uintptr(unsafePointerOf(op)), uintptr(unsafePointerOf(fp)), 0, 0, 5)
}

// osExecutable returns the running executable path.
func osExecutable() (string, error) { return os.Executable() }

// settingsLoad reads app settings from the current profile.
func settingsLoad() settings.Settings { return settings.Load(curProfDir) }

// settingsSave writes app settings to the current profile.
func settingsSave(s settings.Settings) error { return settings.Save(curProfDir, s) }

// settingsSetAutoStart registers/unregisters the Run entry.
func settingsSetAutoStart(on bool, exe string) error { return settings.SetAutoStart(on, exe) }

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



// utf16ptr is a small alias used by dialog helpers.
func utf16ptr(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }

// openFileNameW mirrors OPENFILENAMEW (64-bit layout).
type openFileNameW struct {
	LStructSize       uint32
	HwndOwner         uintptr
	HInstance         uintptr
	LpstrFilter       uintptr
	LpstrCustomFilter uintptr
	NMaxCustFilter    uint32
	NFilterIndex      uint32
	LpstrFile         uintptr
	NMaxFile          uint32
	LpstrFileTitle    uintptr
	NMaxFileTitle     uint32
	LpstrInitialDir   uintptr
	LpstrTitle        uintptr
	Flags             uint32
	NFileOffset       uint16
	NFileExtension    uint16
	LpstrDefExt       uintptr
	LCustData         uintptr
	LpfnHook          uintptr
	LpTemplateName    uintptr
	// LPEDITMENU for win2000+ omitted (we do not use OFN_ENABLEHOOK templates)
	_Pad  [8]byte
	_Pad2 [8]byte
}

// comdlgOpenFile opens the classic file-open dialog. Returns the chosen
// paths (multi-select splits on NUL). Empty slice = cancelled.
func comdlgOpenFile(title, filter string, extraFlags uint32) []string {
	comdlg32 := windows.NewLazySystemDLL("comdlg32.dll")
	GetOpenFileNameW := comdlg32.NewProc("GetOpenFileNameW")
	buf := make([]uint16, 32768)
	fp := utf16ptr(filter)
	tp := utf16ptr(title)
	ofn := openFileNameW{
		LStructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		HwndOwner:   hwndMain,
		LpstrFilter: uintptr(unsafe.Pointer(fp)),
		LpstrFile:   uintptr(unsafe.Pointer(&buf[0])),
		NMaxFile:    uint32(len(buf)),
		LpstrTitle:  uintptr(unsafe.Pointer(tp)),
		Flags:       0x00000800 | extraFlags, // OFN_FILEMUSTEXIST
	}
	r, _, _ := GetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return nil
	}
	// buf holds "dir\0file1\0file2...\0\0" (multi) or "full\path\0\0"
	var parts []string
	start := 0
	for i := 0; i < len(buf); i++ {
		if buf[i] == 0 {
			if i == start {
				break
			}
			parts = append(parts, windows.UTF16ToString(buf[start:i]))
			start = i + 1
			if start < len(buf) && buf[start] == 0 {
				break
			}
		}
	}
	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 {
		return []string{parts[0]}
	}
	dir := parts[0]
	out := make([]string, 0, len(parts)-1)
	for _, f := range parts[1:] {
		out = append(out, filepath.Join(dir, f))
	}
	return out
}
