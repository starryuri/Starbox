//go:build windows

// Starbox setup — a professional multi-page native installer wizard.
//
// Welcome → Options (location/shortcuts/upgrade notice) → Progress (live
// steps) → Done (launch / finish), dark themed to match the main app.
//
// Professional behaviors: existing-install detection (upgrade path with data
// preservation notice), graceful shutdown of a running STARBOX before file
// replacement, registry via the native API (no reg.exe shell-outs), last
// install location remembered, EstimatedSize + DisplayVersion in the
// uninstall registry entry, per-monitor DPI awareness.
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
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

//go:embed payload/starbox.exe
var payloadExe []byte

//go:embed payload/unins.exe
var uninsExe []byte

const (
	uninstallKey   = `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\STARBOX`
	appKey         = `HKCU\Software\STARBOX`
	appVersion     = "1.2.11"
	createNoWindow = 0x08000000
)

// palette (COLORREF 0x00BBGGRR) — same theme as the main app
const (
	colBg    = uintptr(0xf8f8f8) // classic white content
	colSide  = uintptr(0xf0f0f0) // classic dialog gray
	colCard  = uintptr(0xffffff) // white panels
	colCard2 = uintptr(0xe1e1e1) // light gray fill
	colAcc   = uintptr(0xb86800) // #0067b8 setup blue
	colFg    = uintptr(0x1a1a1a) // near-black text
	colDim   = uintptr(0x6d6d6d) // mid gray
	colErr   = uintptr(0x6050ff) // #ff5060 soft red
	colOnAcc = uintptr(0xffffff) // white on accent
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	bsOwnerDraw        = 0x0000000B
	esAutoHScroll      = 0x00000080
	dtSingle           = 0x0020
	dtVCenter          = 0x0004
	dtCenter           = 0x0001
	dtWordBreak        = 0x0010

	wmAppStep = 0x8010
	wmAppDone = 0x8011
	wmLayoutTick = 0x8012

	IDBack   = 101
	IDNext   = 102
	IDCancel = 103
	IDBrowse = 104
)

// pages
const (
	pgWelcome = iota
	pgOptions
	pgProgress
	pgDone
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	uxtheme  = windows.NewLazySystemDLL("uxtheme.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")

	pCreateWindowEx = user32.NewProc("CreateWindowExW")
	pDefWindowProc  = user32.NewProc("DefWindowProcW")
	pDestroyWindow  = user32.NewProc("DestroyWindow")
	pRegisterClass  = user32.NewProc("RegisterClassExW")
	pSendMessage    = user32.NewProc("SendMessageW")
	pSetWindowText  = user32.NewProc("SetWindowTextW")
	pShowWindow     = user32.NewProc("ShowWindow")
	pUpdateWindow   = user32.NewProc("UpdateWindow")
	pInvalidateRect = user32.NewProc("InvalidateRect")
	pMoveWindow     = user32.NewProc("MoveWindow")
	pGetClientRect  = user32.NewProc("GetClientRect")
	pBeginPaint     = user32.NewProc("BeginPaint")
	pEndPaint       = user32.NewProc("EndPaint")
	pPostQuit       = user32.NewProc("PostQuitMessage")
	pLoadIcon       = user32.NewProc("LoadIconW")
	pGetDpiSystem   = user32.NewProc("GetDpiForSystem")
	pGetWindowRect  = user32.NewProc("GetWindowRect")
	pFindWindow     = user32.NewProc("FindWindowW")

	pCreateFont      = gdi32.NewProc("CreateFontW")
	pCreateSolid     = gdi32.NewProc("CreateSolidBrush")
	pDeleteObject    = gdi32.NewProc("DeleteObject")
	pDrawText        = user32.NewProc("DrawTextW")
	pSetTextColor    = gdi32.NewProc("SetTextColor")
	pSetBkMode       = gdi32.NewProc("SetBkMode")
	pSelectObject    = gdi32.NewProc("SelectObject")
	pCreateCompatDC  = gdi32.NewProc("CreateCompatibleDC")
	pDeleteDC        = gdi32.NewProc("DeleteDC")
	pCreateDIB       = gdi32.NewProc("CreateDIBSection")
	pBitBlt          = gdi32.NewProc("BitBlt")

	pBrowse = shell32.NewProc("SHBrowseForFolderW")
	pPath   = shell32.NewProc("SHGetPathFromIDListW")
	pFree   = ole32.NewProc("CoTaskMemFree")

	wndProc = syscall.NewCallback(wndProcMain)
)

var (
	hwndMain                   uintptr
	hInst                      uintptr
	fontHead, fontBody         uintptr
	fontSmall, fontBtn         uintptr
	hBack, hNext, hCancel      uintptr
	hBrowse                    uintptr
	hDirEdit                   uintptr
	page                       = pgWelcome
	dpiScale                   = 100
	installDir                 string
	upgrading                  bool
	existingVersion            string
	optStartMenu               = true
	optDesktop                 = true
	optLaunch                  = true
	optErr                     string
	instBusy                   bool
	instStep                   int
	instErr                    string
	brushBg, brushSide, brushC uintptr
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

type rect struct{ Left, Top, Right, Bottom int32 }

type paintStruct struct {
	HDC      uintptr
	FErase   uint32
	RcPaint  rect
	FRestore uint32
	FIncUpd  uint32
	Reserved [32]byte
}

type drawItemStruct struct {
	CtlType   uint32
	CtlID     uint32
	ItemID    uint32
	ItemAction uint32
	ItemState uint32
	HwndItem  uintptr
	HDC       uintptr
	RcItem    rect
	ItemData  uintptr
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

func utf16p(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }

func scale(n int) int {
	if dpiScale <= 100 || dpiScale > 500 {
		return n
	}
	return n * dpiScale / 100
}

func makeFont(px int, bold bool) uintptr {
	w := uintptr(400)
	if bold {
		w = 700
	}
	h, _, _ := pCreateFont.Call(uintptr(px), 0, 0, 0, w, 0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(utf16p("Microsoft YaHei UI"))), 0)
	return h
}

func setFont(h, f uintptr) {
	if h != 0 {
		pSendMessage.Call(h, 0x0030 /*WM_SETFONT*/, f, 1)
	}
}

func makeChild(class, text string, style uint32, id, x, y, w, h int) uintptr {
	r, _, _ := pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16p(class))),
		uintptr(unsafe.Pointer(utf16p(text))),
		uintptr(wsChild|wsVisible|style),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		hwndMain, uintptr(id), hInst, 0)
	return r
}

func fillRect(dc uintptr, x, y, w, h int, color uintptr) {
	if w <= 0 || h <= 0 {
		return
	}
	rc := rect{int32(x), int32(y), int32(x + w), int32(y + h)}
	br, _, _ := pCreateSolid.Call(color)
	user32.NewProc("FillRect").Call(dc, uintptr(unsafe.Pointer(&rc)), br)
	pDeleteObject.Call(br)
}

func drawText(dc uintptr, x, y, w, h int, text string, font uintptr, color uintptr, flags uintptr) {
	if w <= 0 || h <= 0 {
		return
	}
	if font != 0 {
		pSelectObject.Call(dc, font)
	}
	pSetBkMode.Call(dc, 1)
	pSetTextColor.Call(dc, color)
	rc := rect{int32(x), int32(y), int32(x + w), int32(y + h)}
	tp, _ := windows.UTF16PtrFromString(text)
	pDrawText.Call(dc, uintptr(unsafe.Pointer(tp)), uintptr(0xFFFFFFFF), uintptr(unsafe.Pointer(&rc)), flags)
}

func clientSize() (int, int) {
	var rc rect
	pGetClientRect.Call(hwndMain, uintptr(unsafe.Pointer(&rc)))
	return int(rc.Right), int(rc.Bottom)
}

func defaultDir() string {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "STARBOX")
	}
	return filepath.Join(os.Getenv("USERPROFILE"), "STARBOX")
}

// ---------- registry helpers (native API — no reg.exe shell-outs) ----------

func regGet(key, name string) string {
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	return v
}

func regSetString(key, name, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(name, value)
}

func regSetDWORD(key, name string, v uint32) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetDWordValue(name, v)
}

func regDeleteValue(key, name string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	_ = k.DeleteValue(name)
}

// ---------- running-app shutdown ----------

func killRunningApp() {
	// graceful first: ask the main window to close
	cls := utf16p("STARBOXMainWnd")
	if h, _, _ := pFindWindow.Call(uintptr(unsafe.Pointer(cls)), 0); h != 0 {
		user32.NewProc("PostMessageW").Call(h, 0x0010 /*WM_CLOSE*/, 0, 0)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if h2, _, _ := pFindWindow.Call(uintptr(unsafe.Pointer(cls)), 0); h2 == 0 {
				return
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
	// force fallback
	cmd := exec.Command("taskkill", "/IM", "starbox.exe", "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	_ = cmd.Run()
}

// ---------- install steps ----------

var installSteps = []string{
	"结束运行中的 STARBOX",
	"写入程序文件",
	"创建快捷方式",
	"写入注册表信息",
	"完成配置",
}

func setStep(i int) {
	instStep = i
	if hwndMain != 0 {
		user32.NewProc("PostMessageW").Call(hwndMain, wmAppStep, uintptr(i), 0)
	}
}

func shortcut(lnk, target, workdir, desc string) error {
	ps := fmt.Sprintf(`$ws=New-Object -ComObject WScript.Shell;$s=$ws.CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.Description='%s';$s.Save()`,
		strings.ReplaceAll(lnk, "'", "''"), strings.ReplaceAll(target, "'", "''"),
		strings.ReplaceAll(workdir, "'", "''"), strings.ReplaceAll(desc, "'", "''"))
	cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return cmd.Run()
}

func startInstall() {
	instBusy = true
	instErr = ""
	go func() {
		fail := func(err error) {
			instErr = err.Error()
			instBusy = false
			user32.NewProc("PostMessageW").Call(hwndMain, wmAppStep, uintptr(len(installSteps)), 0)
		}
		setStep(0)
		killRunningApp()

		setStep(1)
		if err := os.MkdirAll(installDir, 0o755); err != nil {
			fail(err)
			return
		}
		exePath := filepath.Join(installDir, "starbox.exe")
		if err := os.WriteFile(exePath, payloadExe, 0o644); err != nil {
			fail(err)
			return
		}
		if err := os.WriteFile(filepath.Join(installDir, "unins.exe"), uninsExe, 0o644); err != nil {
			fail(err)
			return
		}
		_ = os.WriteFile(filepath.Join(installDir, "version.txt"), []byte(appVersion), 0o644)

		setStep(2)
		if optStartMenu {
			folder := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "STARBOX")
			_ = os.MkdirAll(folder, 0o755)
			if err := shortcut(filepath.Join(folder, "STARBOX.lnk"), exePath, installDir, "星匣 STARBOX · 你的次元 · 收于一匣"); err != nil {
				fail(err)
				return
			}
			if err := shortcut(filepath.Join(folder, "卸载 STARBOX.lnk"), filepath.Join(installDir, "unins.exe"), installDir, "卸载 STARBOX"); err != nil {
				fail(err)
				return
			}
		}
		if optDesktop {
			dd := filepath.Join(os.Getenv("USERPROFILE"), "Desktop", "STARBOX.lnk")
			if err := shortcut(dd, exePath, installDir, "星匣 STARBOX · 你的次元 · 收于一匣"); err != nil {
				fail(err)
				return
			}
		}

		setStep(3)
		sizeKB := uint32((len(payloadExe) + len(uninsExe)) / 1024)
		_ = regSetString(uninstallKey, "DisplayName", "STARBOX")
		_ = regSetString(uninstallKey, "DisplayVersion", appVersion)
		_ = regSetString(uninstallKey, "Publisher", "starryuri")
		_ = regSetString(uninstallKey, "InstallLocation", installDir)
		_ = regSetString(uninstallKey, "UninstallString", `"`+filepath.Join(installDir, "unins.exe")+`"`)
		_ = regSetString(uninstallKey, "DisplayIcon", filepath.Join(installDir, "starbox.exe"))
		_ = regSetDWORD(uninstallKey, "NoModify", 1)
		_ = regSetDWORD(uninstallKey, "NoRepair", 1)
		_ = regSetDWORD(uninstallKey, "EstimatedSize", sizeKB)
		_ = regSetString(appKey, "InstallLocation", installDir)
		_ = regSetString(appKey, "Version", appVersion)

		setStep(4)
		time.Sleep(250 * time.Millisecond)
		instBusy = false
		user32.NewProc("PostMessageW").Call(hwndMain, wmAppDone, 0, 0)
	}()
}

// ---------- page navigation ----------

func goPage(p int) {
	page = p
	layoutButtons()
	pInvalidateRect.Call(hwndMain, 0, 1)
	pUpdateWindow.Call(hwndMain)
}

func onNext() {
	switch page {
	case pgWelcome:
		goPage(pgOptions)
	case pgOptions:
		d := strings.TrimSpace(installDir)
		d = strings.Trim(d, `"`)
		if d == "" {
			optErr = "请填写安装位置"
			pInvalidateRect.Call(hwndMain, 0, 1)
			return
		}
		if vol := filepath.VolumeName(d) + `\`; vol == d {
			optErr = "不能选择盘符根目录"
			pInvalidateRect.Call(hwndMain, 0, 1)
			return
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			optErr = "无法创建目录：" + err.Error()
			pInvalidateRect.Call(hwndMain, 0, 1)
			return
		}
		optErr = ""
		installDir = d
		if !upgrading {
			if _, err := os.Stat(filepath.Join(d, "starbox.exe")); err == nil {
				upgrading = true // installing over an existing copy
			}
		}
		goPage(pgProgress)
		startInstall()
	case pgDone:
		if optLaunch {
			exe := filepath.Join(installDir, "starbox.exe")
			if _, err := os.Stat(exe); err == nil {
				cmd := exec.Command(exe)
				cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
				_ = cmd.Start()
			}
		}
		pDestroyWindow.Call(hwndMain)
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "未知版本"
	}
	return s
}

// custom checkbox
func drawCheck(dc uintptr, x, y, size int, text string, val *bool) {
	fillRect(dc, x, y, size, size, colCard)
	if val != nil && *val {
		inner := size * 4 / 10
		fillRect(dc, x+inner/2, y+inner/2, size-inner, size-inner, colAcc)
		drawText(dc, x, y, size, size, "✓", fontSmall, 0xffffff, dtCenter|dtVCenter)
	}
	drawText(dc, x+size+scale(12), y, 600, size, text, fontBody, colFg, 0x0024)
}

func hitCheck(x, y int) {
	if page == pgOptions {
		// clicking the path frame opens the folder browser
		fy, _, ex, ew, eh, _, _ := optionsGeometry()
		if x >= ex-scale(8) && x <= ex+ew+scale(8) && y >= fy-eh/2-scale(4) && y <= fy+eh/2+scale(4) {
			user32.NewProc("PostMessageW").Call(hwndMain, 0x0111, IDBrowse, 0)
			return
		}
	}
	if page != pgOptions && page != pgDone {
		return
	}
	size := scale(22)
	var boxes []struct {
		bx, by int
		val    *bool
	}
	if page == pgOptions {
		_, _, _, _, _, cb1Y, cb2Y := optionsGeometry()
		boxes = append(boxes,
			struct {
				bx, by int
				val    *bool
			}{scale(28), cb1Y, &optStartMenu},
			struct {
				bx, by int
				val    *bool
			}{scale(28), cb2Y, &optDesktop},
		)
	} else {
		_, h := clientSize()
		cbY := scale(104) + scale(164)
		_ = h
		boxes = append(boxes, struct {
			bx, by int
			val    *bool
		}{scale(28), cbY, &optLaunch})
	}
	for _, b := range boxes {
		if x >= b.bx && x <= b.bx+size+600 && y >= b.by && y <= b.by+size {
			*b.val = !*b.val
			pInvalidateRect.Call(hwndMain, 0, 1)
			return
		}
	}
}

// ---------- page interaction (edit / buttons / page painting) ----------

func getEdit() string {
	buf := make([]uint16, 512)
	n, _, _ := pSendMessage.Call(hDirEdit, 0x000D /*WM_GETTEXT*/, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	return windows.UTF16ToString(buf[:n])
}

func setEdit(s string) {
	sp, _ := windows.UTF16PtrFromString(s)
	pSetWindowText.Call(hDirEdit, uintptr(unsafe.Pointer(sp)))
}

func boolVis(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

func setText(h uintptr, s string) {
	sp, _ := windows.UTF16PtrFromString(s)
	pSetWindowText.Call(h, uintptr(unsafe.Pointer(sp)))
}

// optionsGeometry computes the options-page rows from the window size so
// nothing ever overlaps the footer buttons on short windows.
func optionsGeometry() (frameY, frameH, editX, editW, editH, cb1Y, cb2Y int) {
	w, _ := clientSize()
	_ = w
	frameY = scale(150)
	editX = scale(40)
	editW = w - scale(40) - scale(150)
	editH = scale(32)
	frameH = editH + scale(8)
	cb1Y = frameY + frameH + scale(28)
	cb2Y = cb1Y + scale(38)
	return
}

func onBack() {
	if page == pgOptions {
		goPage(pgWelcome)
	}
}

// layoutButtons shows/hides + positions the footer row per page. The
// install-location EDIT control only exists visually on pgOptions — it is
// created 0x0 sized and positioned here (it was never moved before, which
// is why typed/browsed paths were invisible).
func layoutButtons() {
	back := page == pgOptions
	nextLabel := "下一步"
	cancel := page == pgWelcome || page == pgOptions
	switch page {
	case pgOptions:
		nextLabel = "安装"
	case pgDone:
		nextLabel = "完成"
	case pgProgress:
		nextLabel = "安装中…"
	}
	pShowWindow.Call(hBack, boolVis(back))
	pShowWindow.Call(hCancel, boolVis(cancel && !instBusy))
	pShowWindow.Call(hBrowse, boolVis(page == pgOptions))
	pShowWindow.Call(hDirEdit, 0) // path is owner-drawn; EDIT retired
	setText(hNext, nextLabel)
	w, h := clientSize()
	bw, bh := scale(118), scale(42)
	y := h - scale(34) - bh
	pMoveWindow.Call(hNext, uintptr(w-scale(24)-bw), uintptr(y), uintptr(bw), uintptr(bh), 1)
	pMoveWindow.Call(hBack, uintptr(w-scale(24)-2*bw-scale(10)), uintptr(y), uintptr(bw), uintptr(bh), 1)
	pMoveWindow.Call(hCancel, uintptr(scale(24)), uintptr(y), uintptr(bw), uintptr(bh), 1)
	pMoveWindow.Call(hBrowse, uintptr(w-scale(24)-scale(96)), uintptr(y-scale(50)), uintptr(scale(96)), uintptr(scale(38)), 1)
	if page == pgOptions {
		fy, fh, _, _, eh, _, _ := optionsGeometry()
	w2, _ := clientSize()
	_ = w2
	pMoveWindow.Call(hBrowse, uintptr(w2-scale(24)-scale(110)), uintptr(fy-eh/2-scale(4)), uintptr(scale(110)), uintptr(fh+8), 1)
	}
}

// ---------- painting ----------

func paintPage(dc uintptr, w, h int) {
	contentY := scale(104)
	contentH := h - scale(84) - contentY
	x := scale(28)
	cw := w - 2*x

	switch page {
	case pgWelcome:
		drawText(dc, x, contentY+scale(16), cw, scale(44), "欢迎使用 星匣 STARBOX 安装向导", fontHead, colFg, dtSingle)
		drawText(dc, x, contentY+scale(78), cw, contentH-scale(90),
			"本向导将把 STARBOX 安装到你的电脑。\n\n"+
				"· 番剧、收藏、订阅全部数据保存在本地\n"+
				"· 随时可在「设置 → 应用」中卸载，并可选择保留数据\n"+
				"· 安装过程不会写入任何注册表垃圾项\n\n"+
				"点击「下一步」继续。", fontBody, colDim, 0)
	case pgOptions:
		drawText(dc, x, contentY+scale(8), cw, scale(30), "选择安装位置", fontBody, colFg, dtSingle)
		fy, fh, ex, ew, eh, cb1Y, cb2Y := optionsGeometry()
		// path field: border + current path (owner-drawn; click opens browse)
		fillRect(dc, ex-scale(8), fy-eh/2-scale(4), ew+scale(16), fh+scale(8), colCard2)
		fillRect(dc, ex-scale(6), fy-eh/2-scale(2), ew+scale(12), fh+scale(4), colCard)
		drawText(dc, ex, fy-eh/2, ew, eh, installDir, fontBody, colFg, 0x0024|0x00000800) // single|vcenter|path-ellipsis
		if upgrading {
			cbMid := (cb1Y + cb2Y + scale(22)) / 2
			noteY := cbMid + scale(16)
			fillRect(dc, x, noteY, cw, scale(58), colCard)
			drawText(dc, x+scale(10), noteY+scale(8), cw-scale(20), scale(44),
				"检测到已安装的 STARBOX（版本 "+orDash(existingVersion)+"）。将原地升级，你的数据会完整保留。",
				fontSmall, colDim, 0x0025)
		}
		drawCheck(dc, x, cb1Y, scale(22), "创建开始菜单快捷方式", &optStartMenu)
		drawCheck(dc, x, cb2Y, scale(22), "创建桌面快捷方式", &optDesktop)
		if optErr != "" {
			drawText(dc, x, cb1Y-scale(34), cw, scale(26), optErr, fontSmall, colErr, dtSingle)
		} else if !upgrading {
			drawText(dc, ex, fy+eh/2+scale(14), ew, scale(24), "默认安装到 %LOCALAPPDATA%\\STARBOX，可自行修改", fontSmall, colDim, dtSingle)
		}
	case pgProgress:
		drawText(dc, x, contentY+scale(8), cw, scale(34), "正在安装 STARBOX…", fontBody, colFg, dtSingle)
		sy := contentY + scale(56)
		for i, s := range installSteps {
			glyph, color := "○", colDim
			if i < instStep || instErr != "" && i < instStep {
				glyph, color = "✓", colAcc
			} else if i == instStep && instErr == "" {
				glyph, color = "●", colAcc
			}
			drawText(dc, x, sy+i*scale(34), scale(28), scale(28), glyph, fontBody, color, dtCenter)
			txtCol := uintptr(colFg)
			if i > instStep {
				txtCol = colDim
			}
			drawText(dc, x+scale(36), sy+i*scale(34), cw-scale(36), scale(28), s, fontSmall, txtCol, 0x0025)
		}
		barY := contentY + contentH - scale(56)
		fillRect(dc, x, barY, cw, scale(10), colCard)
		done := instStep * 100 / len(installSteps)
		if instErr != "" {
			drawText(dc, x, barY+scale(18), cw, scale(26), "安装失败："+instErr, fontSmall, colErr, 0)
			drawText(dc, x, contentY+scale(8), cw, scale(34), "安装遇到问题", fontBody, colFg, dtSingle)
		} else if done > 0 {
			fillRect(dc, x, barY, cw*done/100, scale(10), colAcc)
		}
	case pgDone:
		drawText(dc, x, contentY+scale(16), cw, scale(44), "安装完成！", fontHead, colAcc, dtSingle)
		drawText(dc, x, contentY+scale(80), cw, scale(30), "STARBOX "+appVersion+" 已安装到：", fontBody, colFg, dtSingle)
		drawText(dc, x, contentY+scale(112), cw, scale(30), installDir, fontSmall, colDim, 0x00000800|dtSingle)
		cbY := contentY + scale(164)
		drawCheck(dc, x, cbY, scale(22), "安装完成后启动 STARBOX", &optLaunch)
		drawText(dc, x, cbY+scale(46), cw, scale(56),
			"随时可以从开始菜单或「设置 → 应用」中卸载 STARBOX。\n卸载时可以选择保留你的全部数据。", fontSmall, colDim, 0)
	}
}


// ---------- banner / footer ----------

func paintChrome(dc uintptr, w, h int) {
	// banner
	bh := scale(92)
	fillRect(dc, 0, 0, w, bh, colSide)
	fillRect(dc, 0, bh-scale(3), w, scale(3), colAcc)
	drawText(dc, scale(28), scale(16), w-scale(56), scale(40), "星匣 STARBOX", fontHead, colFg, dtSingle)
	sub := "安装向导 · 版本 " + appVersion
	if page == pgProgress && instBusy {
		sub = "正在安装，请稍候…"
	}
	drawText(dc, scale(28), scale(56), w-scale(56), scale(26), sub, fontSmall, colDim, dtSingle)
	// footer separator
	fy := h - scale(84)
	fillRect(dc, 0, fy, w, scale(1), colCard2)
	// content bg
	fillRect(dc, 0, bh, w, fy-bh, colBg)
}

// ---------- wndProc ----------

func wndProcMain(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case 0x0111: // WM_COMMAND
		switch uintptr(0xFFFF) & wParam {
		case IDNext:
			onNext()
			return 0
		case IDBack:
			onBack()
			return 0
		case IDCancel:
			if !instBusy {
				pDestroyWindow.Call(hwnd)
			}
			return 0
		case IDBrowse:
			if p := browseFolder(); p != "" {
				installDir = p
				upgrading = false
				if _, err := os.Stat(filepath.Join(p, "starbox.exe")); err == nil {
					upgrading = true
				}
				pInvalidateRect.Call(hwndMain, 0, 1)
			}
			return 0
		}
	case 0x0202: // WM_LBUTTONUP
		x := int(int16(uint16(lParam & 0xFFFF)))
		y := int(int16(uint16((lParam >> 16) & 0xFFFF)))
		hitCheck(x, y)
		return 0
	case 0x0100: // WM_KEYDOWN
		switch wParam {
		case 0x0D: // Enter
			if !instBusy {
				onNext()
			}
			return 0
		case 0x1B: // Esc
			if !instBusy {
				pDestroyWindow.Call(hwnd)
			}
			return 0
		}
	case wmAppStep, wmAppDone:
		if msg == wmAppDone {
			page = pgDone
			layoutButtons()
		}
		pInvalidateRect.Call(hwnd, 0, 1)
		return 0
	case 0x000F: // WM_PAINT
		var ps paintStruct
		dc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if dc != 0 {
			w, h := clientSize()
			mem, _, _ := pCreateCompatDC.Call(dc)
			if mem != 0 {
				var bi struct {
					Size       uint32
					Width      int32
					Height     int32
					Planes     uint16
					BitCount   uint16
					Compression uint32
					SizeImage  uint32
					XPpm       int32
					YPpm       int32
					ClrUsed    uint32
					ClrImp     uint32
				}
				bi.Size = 40
				bi.Width = int32(w)
				bi.Height = int32(-h)
				bi.Planes = 1
				bi.BitCount = 32
				var bits *byte
				bmp, _, _ := pCreateDIB.Call(mem, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
				if bmp != 0 && bits != nil {
					old, _, _ := pSelectObject.Call(mem, bmp)
					fillRect(mem, 0, 0, w, h, colBg)
					paintChrome(mem, w, h)
					paintPage(mem, w, h)
					pBitBlt.Call(dc, 0, 0, uintptr(w), uintptr(h), mem, 0, 0, 0x00CC0020)
					pSelectObject.Call(mem, old)
					pDeleteObject.Call(bmp)
				}
				pDeleteDC.Call(mem)
			}
			pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		}
		return 0
	case 0x002B: // WM_DRAWITEM — owner-draw buttons (缺失会导致按钮变成白色空块)
		return drawItem(wParam, lParam)
	case 0x0133: // WM_CTLCOLOREDIT — dark edit
		pSetTextColor.Call(wParam, colFg)
		pSetBkMode.Call(wParam, 0)
		pSetBkMode2(wParam)
		return brushC
	case 0x0138: // WM_CTLCOLORBTN
		return brushBg
	case 0x0005: // WM_SIZE
		layoutButtons()
		r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
		return r
	case 0x0113: // WM_TIMER
		if wParam == 2 && page == pgOptions {
			layoutButtons() // cheap; keeps edit/browse sized despite DPI/race quirks
		}
		return 0
	case 0x0010: // WM_CLOSE
		if !instBusy {
			pDestroyWindow.Call(hwnd)
		}
		return 0
	case 0x0002: // WM_DESTROY
		pPostQuit.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

func pSetBkMode2(dc uintptr) {
	pSetBkMode.Call(dc, 0)
}

// ---------- browse ----------

func browseFolder() string {
	title := utf16p("选择安装位置")
	bi := struct {
		HwndOwner uintptr
		PidlRoot  uintptr
		Disp      uintptr
		Title     *uint16
		Flags     uint32
		Callback  uintptr
		Param     uintptr
		Image     int32
	}{hwndMain, 0, 0, title, 0x1 | 0x40, 0, 0, 0}
	r, _, _ := pBrowse.Call(uintptr(unsafe.Pointer(&bi)))
	if r == 0 {
		return ""
	}
	defer pFree.Call(r)
	var buf [260]uint16
	if ok, _, _ := pPath.Call(r, uintptr(unsafe.Pointer(&buf[0]))); ok == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:])
}

// ---------- main ----------

func main() {
	runtime.LockOSThread()
	user32.NewProc("SetProcessDPIAware").Call()
	user32.NewProc("SetThreadDpiAwarenessContext").Call(^uintptr(3))
	if r, _, _ := pGetDpiSystem.Call(); r != 0 {
		dpiScale = int(r) * 100 / 96
	}

	mod, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	hInst = mod

	brushBg, _, _ = pCreateSolid.Call(colBg)
	brushSide, _, _ = pCreateSolid.Call(colSide)
	brushC, _, _ = pCreateSolid.Call(colCard)

	// remembered location → upgrade detection
	installDir = regGet(appKey, "InstallLocation")
	if installDir == "" {
		installDir = regGet(uninstallKey, "InstallLocation")
	}
	if installDir != "" {
		upgrading = true
		existingVersion = regGet(uninstallKey, "DisplayVersion")
	} else {
		installDir = defaultDir()
	}

	clsName := utf16p("STARBOXSetupWnd")
	wc := wndClassEx{
		Size:          uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:       wndProc,
		HInstance:     hInst,
		HIcon:         curIcon(hInst),
		HCursor:       0,
		HbrBackground: brushBg,
		ClassName:     clsName,
	}
	pRegisterClass.Call(uintptr(unsafe.Pointer(&wc)))

	ww, wh := scale(660), scale(460)
	hwndMain, _, _ = pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(utf16p("星匣 STARBOX 安装向导"))),
		uintptr(wsOverlappedWindow&^0x00030000&^0x00040000), // no maximize/resize (thick frame kept for moving)
		0x80000000, 0x80000000, uintptr(ww), uintptr(wh),
		0, 0, hInst, 0)
	user32.NewProc("SetWindowLongPtrW").Call(hwndMain, ^uintptr(20), uintptr(uint32(0x00000480)))
	user32.NewProc("SetTimer").Call(hwndMain, 2, 250, 0) // layout watchdog (edit/browse sizing) // GWL_STYLE: dialog-frame

	fontHead = makeFont(scale(24), true)
	fontBody = makeFont(scale(16), false)
	fontSmall = makeFont(scale(13), false)
	fontBtn = makeFont(scale(15), false)

	hBack = makeChild("BUTTON", "上一步", bsOwnerDraw, IDBack, 0, 0, 0, 0)
	hNext = makeChild("BUTTON", "下一步", bsOwnerDraw, IDNext, 0, 0, 0, 0)
	hCancel = makeChild("BUTTON", "取消", bsOwnerDraw, IDCancel, 0, 0, 0, 0)
	hBrowse = makeChild("BUTTON", "浏览…", bsOwnerDraw, IDBrowse, 0, 0, 0, 0)
	hDirEdit = makeChild("EDIT", installDir, esAutoHScroll, 110, 0, 0, 0, 0)
	uxtheme.NewProc("SetWindowTheme").Call(hDirEdit, uintptr(unsafe.Pointer(utf16p(""))), uintptr(unsafe.Pointer(utf16p(""))))
	setFont(hDirEdit, fontBody)
	setFont(hBack, fontBtn)
	setFont(hNext, fontBtn)
	setFont(hCancel, fontBtn)
	setFont(hBrowse, fontBtn)

	pShowWindow.Call(hwndMain, 5)
	pUpdateWindow.Call(hwndMain)
	layoutButtons()

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

func curIcon(hInst uintptr) uintptr {
	r, _, _ := pLoadIcon.Call(hInst, 1)
	if r == 0 {
		r, _, _ = pLoadIcon.Call(0, 32512)
	}
	return r
}

func drawBtn(di *drawItemStruct, fill, tc uintptr) {
	br, _, _ := pCreateSolid.Call(fill)
	user32.NewProc("FillRect").Call(di.HDC, uintptr(unsafe.Pointer(&di.RcItem)), br)
	pDeleteObject.Call(br)
	pSetBkMode.Call(di.HDC, 1)
	pSetTextColor.Call(di.HDC, tc)
	tp, _ := windows.UTF16PtrFromString(getBtnText(di.HwndItem))
	rc := di.RcItem
	pDrawText.Call(di.HDC, uintptr(unsafe.Pointer(tp)), uintptr(0xFFFFFFFF), uintptr(unsafe.Pointer(&rc)), 0x0025)
}

func getBtnText(h uintptr) string {
	buf := make([]uint16, 128)
	n, _, _ := pSendMessage.Call(h, 0x000D, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	return windows.UTF16ToString(buf[:n])
}

func drawItem(wParam, lParam uintptr) uintptr {
	di := (*drawItemStruct)(unsafe.Pointer(lParam))
	id := uintptr(di.CtlID)
	fill := uintptr(colCard)
	tc := uintptr(colFg)
	switch id {
	case IDNext:
		if di.ItemState&0x0001 != 0 { // selected
			fill = colCard2
		}
		fill, tc = colAcc, colOnAcc
		if di.ItemState&0x0001 != 0 {
			fill = 0xccb81e
		}
	case IDBack, IDCancel, IDBrowse:
		if di.ItemState&0x0001 != 0 {
			fill = colCard2
		}
	}
	drawBtn(di, fill, tc)
	if di.ItemState&0x0010 != 0 { // focus underline
		br, _, _ := pCreateSolid.Call(colAcc)
		rc := di.RcItem
		rc.Bottom = rc.Top + 3
		user32.NewProc("FillRect").Call(di.HDC, uintptr(unsafe.Pointer(&rc)), br)
		pDeleteObject.Call(br)
	}
	return 1
}
