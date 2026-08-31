//go:build windows

// Starbox uninstaller — professional native wizard matching the installer.
//
// Confirm (with keep-data option) → Progress (live steps) → Done.
// Gracefully closes a running STARBOX, removes shortcuts + registry via the
// native API (including the autostart Run entry), and honours data retention.
package main

import (
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

const (
	uninstallKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\STARBOX`
	appKey       = `HKCU\Software\STARBOX`
	runKey       = `Software\Microsoft\Windows\CurrentVersion\Run`
	runName      = "STARBOX"
	createNoWin  = 0x08000000
)

// palette (COLORREF 0x00BBGGRR)
const (
	colBg    = uintptr(0xf8f8f8)
	colSide  = uintptr(0xf0f0f0)
	colCard  = uintptr(0xffffff)
	colCard2 = uintptr(0xe1e1e1) // light gray fill
	colAcc   = uintptr(0xb86800)
	colFg    = uintptr(0x1a1a1a) // near-black text
	colDim   = uintptr(0x6d6d6d) // mid gray
	colErr   = uintptr(0x6050ff)
	colOnAcc = uintptr(0xffffff) // white on accent
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	bsOwnerDraw        = 0x0000000B
	dtSingle           = 0x0020
	dtCenter           = 0x0001

	wmAppStep = 0x8010
	wmAppDone = 0x8011

	IDYes   = 101
	IDNo    = 102
	IDClose = 103
)

// pages
const (
	pgConfirm = iota
	pgProgress
	pgDone
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")

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
	pFindWindow     = user32.NewProc("FindWindowW")
	pDrawText       = user32.NewProc("DrawTextW")
	pSetTextColor   = gdi32.NewProc("SetTextColor")
	pSetBkMode      = gdi32.NewProc("SetBkMode")
	pSelectObject   = gdi32.NewProc("SelectObject")
	pCreateCompatDC = gdi32.NewProc("CreateCompatibleDC")
	pDeleteDC       = gdi32.NewProc("DeleteDC")
	pCreateDIB      = gdi32.NewProc("CreateDIBSection")
	pBitBlt         = gdi32.NewProc("BitBlt")
	pCreateFont     = gdi32.NewProc("CreateFontW")
	pCreateSolid    = gdi32.NewProc("CreateSolidBrush")
	pDeleteObject   = gdi32.NewProc("DeleteObject")

	wndProc = syscall.NewCallback(wndProcMain)
)

var (
	hwndMain    uintptr
	hInst       uintptr
	fontHead    uintptr
	fontBody    uintptr
	fontSmall   uintptr
	fontBtn     uintptr
	hYes, hNo   uintptr
	hClose      uintptr
	page        = pgConfirm
	dpiScale    = 100
	keepData    = true
	uninBusy    bool
	uninStep    int
	uninErr     string
	uninDir     string
	brushBg     uintptr
	brushSide   uintptr
	brushCard   uintptr
	uninDataDir string
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
		pSendMessage.Call(h, 0x0030, f, 1)
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

func setText(h uintptr, s string) {
	sp, _ := windows.UTF16PtrFromString(s)
	pSetWindowText.Call(h, uintptr(unsafe.Pointer(sp)))
}

// ---------- uninstall steps ----------

var uninSteps = []string{
	"结束运行中的 STARBOX",
	"移除快捷方式",
	"清理注册表信息",
	"删除程序文件",
	"安排收尾清理",
}

func setStep(i int) {
	uninStep = i
	if hwndMain != 0 {
		user32.NewProc("PostMessageW").Call(hwndMain, wmAppStep, uintptr(i), 0)
	}
}

func killRunningApp() {
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
	cmd := exec.Command("taskkill", "/IM", "starbox.exe", "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWin}
	_ = cmd.Run()
}

func startUninstall() {
	uninBusy = true
	uninErr = ""
	go func() {
		setStep(0)
		killRunningApp()

		setStep(1)
		smFolder := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "STARBOX")
		_ = os.RemoveAll(smFolder)
		_ = os.Remove(filepath.Join(os.Getenv("USERPROFILE"), "Desktop", "STARBOX.lnk"))

		setStep(2)
		_ = registry.DeleteKey(registry.CURRENT_USER, uninstallKey)
		_ = registry.DeleteKey(registry.CURRENT_USER, appKey)
		k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
		if err == nil {
			_ = k.DeleteValue(runName) // remove leftover autostart entry
			k.Close()
		}

		setStep(3)
		dir := uninDir
		self, _ := os.Executable()
		if entries, err2 := os.ReadDir(dir); err2 == nil {
			for _, e := range entries {
				p := filepath.Join(dir, e.Name())
				if e.IsDir() {
					if keepData && strings.EqualFold(e.Name(), "data") {
						continue
					}
					_ = os.RemoveAll(p)
					continue
				}
				if self != "" && strings.EqualFold(p, self) {
					continue
				}
				_ = os.Remove(p)
			}
		}

		setStep(4)
		if self != "" {
			k32 := windows.NewLazySystemDLL("kernel32.dll")
			mf := k32.NewProc("MoveFileExW")
			if p, e := syscall.UTF16PtrFromString(self); e == nil {
				mf.Call(uintptr(unsafe.Pointer(p)), 0, 0x4) // delete at reboot
			}
		}
		time.Sleep(250 * time.Millisecond)
		uninBusy = false
		user32.NewProc("PostMessageW").Call(hwndMain, wmAppDone, 0, 0)
	}()
}

// ---------- pages ----------

func layoutButtons() {
	w, h := clientSize()
	bw, bh := scale(118), scale(42)
	y := h - scale(30) - bh
	yesVisible := page == pgConfirm && !uninBusy
	noVisible := page == pgConfirm
	closeVisible := page == pgDone
	pShowWindow.Call(hYes, b2v(yesVisible))
	pShowWindow.Call(hNo, b2v(noVisible))
	pShowWindow.Call(hClose, b2v(closeVisible))
	pMoveWindow.Call(hYes, uintptr(w-scale(24)-2*bw-scale(10)), uintptr(y), uintptr(bw), uintptr(bh), 1)
	pMoveWindow.Call(hNo, uintptr(w-scale(24)-bw), uintptr(y), uintptr(bw), uintptr(bh), 1)
	pMoveWindow.Call(hClose, uintptr(w-scale(24)-bw), uintptr(y), uintptr(bw), uintptr(bh), 1)
}

func b2v(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

func drawCheck(dc uintptr, x, y, size int, text string, val *bool) {
	fillRect(dc, x, y, size, size, colCard)
	if val != nil && *val {
		inner := size * 4 / 10
		fillRect(dc, x+inner/2, y+inner/2, size-inner, size-inner, colAcc)
	}
	drawText(dc, x+size+scale(12), y, 600, size, text, fontBody, colFg, 0x0025)
}

func paintPage(dc uintptr, w, h int) {
	x := scale(28)
	cw := w - 2*x
	contentY := scale(104)

	switch page {
	case pgConfirm:
		drawText(dc, x, contentY+scale(12), cw, scale(40), "卸载 星匣 STARBOX", fontHead, colFg, dtSingle)
		drawText(dc, x, contentY+scale(66), cw, scale(56),
			"即将从你的电脑移除 STARBOX。卸载前会先关闭正在运行的程序。", fontBody, colDim, 0)
		cbY := contentY + scale(132)
		drawCheck(dc, x, cbY, scale(22), "保留我的数据（番剧库 / 封面 / 设置）", &keepData)
		note := "卸载后程序文件与快捷方式将被删除。"
		if keepData {
			note = "你的数据目录（data）将被完整保留，重装后可继续使用。"
		} else {
			note = "注意：你的数据目录（data）也将被删除，且无法恢复。"
		}
		ny := contentY + scale(182)
		fillRect(dc, x, ny, cw, scale(54), colSide)
		drawText(dc, x+scale(10), ny+scale(8), cw-scale(20), scale(40), note, fontSmall, colAcc, 0x0025)
		drawText(dc, x, ny+scale(64), cw, scale(50),
			"卸载位置："+uninDir, fontSmall, colDim, 0)
	case pgProgress:
		drawText(dc, x, contentY+scale(8), cw, scale(34), "正在卸载…", fontBody, colFg, dtSingle)
		sy := contentY + scale(56)
		for i, s := range uninSteps {
			glyph, color := "○", colDim
			if i < uninStep || (uninErr != "" && i < uninStep) {
				glyph, color = "✓", colAcc
			} else if i == uninStep && uninErr == "" {
				glyph, color = "●", colAcc
			}
			drawText(dc, x, sy+i*scale(34), scale(28), scale(28), glyph, fontBody, color, dtCenter)
			txtCol := uintptr(colFg)
			if i > uninStep {
				txtCol = colDim
			}
			drawText(dc, x+scale(36), sy+i*scale(34), cw-scale(36), scale(28), s, fontSmall, txtCol, 0x0025)
		}
		barY := contentY + contentH0(h) - scale(56)
		fillRect(dc, x, barY, cw, scale(10), colCard)
		done := uninStep * 100 / len(uninSteps)
		if uninErr != "" {
			drawText(dc, x, barY+scale(18), cw, scale(26), "卸载出错："+uninErr, fontSmall, colErr, 0)
		} else if done > 0 {
			fillRect(dc, x, barY, cw*done/100, scale(10), colAcc)
		}
	case pgDone:
		drawText(dc, x, contentY+scale(16), cw, scale(44), "卸载完成", fontHead, colAcc, dtSingle)
		msg := "STARBOX 已从你的电脑移除。"
		if keepData {
			msg += "\n你的数据已保留在原目录的 data 文件夹中，重装即可继续使用。"
		}
		msg += "\n\n感谢使用 星匣 STARBOX。"
		drawText(dc, x, contentY+scale(84), cw, scale(140), msg, fontBody, colFg, 0)
		if uninErr != "" {
			drawText(dc, x, contentY+scale(200), cw, scale(30), "部分文件将在重启后自动清理。", fontSmall, colDim, dtSingle)
		}
	}
}

func contentH0(h int) int { return h - scale(84) - scale(104) }

func paintChrome(dc uintptr, w, h int) {
	bh := scale(92)
	fillRect(dc, 0, 0, w, bh, colSide)
	fillRect(dc, 0, bh-scale(3), w, scale(3), colAcc)
	drawText(dc, scale(28), scale(16), w-scale(56), scale(40), "星匣 STARBOX", fontHead, colFg, dtSingle)
	sub := "卸载向导"
	if page == pgProgress && uninBusy {
		sub = "正在卸载，请稍候…"
	}
	drawText(dc, scale(28), scale(56), w-scale(56), scale(26), sub, fontSmall, colDim, dtSingle)
	fy := h - scale(84)
	fillRect(dc, 0, fy, w, scale(1), colCard2)
	fillRect(dc, 0, bh, w, fy-bh, colBg)
}

func hitCheck(x, y int) {
	if page != pgConfirm {
		return
	}
	size := scale(22)
	cbY := scale(104) + scale(132)
	if x >= scale(28) && x <= scale(28)+size+600 && y >= cbY && y <= cbY+size {
		keepData = !keepData
		pInvalidateRect.Call(hwndMain, 0, 1)
	}
}

func startUninstallWrapper() {
	if uninBusy {
		return
	}
	page = pgProgress
	layoutButtons()
	pInvalidateRect.Call(hwndMain, 0, 1)
	startUninstall()
}

func wndProcMain(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case 0x0111: // WM_COMMAND
		switch uintptr(0xFFFF) & wParam {
		case IDYes:
			startUninstallWrapper()
			return 0
		case IDNo:
			if !uninBusy {
				pDestroyWindow.Call(hwnd)
			}
			return 0
		case IDClose:
			pDestroyWindow.Call(hwnd)
			return 0
		}
	case 0x0100: // WM_KEYDOWN
		switch wParam {
		case 0x1B: // Esc
			if !uninBusy {
				pDestroyWindow.Call(hwnd)
			}
			return 0
		case 0x0D: // Enter
			if page == pgConfirm {
				startUninstallWrapper()
			} else if page == pgDone {
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
					Size        uint32
					Width       int32
					Height      int32
					Planes      uint16
					BitCount    uint16
					Compression uint32
					SizeImage   uint32
					XPpm        int32
					YPpm        int32
					ClrUsed     uint32
					ClrImp      uint32
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
	case 0x0202: // WM_LBUTTONUP
		x := int(int16(uint16(lParam & 0xFFFF)))
		y := int(int16(uint16((lParam >> 16) & 0xFFFF)))
		hitCheck(x, y)
		return 0
	case 0x0010: // WM_CLOSE
		if !uninBusy {
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

func wndProcBtn(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case 0x0111:
		switch uintptr(0xFFFF) & wParam {
		case IDYes, IDNo, IDClose:
			return 0
		}
	}
	r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

func drawItem(wParam, lParam uintptr) uintptr {
	di := (*drawItemStruct)(unsafe.Pointer(lParam))
	id := uintptr(di.CtlID)
	fill := uintptr(colCard)
	tc := uintptr(colFg)
	switch id {
	case IDYes:
		fill, tc = colErr, colFg
		if di.ItemState&0x0001 != 0 {
			fill = uintptr(0x0000D0)
		}
	}
	drawBtn(di, fill, tc)
	if di.ItemState&0x0010 != 0 {
		br, _, _ := pCreateSolid.Call(colAcc)
		rc := di.RcItem
		rc.Bottom = rc.Top + 3
		user32.NewProc("FillRect").Call(di.HDC, uintptr(unsafe.Pointer(&rc)), br)
		pDeleteObject.Call(br)
	}
	return 1
}

func curIcon(hInst uintptr) uintptr {
	r, _, _ := pLoadIcon.Call(hInst, 1)
	if r == 0 {
		r, _, _ = pLoadIcon.Call(0, 32512)
	}
	return r
}

func main() {
	runtime.LockOSThread()
	user32.NewProc("SetProcessDPIAware").Call()
	user32.NewProc("SetThreadDpiAwarenessContext").Call(^uintptr(3))
	if r, _, _ := user32.NewProc("GetDpiForSystem").Call(); r != 0 {
		dpiScale = int(r) * 100 / 96
	}

	mod, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	hInst = mod

	brushBg, _, _ = pCreateSolid.Call(colBg)
	brushSide, _, _ = pCreateSolid.Call(colSide)
	brushCard, _, _ = pCreateSolid.Call(colCard)

	// uninstall dir = dir of this exe
	if self, err := os.Executable(); err == nil {
		uninDir = filepath.Dir(self)
	}

	clsName := utf16p("STARBOXUninWnd")
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

	ww, wh := scale(620), scale(440)
	hwndMain, _, _ = pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(utf16p("卸载 星匣 STARBOX"))),
		uintptr(wsOverlappedWindow&^0x00030000&^0x00040000),
		0x80000000, 0x80000000, uintptr(ww), uintptr(wh),
		0, 0, hInst, 0)

	fontHead = makeFont(scale(24), true)
	fontBody = makeFont(scale(16), false)
	fontSmall = makeFont(scale(13), false)
	fontBtn = makeFont(scale(15), false)

	hYes = makeChild("BUTTON", "卸载", bsOwnerDraw, IDYes, 0, 0, 0, 0)
	hNo = makeChild("BUTTON", "取消", bsOwnerDraw, IDNo, 0, 0, 0, 0)
	hClose = makeChild("BUTTON", "关闭", bsOwnerDraw, IDClose, 0, 0, 0, 0)
	setFont(hYes, fontBtn)
	setFont(hNo, fontBtn)
	setFont(hClose, fontBtn)

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
	_ = fmt.Sprintf
	_ = strings.TrimSpace
}
