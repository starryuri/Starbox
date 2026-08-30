//go:build windows

// STARBOX — native Win32 desktop app (no WebView2, no Gio). Reliable clicks.
// Dark theme (navy + cyan accent), owner-drawn sidebar nav, responsive layout.
package main

import (
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"butler/internal/anime"
	"butler/internal/config"
	"butler/internal/kb"

)



const (
	wsOverlappedWindow = 0x00CF0000
	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	ssLeft             = 0x00000000
	bsOwnerDraw        = 0x0000000B
	bsAutoCheckBox     = 0x00000003
	esAutoHScroll      = 0x00000080
	esPassword         = 0x00000020
	wsTabStop          = 0x00010000
)

const (
	IDBrand = 1
	navBase = 100
	IDTitle = 301
	IDBody  = 302
	K_CARD  = 401
	IDPlat  = 501 // 4 platform buttons: 501..504
	IDAcc   = 505
	IDPass  = 506
	IDSave  = 507
	IDReff  = 508
	IDReffMine = 511
	IDInfo  = 509
	IDHint  = 510
	IDAuto  = 601
	IDSaveS = 602
	IDThN   = 603 // theme: night
	IDThS   = 604 // theme: sakura
	IDThD   = 605 // theme: day
	IDQuitE = 606 // quit action: exit
	IDQuitT = 607 // quit action: to tray (reserved)
	IDSilent = 608 // silent start
	IDProfPrev = 609
	IDProfNext = 610
	IDProfNew  = 611
	IDProfDel  = 612
	IDProfName = 613
	KBTab    = 701 // 5 tabs: 701..705
	KBToA    = 706 // add title edit
	KBAdd    = 707 // add button
	KBSearch = 708 // search button
)

func pBool(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

const dataDirName = "data"

// GDI / painting constants
const (
	biRGB        = 0
	dibRGBColors = 0
	srcCopy      = 0x00CC0020
	colorOnColor = 3
	wmAppCover   = 0x8001
	wmAppRefresh = 0x8002
	wmOverview   = 0x8003
	wmInsight    = 0x8004
	wmDisk       = 0x8005
	wmFavWorks   = 0x8006
	wmSearchDone = 0x8007
	wmRss        = 0x8008
	wmDetail     = 0x800A
	wmStatusTick = 0x800C
	wmBindDone      = 0x800B
	wmDebugCard     = 0x8010
)

// DrawText flags
const (
	dtLeft      = 0x00
	dtCenter    = 0x01
	dtVCenter   = 0x04
	dtWordBreak = 0x10
	dtSingle    = 0x20
	dtNoPrefix  = 0x0800
)

var pages = []string{"overview", "disk", "rss", "insight", "kb", "favs", "notify", "rules", "settings"}

var pageLabels = map[string]string{
	"overview": "概况", "disk": "磁盘", "rss": "订阅", "insight": "情报",
	"kb": "知识库", "favs": "收藏", "notify": "通知", "rules": "规则", "settings": "设置",
}

// colors (COLORREF 0x00BBGGRR)
// color vars — driven by the active theme (theme.go); mutable so theme
// switching can repaint live without restarting.
var (
	colBg    uintptr
	colSide  uintptr
	colAcc   uintptr
	colFg    uintptr
	colOnAcc uintptr
	colCard  uintptr
	colCard2 uintptr
	colDim   uintptr
	colRed   uintptr
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	uxtheme  = windows.NewLazySystemDLL("uxtheme.dll")

	pCreateWindowEx     = user32.NewProc("CreateWindowExW")
	pSetWindowLongPtr   = user32.NewProc("SetWindowLongPtrW")
	pCallWindowProc     = user32.NewProc("CallWindowProcW")
	pDefWindowProc      = user32.NewProc("DefWindowProcW")
	pDestroyWindow      = user32.NewProc("DestroyWindow")
	pRegisterClassEx    = user32.NewProc("RegisterClassExW")
	pCreateFont         = gdi32.NewProc("CreateFontW")
	pGetTextExtentPoint = gdi32.NewProc("GetTextExtentPoint32W")
	pSendMessage        = user32.NewProc("SendMessageW")
	pPostMessage        = user32.NewProc("PostMessageW")
	pSetTimer           = user32.NewProc("SetTimer")
	pSetWindowText      = user32.NewProc("SetWindowTextW")
	pShowWindow         = user32.NewProc("ShowWindow")
	pUpdateWindow       = user32.NewProc("UpdateWindow")
	pInvalidateRect     = user32.NewProc("InvalidateRect")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pLoadIcon           = user32.NewProc("LoadIconW")
	pPostQuitMessage    = user32.NewProc("PostQuitMessage")
	pCreateSolidBrush   = gdi32.NewProc("CreateSolidBrush")
	pSetTextColor       = gdi32.NewProc("SetTextColor")
	pSetBkMode          = gdi32.NewProc("SetBkMode")
	pSetBkColor         = gdi32.NewProc("SetBkColor")
	pFillRect           = user32.NewProc("FillRect")
	pDrawText           = user32.NewProc("DrawTextW")
	pGetDlgCtrlID       = user32.NewProc("GetDlgCtrlID")
	pGetClientRect      = user32.NewProc("GetClientRect")
	pMoveWindow         = user32.NewProc("MoveWindow")
	pGetWindowRect      = user32.NewProc("GetWindowRectW")
	pGetDpiForWindow    = user32.NewProc("GetDpiForWindow")
	pSetThreadDpiAware  = user32.NewProc("SetThreadDpiAwarenessContext")
	pGetDpiForMonitor   = user32.NewProc("GetDpiForMonitor")
	pAdjustWindowRectEx = user32.NewProc("AdjustWindowRectEx")
	pGetDC              = user32.NewProc("GetDC")
	pReleaseDC          = user32.NewProc("ReleaseDC")
	pBeginPaint         = user32.NewProc("BeginPaint")
	pEndPaint           = user32.NewProc("EndPaint")
	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pStretchBlt         = gdi32.NewProc("StretchBlt")
	pSetStretchBltMode  = gdi32.NewProc("SetStretchBltMode")
	pBitBlt             = gdi32.NewProc("BitBlt")
	pTrackMouseEvent    = user32.NewProc("TrackMouseEvent")
	pSetCursor          = user32.NewProc("SetCursor")
	pLoadCursor         = user32.NewProc("LoadCursorW")
	pSetWindowTheme    = uxtheme.NewProc("SetWindowTheme")
)

var (
	hwndMain            uintptr
	fontTitle, fontNav  uintptr
	fontCard, fontBody  uintptr
	fontTiny            uintptr
	brushBg, brushCard  uintptr
	hBrand, hTag        uintptr
	hNav                [20]uintptr
	hCards              [4]uintptr
	hTitle, hBody       uintptr
	st                  *kb.Store
	curPlat             int
	hPlat               [4]uintptr
	hAcc, hPass, hSave  uintptr
	hReff, hInfo, hHint uintptr
	hReffMine           uintptr
	hAuto, hAutoSave    uintptr
	hThN, hThS, hThD    uintptr
	hQuitE, hQuitT      uintptr
	hSilent             uintptr
	hProfPrev, hProfNext uintptr
	hProfName, hProfNew, hProfDel uintptr
	hProfLabel          uintptr
	optSilent           bool
	kbCol               string
	hKbTab              [5]uintptr
	hKbToA, hKbAddBtn   uintptr
	hKbSearchBtn        uintptr
	page                string
	dataDir             string
	wndProc             = syscall.NewCallback(wndProcMain)
)

// --- KB anime card / detail state ---
var (
	kbRecs   []kb.Record
	hoverAct  string
	hoverID   string
	hoverTrk  bool
	curHand   uintptr
	dpiScale  int // 100 = 100%
	curArrow  uintptr
	wheelAccum int
	kbCards  []kbCard
	kbScroll int
	detailID string
	detHits  []detHit
	coverDir string
	covers   sync.Map // id -> *covInfo
	// anime search-and-pick
	searchMode    bool
	detailBusy    bool
	detailLoading string // record id being enriched
	detailInfo    *anime.Detail

	detailScroll        int
	detailContentBottom int // bottom Y of the last painted detail content

	searchBusy    bool
	searchQuery   string
	searchResults []anime.Result
	bgmFallback   bool // set when Bangumi relay fails/empty this session
)

// --- generic themed list state (favs / notify / rules) ---
var (
	listRows   []listRow
	listHits   []detHit
	listPage   string // "favs" | "notify" | "rules"
	listAct    bool   // whether an action button (top-right) is shown
	listActL   string // action label
	listScroll int
	// favs detail (works view)
	favDetailID string
	favScroll   int
	favEntName  string
	favEntType  string
	favEntImage string
	favWorks    []anime.Media
	favBusy     bool
)

type listRow struct {
	id, title, sub, tag string
	accent              bool // highlight (e.g. unread / current)
}


type kbCard struct {
	id, title, status string
	x, y, w, h        int
}

type detHit struct {
	x, y, w, h int
	action     string
	id         string
}

type covInfo struct {
	hbmp   uintptr
	w, h   int
	loaded bool
	path   string
}

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

type paintStruct struct {
	HDC         uintptr
	FErase      uint32
	RcPaint     rect
	FRestore    uint32
	FIncUpdate  uint32
	RGBReserved [32]byte
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

type bitmapInfo struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

func drawBtn(di *drawItemStruct, fill, tc uintptr) {
	br, _, _ := pCreateSolidBrush.Call(fill)
	pFillRect.Call(di.HDC, uintptr(unsafe.Pointer(&di.RcItem)), br)
	pDeleteObject.Call(br)
	if di.ItemState&0x0010 != 0 { // ODS_FOCUS — visible keyboard focus
		br, _, _ = pCreateSolidBrush.Call(colAcc)
		rc := di.RcItem
		rc.Bottom = rc.Top + 3
		pFillRect.Call(di.HDC, uintptr(unsafe.Pointer(&rc)), br)
		pDeleteObject.Call(br)
	}
	pSetBkMode.Call(di.HDC, 1)
	pSetTextColor.Call(di.HDC, tc)
	label := getText(di.HwndItem)
	bw2, bh2 := int(di.RcItem.Right-di.RcItem.Left), int(di.RcItem.Bottom-di.RcItem.Top)
	drawTextRectFit(di.HDC, int(di.RcItem.Left), int(di.RcItem.Top), bw2, bh2, label, 24, false, tc, 0x0025)
}

func drawItem(diPtr uintptr) uintptr {
	di := (*drawItemStruct)(unsafe.Pointer(diPtr))
	id := uintptr(di.CtlID)
	switch {
	case id >= navBase && id < uintptr(navBase+len(pages)):
		idx := id - navBase
		fill := uintptr(colSide)
		tc := uintptr(colFg)
		if pages[idx] == page {
			fill = colAcc
			tc = colOnAcc
		}
		drawBtn(di, fill, tc)
		return 1
	case id >= KBTab && id < uintptr(KBTab+len(kbCols)):
		idx := id - KBTab
		fill := uintptr(colCard2)
		tc := uintptr(colFg)
		if kbCols[idx] == kbCol {
			fill = colAcc
			tc = colOnAcc
		}
		drawBtn(di, fill, tc)
		return 1
	case id == KBAdd:
		drawBtn(di, colAcc, colOnAcc)
		return 1
	case id == KBSearch:
		drawBtn(di, colAcc, colOnAcc)
		return 1
	case id >= IDPlat && id < IDPlat+4:
		fill := uintptr(colCard2)
		tc := uintptr(colFg)
		if int(id-IDPlat) == curPlat {
			fill = colAcc
			tc = colOnAcc
		}
		drawBtn(di, fill, tc)
		return 1
	case id == IDSaveS || id == IDSave:
		drawBtn(di, colAcc, colOnAcc)
		return 1
	case id == IDReff || id == IDReffMine || id == IDProfPrev || id == IDProfNext || id == IDProfNew || id == IDProfDel:
		drawBtn(di, colCard2, colFg)
		return 1
	case id == IDThN || id == IDThS || id == IDThD:
		want := "day"
		if id == IDThN {
			want = "night"
		} else if id == IDThS {
			want = "sakura"
		}
		if activeThemeID == want {
			drawBtn(di, colAcc, colOnAcc)
		} else {
			drawBtn(di, colCard2, colFg)
		}
		return 1
	}
	r, _, _ := pDefWindowProc.Call(hwndMain, 0x002B, 0, 0)
	return r
}

func main() {
	runtime.LockOSThread()
	user32.NewProc("SetProcessDPIAware").Call()
	if !acquireSingleInstance() {
		return // existing window was raised instead
	}
	// per-monitor v2 awareness (-4 = DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2);
	// older systems ignore it (we keep the legacy SetProcessDPIAware fallback path)
	user32.NewProc("SetThreadDpiAwarenessContext").Call(^uintptr(3))
	mod, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	hInst := mod

	exe, _ := os.Executable()
	dataDir = filepath.Join(filepath.Dir(exe), dataDirName)
	coverDir = filepath.Join(dataDir, "covers")
	_ = os.MkdirAll(coverDir, 0o755)
	cfg, _ = config.Load(filepath.Join(filepath.Dir(exe), "config.json"))
	initProfiles() // migrate legacy data on first run + resolve identity
	st = kb.New(curProfDir)
	loadThemeChoice()
	applyTheme()
	loadDetailLayout()
	initTray() // registers icon + honors -tray/-silent
	page = "overview"


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

	// initial size fits the actual desktop: never bigger than the work area,
	// never smaller than the minimum layout (1180x700). On small/hi-DPI
	// screens this prevents the window from hanging off-screen.
	initW, initH := 1720, 1060
	if rc := user32.NewProc("GetSystemMetrics"); true {
		_ = rc
	}
	var wa rect
	user32.NewProc("SystemParametersInfoW").Call(0x0030, 0, uintptr(unsafe.Pointer(&wa)), 0) // SPI_GETWORKAREA
	if wa.Right > 0 && wa.Bottom > 0 {
		ww, wh := int(wa.Right)-int(wa.Left), int(wa.Bottom)-int(wa.Top)
		if initW > ww {
			initW = ww
		}
		if initH > wh {
			initH = wh
		}
			minW2, minH2 := scale(1180), scale(700)
			if initW < minW2 {
				initW = minW2
			}
			if initH < minH2 {
				initH = minH2
			}
	}
	hwndMain, _, _ = pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(utf16("星匣 STARBOX"))),
		uintptr(wsOverlappedWindow),
		0x80000000, 0x80000000, uintptr(initW), uintptr(initH),
		0, 0, hInst, 0)
	enableDarkTitleBar(hwndMain)
	curHand, _, _ = pLoadCursor.Call(0, 32649)  // IDC_HAND
	curArrow, _, _ = pLoadCursor.Call(0, 32512) // IDC_ARROW
	if r, _, _ := pGetDpiForWindow.Call(hwndMain); r != 0 {
		dpiScale = int(r) * 100 / 96
	}
	computeUiScale()
	pSetTimer.Call(hwndMain, 1, 5000, 0) // overview auto-refresh (WM_TIMER id 1)

	fontTitle = createWin32Font(38, true)
	fontNav = createWin32Font(24, false)
	fontCard = createWin32Font(28, false)
	fontBody = createWin32Font(23, false)
	fontTiny = createWin32Font(18, false)

	hBrand = createChild("STATIC", "星匣 STARBOX", ssLeft, IDBrand, 26, 26, 290, 60, fontTitle)
	hTag = createChild("STATIC", "你的次元 · 收于一匣", ssLeft, 0, 30, 86, 230, 26, fontNav)
	for i, p := range pages {
		label := pageLabels[p]
		if p == page {
			label = "● " + label
		}
		hNav[i] = createChild("BUTTON", label, bsOwnerDraw, navBase+i, 26, 126+i*48, 228, 42, fontNav)
	}
	hTitle = createChild("STATIC", "", ssLeft, IDTitle, 310, 30, 900, 46, fontTitle)
	for i := 0; i < 4; i++ {
		hCards[i] = createChild("STATIC", "", ssLeft, K_CARD+i, 310+i*210, 96, 200, 150, fontCard)
	}
	hBody = createChild("STATIC", "", ssLeft, IDBody, 310, 280, 900, 480, fontBody)
	// insight page controls
	for i := range hPlat {
		hPlat[i] = createChild("BUTTON", platLabels[i], bsOwnerDraw, IDPlat+i, 310+i*130, 96, 124, 40, fontNav)
	}
	hAcc = createChild("EDIT", "", esAutoHScroll|wsTabStop, IDAcc, 310, 160, 380, 32, fontBody)
	hPass = createChild("EDIT", "", esAutoHScroll|esPassword|wsTabStop, IDPass, 700, 160, 380, 32, fontBody)
	hSave = createChild("BUTTON", "保存账号", bsOwnerDraw, IDSave, 1092, 158, 120, 36, fontNav)
	hReff = createChild("BUTTON", "刷新热门", bsOwnerDraw, IDReff, 310, 214, 120, 34, fontNav)
	hReffMine = createChild("BUTTON", "我的仓库", bsOwnerDraw, IDReffMine, 440, 214, 120, 34, fontNav)
	hHint = createChild("STATIC", "", ssLeft, IDHint, 440, 220, 760, 26, fontNav)
	hInfo = createChild("STATIC", "", ssLeft, IDInfo, 310, 264, 940, 440, fontBody)
	// settings page controls
	hAuto = createChild("BUTTON", "开机自启动", bsAutoCheckBox, IDAuto, 310, 120, 240, 40, fontNav)
	hSilent = createChild("BUTTON", "静默启动（自启动时不弹窗）", bsAutoCheckBox, IDSilent, 560, 120, 340, 40, fontNav)
	hAutoSave = createChild("BUTTON", "保存设置", bsOwnerDraw, IDSaveS, 910, 118, 130, 40, fontNav)
	// theme row
	hThN = createChild("BUTTON", "暗夜", bsOwnerDraw, IDThN, 310, 174, 150, 44, fontNav)
	hThS = createChild("BUTTON", "樱夜", bsOwnerDraw, IDThS, 470, 174, 150, 44, fontNav)
	hThD = createChild("BUTTON", "白天", bsOwnerDraw, IDThD, 630, 174, 150, 44, fontNav)
	// quit action
	hQuitE = createChild("BUTTON", "关闭窗口 = 退出程序", bsAutoCheckBox, IDQuitE, 310, 232, 300, 40, fontNav)
	hQuitT = createChild("BUTTON", "关闭窗口 = 收纳到托盘（需托盘支持）", bsAutoCheckBox, IDQuitT, 620, 232, 380, 40, fontNav)
	// identity (账户) row
	hProfLabel = createChild("STATIC", "", ssLeft, 0, 310, 296, 700, 36, fontNav)
	hProfPrev = createChild("BUTTON", "◀ 上一个", bsOwnerDraw, IDProfPrev, 310, 340, 130, 40, fontNav)
	hProfNext = createChild("BUTTON", "下一个 ▶", bsOwnerDraw, IDProfNext, 450, 340, 130, 40, fontNav)
	hProfName = createChild("EDIT", "", esAutoHScroll, IDProfName, 596, 342, 240, 38, fontBody)
	hProfNew = createChild("BUTTON", "新建身份", bsOwnerDraw, IDProfNew, 846, 340, 130, 40, fontNav)
	hProfDel = createChild("BUTTON", "删除当前", bsOwnerDraw, IDProfDel, 986, 340, 130, 40, fontNav)
	// kb page controls
	kbCol = "anime"
	for i := range kbCols {
		hKbTab[i] = createChild("BUTTON", kbColLabels[kbCols[i]], bsOwnerDraw, KBTab+i, 310+i*120, 96, 116, 40, fontNav)
	}
	hKbToA = createChild("EDIT", "", esAutoHScroll|wsTabStop, KBToA, 310, 160, 460, 36, fontBody)
	hKbAddBtn = createChild("BUTTON", "＋ 添加", bsOwnerDraw|wsTabStop, KBAdd, 780, 158, 110, 40, fontNav)
	hKbSearchBtn = createChild("BUTTON", "搜索并添加", bsOwnerDraw|wsTabStop, KBSearch, 900, 158, 150, 40, fontNav)
	// dark-theme the edit boxes (disable visual styles so WM_CTLCOLOREDIT applies)
	empty := utf16("")
	pSetWindowTheme.Call(hKbToA, uintptr(unsafe.Pointer(empty)), uintptr(unsafe.Pointer(empty)))
	pSetWindowTheme.Call(hAcc, uintptr(unsafe.Pointer(empty)), uintptr(unsafe.Pointer(empty)))
	pSetWindowTheme.Call(hPass, uintptr(unsafe.Pointer(empty)), uintptr(unsafe.Pointer(empty)))
	pSetWindowTheme.Call(hProfName, uintptr(unsafe.Pointer(empty)), uintptr(unsafe.Pointer(empty)))
	// forward wheel messages from the edits so scrolling never dies after typing
	subclassEditWheel(hKbToA)
	subclassEditWheel(hAcc)
	subclassEditWheel(hPass)

	webInit()
	renderPage()

	pShowWindow.Call(hwndMain, 5)
	pUpdateWindow.Call(hwndMain)
	relayout()


	var msg msgStruct
	for {
		r, _, _ := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		// wheel from a focused EDIT goes to the main window so lists keep scrolling
		if msg.message == 0x020A && msg.hwnd != hwndMain {
			msg.hwnd = hwndMain
		}
		// dialog manager handles Tab navigation + keyboard mnemonics
		isDlg, _, _ := user32.NewProc("IsDialogMessageW").Call(hwndMain, uintptr(unsafe.Pointer(&msg)))
		if isDlg != 0 {
			continue
		}
		user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
		user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}
}

