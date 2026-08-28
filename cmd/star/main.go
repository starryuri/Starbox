//go:build windows

// STARBOX — native Win32 desktop app (no WebView2, no Gio). Reliable clicks.
// Dark theme (navy + cyan accent), owner-drawn sidebar nav, responsive layout.
package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"butler/internal/anime"
	"butler/internal/config"
	"butler/internal/du"
	"butler/internal/githot"
	"butler/internal/kb"
	"butler/internal/monitor"
	"butler/internal/rss"
	"butler/internal/settings"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
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
	wmBindDone      = 0x800B
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
const (
	colBg    = 0x20100c // #0c1020
	colSide  = 0x2b1610 // #10162b
	colAcc   = 0xeed322 // #22d3ee
	colFg    = 0xf7ece7 // #e7ecf7
	colOnAcc = 0x170e0b
	colCard  = 0x4a3c20 // #203c4a
	colCard2 = 0x60502e // #2e5060 (cover placeholder)
	colDim   = 0x8f8271 // #71828f
	colRed   = 0x2e201a // #1a202e dark red (delete)
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
	kbCol               string
	hKbTab              [5]uintptr
	hKbToA, hKbAddBtn   uintptr
	hKbSearchBtn        uintptr
	page                string
	mgr                 *monitor.State
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
	searchBusy    bool
	searchQuery   string
	searchResults []anime.Result
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
		return fmt.Sprintf("%d 天 %d 小时", d, h)
	}
	if h > 0 {
		return fmt.Sprintf("%d 小时 %d 分", h, m)
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

func loadBind() {
	bindStatus = ""
	recs, _ := st.List("connect")
	m := map[string]interface{}{}
	if len(recs) > 0 {
		m = recs[0].Data
	}
	acc, _ := m[bindKeys[curPlat]].(string)
	pass, _ := m[bindKeys[curPlat]+"_pass"].(string)
	setText(hAcc, acc)
	setText(hPass, pass)
	setText(hHint, "凭据仅存本机")
}

// verifyBind checks GitHub credentials via githot.Auth (async) and, on success,
// stores the token (DPAPI-protected at rest) plus the login name in connect.
// verifyBind checks GitHub credentials via githot.Auth (async) and, on success,
// stores the token (DPAPI-protected at rest) plus the login name in connect.
func verifyBind(token string) {
	if bindVerifying || token == "" {
		return
	}
	bindVerifying = true
	bindStatus = "（正在验证 GitHub 凭据…）"
	setText(hHint, bindStatus)
	go func() {
		acc, err := githot.Auth(token)
		if err != nil {
			bindStatus = "（验证失败：" + err.Error() + "）"
			bindVerifying = false
			pPostMessage.Call(hwndMain, uintptr(wmBindDone), 0, 0)
			return
		}
		bindToken = token
		bindLogin = acc.Login
		prot := settings.DPAPIProtect(token)
		recs, _ := st.List("connect")
		m := map[string]interface{}{}
		if len(recs) > 0 {
			m = recs[0].Data
		}
		m["github_token"] = prot
		m["github_login"] = acc.Login
		if len(recs) > 0 {
			_, _ = st.Update("connect", recs[0].ID, m)
		} else {
			_, _ = st.Add("connect", m)
		}
		bindStatus = "已绑定 @"+acc.Login+"（凭据已加密保存）"
		bindVerifying = false
		pPostMessage.Call(hwndMain, uintptr(wmBindDone), 0, 0)
	}()
}

// refreshMyRepos pulls the bound account's repos into the insight text.

func refreshMyRepos() {
	if bindToken == "" || reposBusy {
		return
	}
	reposBusy = true
	go func() {
		repos, err := githot.MyRepos(bindToken)
		if err == nil && len(repos) > 0 {
			var sb strings.Builder
			sb.WriteString("我的仓库（@" + bindLogin + "）:\n")
			for _, r := range repos {
				sb.WriteString(fmt.Sprintf("  ★ %-6d %s\n", r.Stars, r.Name))
			}
			insText = strings.TrimRight(sb.String(), "\n")
		}
		reposBusy = false
		pPostMessage.Call(hwndMain, uintptr(wmInsight), 0, 0)
	}()
}

func saveBind() {
	recs, _ := st.List("connect")
	m := map[string]interface{}{}
	if len(recs) > 0 {
		m = recs[0].Data
	}
	m[bindKeys[curPlat]] = getText(hAcc)
	m[bindKeys[curPlat]+"_pass"] = getText(hPass)
	if len(recs) > 0 {
		_, _ = st.Update("connect", recs[0].ID, m)
	} else {
		_, _ = st.Add("connect", m)
	}
	setText(hHint, "已保存到本机")
}

func insightInfo() string {
	repos, err := githot.Trending(7, "")
	if err != nil {
		return "（获取 GitHub 热门失败：" + err.Error() + "）"
	}
	if len(repos) == 0 {
		return "（暂无热门）"
	}
	var sb strings.Builder
	for _, r := range repos {
		sb.WriteString(fmt.Sprintf("★ %s  (%d★)  %s\n", r.Name, r.Stars, r.Desc))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func computeStats() (c0, m0, u0, d0 string) {
	if c, err := cpu.Percent(0, false); err == nil && len(c) > 0 {
		c0 = fmt.Sprintf("%.0f%%", c[0])
	}
	if m, err := mem.VirtualMemory(); err == nil {
		m0 = fmt.Sprintf("%.0f%%", m.UsedPercent)
	}
	if up, err := host.Uptime(); err == nil {
		u0 = fmtDuration(up)
	}
	if parts, err := disk.Partitions(false); err == nil && len(parts) > 0 {
		if u, err := disk.Usage(parts[0].Mountpoint); err == nil && u.Total > 0 {
			d0 = fmt.Sprintf("%.0f%%", u.UsedPercent)
		}
	}
	return
}

// async loaders keep the UI thread free so blocking network/scan work never
// makes the window "not responding". Results are posted back for the UI thread.
var (
	ovBusy, insBusy, dskBusy bool
	ovLoaded                 bool // overview stats fetched at least once
	rssBusy                  bool
	rssText                  string
	cfg                      *config.Config
	bindVerifying bool
	reposBusy      bool
	bindStatus    string
	bindToken     string // verified github token (session only)
	bindLogin     string
	ovStat                  [4]string
	ovBody, insText, dskBody string
)

// wmAppRefreshNow asks the UI thread to re-run insightInfo off-thread and show it.
const wmAppRefreshNow = 0x8009

func loadOverview() {
	if ovBusy {
		return
	}
	ovBusy = true
	if !ovLoaded { // placeholder text only on first load — no flicker on refresh
		setText(hCards[0], "CPU:\n…")
		setText(hCards[1], "内存:\n…")
		setText(hCards[2], "运行:\n…")
		setText(hCards[3], "磁盘:\n…")
	}
	go func() {
		c0, m0, u0, d0 := computeStats()
		ovStat[0], ovStat[1], ovStat[2], ovStat[3] = c0, m0, u0, d0
		ovBody = diskText()
		ovLoaded = true
		ovBusy = false
		pPostMessage.Call(hwndMain, uintptr(wmOverview), 0, 0)
	}()
}

func loadInsight() {
	if insBusy {
		return
	}
	insBusy = true
	setText(hInfo, "（正在获取 GitHub 热门…）")
	go func() {
		insText = insightInfo()
		insBusy = false
		pPostMessage.Call(hwndMain, uintptr(wmInsight), 0, 0)
	}()
}

// refreshInsight is the async path behind the "刷新热门" button. It keeps
// the UI thread free (the old synchronous call froze the window for up to 20s).
func refreshInsight() {
	if insBusy {
		return
	}
	insBusy = true
	setText(hInfo, "（正在刷新…）")
	go func() {
		insText = insightInfo()
		insBusy = false
		pPostMessage.Call(hwndMain, uintptr(wmAppRefreshNow), 0, 0)
	}()
}

func loadDisk() {
	if dskBusy {
		return
	}
	dskBusy = true
	setText(hBody, "（正在扫描磁盘，可能需要几秒…）")
	go func() {
		dskBody = dirText()
		dskBusy = false
		pPostMessage.Call(hwndMain, uintptr(wmDisk), 0, 0)
	}()
}

// ---------- notifications: real sources (airing reminders + feed updates) ----------

// notifySeen reports whether a notification with this dedupe key was stored.
func notifySeen(key string) bool {
	recs, err := st.List("notif")
	if err != nil {
		return false
	}
	for _, r := range recs {
		if k, _ := r.Data["key"].(string); k == key {
			return true
		}
	}
	return false
}

// notifyPush stores one notification unless its dedupe key already exists.
func notifyPush(key, typ, title, body string, unix int64) {
	if key != "" && notifySeen(key) {
		return
	}
	data := map[string]interface{}{
		"title": title,
		"body":  body,
		"type":  typ,
		"read":  false,
		"unix":  float64(unix),
	}
	if key != "" {
		data["key"] = key
	}
	_, _ = st.Add("notif", data)
}

// collectAiringNotifs turns upcoming AniList episodes of tracked anime into
// notifications (deduped per episode). Non-blocking: runs in its own goroutine.
func collectAiringNotifs() {
	recs, err := st.List("anime")
	if err != nil {
		return
	}
	ids := make([]int, 0, len(recs))
	for _, r := range recs {
		if v, ok := r.Data["anilist_id"].(string); ok && v != "" {
			if n, e := strconv.Atoi(v); e == nil {
				ids = append(ids, n)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	ups, err := anime.Upcoming(ids)
	if err != nil {
		return
	}
	now := time.Now().Unix()
	for _, u := range ups {
		if u.AiringAt < now || u.AiringAt > now+7*86400 {
			continue // only the next 7 days
		}
		key := "airing-" + strconv.Itoa(u.MediaID) + "-" + strconv.Itoa(u.Episode)
		when := time.Unix(u.AiringAt, 0).Format("01-02 15:04")
		notifyPush(key, "追更", u.Title+" 第 "+strconv.Itoa(u.Episode)+" 集", when+" 播出", u.AiringAt)
	}
}

// collectFeedNotifs pulls every rss task from config.json and stores the
// latest items as notifications (deduped per item link).
func collectFeedNotifs() {
	if cfg == nil {
		return
	}
	for _, task := range cfg.Tasks {
		if task.Type != "rss" || task.URL == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		feed, err := rss.Fetch(ctx, task.URL, task.TimeoutSec)
		cancel()
		if err != nil || feed == nil {
			continue
		}
		limit := task.Limit
		if limit <= 0 || limit > 5 {
			limit = 3
		}
		now := time.Now().Unix()
		for i, it := range feed.Items {
			if i >= limit {
				break
			}
			key := it.ID
			if key == "" {
				key = it.Link
			}
			if key == "" {
				key = task.ID + "-" + it.Title
			}
			notifyPush("feed-"+key, "订阅", it.Title, feed.Title+" · 更新", now)
		}
	}
}

// collectNotifs runs both sources once per session (background, deduped).
var notifCollected bool

func collectNotifsOnce() {
	if notifCollected {
		return
	}
	notifCollected = true
	go func() {
		collectAiringNotifs()
		collectFeedNotifs()
	}()
}

// ---------- rss page (was a placeholder) ----------

func loadRSS() {
	if rssBusy {
		return
	}
	rssBusy = true
	go func() {
		var sb strings.Builder
		feeds := 0
		if cfg != nil {
			for _, task := range cfg.Tasks {
				if task.Type != "rss" || task.URL == "" {
					continue
				}
				feeds++
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				feed, err := rss.Fetch(ctx, task.URL, task.TimeoutSec)
				cancel()
				sb.WriteString("■ " + task.ID)
				if err == nil && feed != nil && feed.Title != "" {
					sb.WriteString(" · " + feed.Title)
				}
				sb.WriteString("\n")
				if err != nil {
					sb.WriteString("  （获取失败：" + err.Error() + "）\n\n")
					continue
				}
				limit := task.Limit
				if limit <= 0 || limit > 10 {
					limit = 8
				}
				for i, it := range feed.Items {
					if i >= limit {
						break
					}
					sb.WriteString("  · " + it.Title + "\n")
				}
				sb.WriteString("\n")
			}
		}
		if feeds == 0 {
			sb.WriteString("（未配置订阅源：在 config.json 的 tasks 中添加 type 为 rss 的条目，然后重启应用）")
		}
		rssText = strings.TrimRight(sb.String(), "\n")
		rssBusy = false
		pPostMessage.Call(hwndMain, uintptr(wmRss), 0, 0)
	}()
}

func diskText() string {
	var sb strings.Builder
	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range parts {
			if u, err := disk.Usage(p.Mountpoint); err == nil && u.Total > 0 {
				sb.WriteString(fmt.Sprintf("%s    %.1f%%    %s / %s\n", p.Mountpoint, u.UsedPercent, humanBytes(u.Used), humanBytes(u.Total)))
			}
		}
	}
	if sb.Len() == 0 {
		return "（未能获取磁盘信息）"
	}
	return strings.TrimRight(sb.String(), "\n")
}

func dirText() string {
	var sb strings.Builder
	sb.WriteString("本机磁盘:\n")
	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range parts {
			if u, err := disk.Usage(p.Mountpoint); err == nil && u.Total > 0 {
				sb.WriteString(fmt.Sprintf("  %s  %.1f%%  %s / %s\n", p.Mountpoint, u.UsedPercent, humanBytes(u.Used), humanBytes(u.Total)))
			}
		}
	}
	sb.WriteString("\n目录占用 (C:\\):\n")
	if items, err := du.Scan("C:\\", 12); err == nil {
		for _, it := range items {
			sb.WriteString(fmt.Sprintf("  %s  %s\n", it.Name, humanBytes(uint64(it.Size))))
		}
	} else {
		sb.WriteString("  （无法扫描: " + err.Error() + "）\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// --- KB text list (non-anime tabs) ---
var kbCols = []string{"anime", "books", "study", "games", "notes"}
var kbColLabels = map[string]string{"anime": "番剧", "books": "书库", "study": "学习", "games": "游戏", "notes": "笔记"}
var kbSecField = map[string]string{"anime": "status", "books": "author", "study": "status", "games": "platform", "notes": "tags"}

func kbText() string {
	recs, _ := st.List(kbCol)
	if len(recs) == 0 {
		return "（暂无条目，输入标题添加）"
	}
	var sb strings.Builder
	for _, r := range recs {
		title, _ := r.Data["title"].(string)
		sec, _ := r.Data[kbSecField[kbCol]].(string)
		line := "• " + title
		if sec != "" {
			line += "  [" + sec + "]"
		}
		sb.WriteString(line + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// --- cover cache & GDI bitmap helpers ---

func makeBitmap(src image.Image) (uintptr, int, int) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return 0, 0, 0
	}
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	var bi bitmapInfo
	bi.Size = 40
	bi.Width = int32(w)
	bi.Height = int32(-h) // top-down
	bi.Planes = 1
	bi.BitCount = 32
	bi.Compression = biRGB
	var bits *byte
	hbmp, _, _ := pCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bi)), dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbmp == 0 || bits == nil {
		return 0, w, h
	}
	dst := unsafe.Slice(bits, w*h*4)
	sp := rgba.Pix
	// BGRA order for 32bpp BI_RGB top-down
	for i := 0; i < w*h; i++ {
		j := i * 4
		dst[j] = sp[j+2]
		dst[j+1] = sp[j+1]
		dst[j+2] = sp[j]
		dst[j+3] = sp[j+3]
	}
	return hbmp, w, h
}

func loadCoverFile(path string) *covInfo {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	hbmp, w, h := makeBitmap(img)
	if hbmp == 0 {
		return nil
	}
	return &covInfo{hbmp: hbmp, w: w, h: h, loaded: true, path: path}
}

func ensureCover(id, url string) {
	if id == "" || url == "" {
		return
	}
	if v, ok := covers.Load(id); ok && v.(*covInfo).loaded {
		return
	}
	path := filepath.Join(coverDir, id+".img")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
		if ci := loadCoverFile(path); ci != nil {
			covers.Store(id, ci)
			return
		}
	}
	// mark pending to avoid duplicate downloads
	if _, loaded := covers.LoadOrStore(id, &covInfo{path: path}); loaded {
		return
	}
	go func() {
		defer func() {
			if v, ok := covers.Load(id); ok && !v.(*covInfo).loaded {
				// mark done (failed) so we don't retry forever on every paint
				// (still keeps placeholder)
			}
		}()
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			covers.Store(id, &covInfo{path: path})
			return
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(io.LimitReader(resp.Body, 8<<20)) // cap cover downloads at 8MB
		data := buf.Bytes()
		if len(data) < 64 {
			covers.Store(id, &covInfo{path: path})
			return
		}
		_ = os.MkdirAll(coverDir, 0o755)
		_ = os.WriteFile(path, data, 0o644)
		img, _, derr := image.Decode(bytes.NewReader(data))
		if derr != nil {
			covers.Store(id, &covInfo{path: path})
			return
		}
		hbmp, w, h := makeBitmap(img)
		if hbmp != 0 {
			covers.Store(id, &covInfo{hbmp: hbmp, w: w, h: h, loaded: true, path: path})
		} else {
			covers.Store(id, &covInfo{path: path})
		}
		pPostMessage.Call(hwndMain, uintptr(wmAppCover), 0, 0)
	}()
}

func getCover(id string) *covInfo {
	if v, ok := covers.Load(id); ok {
		return v.(*covInfo)
	}
	return nil
}

func drawStretch(dc uintptr, x, y, w, h int, ci *covInfo) {
	if ci == nil || ci.hbmp == 0 || w <= 0 || h <= 0 {
		return
	}
	mem, _, _ := pCreateCompatibleDC.Call(dc)
	pSelectObject.Call(mem, ci.hbmp)
	pSetStretchBltMode.Call(dc, colorOnColor)
	pStretchBlt.Call(dc, uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		mem, 0, 0, uintptr(ci.w), uintptr(ci.h), srcCopy)
	pDeleteDC.Call(mem)
}

func fillRectColor(dc uintptr, x, y, w, h int, rgb uintptr) {
	if w <= 0 || h <= 0 {
		return
	}
	rc := rect{Left: int32(x), Top: int32(y), Right: int32(x + w), Bottom: int32(y + h)}
	br, _, _ := pCreateSolidBrush.Call(rgb)
	pFillRect.Call(dc, uintptr(unsafe.Pointer(&rc)), br)
	pDeleteObject.Call(br)
}

func drawTextRect(dc uintptr, x, y, w, h int, text string, font uintptr, rgb uintptr, flags uintptr) {
	if w <= 0 || h <= 0 {
		return
	}
	if font != 0 {
		pSelectObject.Call(dc, font)
	}
	pSetBkMode.Call(dc, 1)
	pSetTextColor.Call(dc, rgb)
	rc := rect{Left: int32(x), Top: int32(y), Right: int32(x + w), Bottom: int32(y + h)}
	tp, _ := windows.UTF16PtrFromString(text)
	pDrawText.Call(dc, uintptr(unsafe.Pointer(tp)), uintptr(0xFFFFFFFF), uintptr(unsafe.Pointer(&rc)), flags)
}

// --- KB card mode helpers ---

func kbCardMode() bool { return page == "kb" && kbCol == "anime" }

func kbGeom() (cx, cw, top, bottom int) {
	w, h := clientSize()
	sidebarW := 320
	contentX := sidebarW + 30
	contentW := w - contentX - 30
	if contentW < 260 {
		contentW = 260
	}
	top = 240
	bottom = h - 36
	return contentX, contentW, top, bottom
}

func refreshKB() {
	recs, _ := st.List(kbCol)
	kbRecs = recs
	for _, r := range recs {
		if c, _ := r.Data["cover"].(string); c != "" {
			ensureCover(r.ID, c)
		}
	}
}

// clampKbScroll keeps the card wall from scrolling past the last card
// (previously it could scroll the whole grid out of view).
func clampKbScroll() {
	if len(kbCards) == 0 {
		kbScroll = 0
		return
	}
	maxBottom := 0
	for _, c := range kbCards {
		if b := c.y + c.h; b > maxBottom {
			maxBottom = b
		}
	}
	raw := maxBottom + kbScroll // content bottom without any scrolling
	_, _, _, bottom := kbGeom()
	if limit := raw - bottom + 40; limit > 0 {
		if kbScroll > limit {
			kbScroll = limit
		}
	} else {
		kbScroll = 0
	}
}

func kbs2cards() []kbCard {
	cx, cw, top, _ := kbGeom()
	if len(kbRecs) == 0 || cw <= 0 {
		return nil
	}
	const gap = 18
	const minW = 200
	cols := (cw + gap) / (minW + gap)
	if cols < 1 {
		cols = 1
	}
	cardW := (cw - (cols-1)*gap) / cols
	if cardW < 120 {
		cardW = 120
	}
	coverH := cardW * 14 / 10
	titleH := 70
	cardH := coverH + titleH
	out := make([]kbCard, 0, len(kbRecs))
	for i, r := range kbRecs {
		col := i % cols
		row := i / cols
		title, _ := r.Data["title"].(string)
		status, _ := r.Data["status"].(string)
		x := cx + col*(cardW+gap)
		y := top + row*(cardH+gap) - kbScroll
		out = append(out, kbCard{id: r.ID, title: title, status: status, x: x, y: y, w: cardW, h: cardH})
	}
	return out
}

func paintKBCards(dc uintptr) {
	cx, cw, top, bottom := kbGeom()
	fillRectColor(dc, cx, top, cw, bottom-top, colSide)
	if searchMode {
		paintSearchResults(dc)
		return
	}
	if len(kbRecs) == 0 {
		drawTextRect(dc, cx, top, cw, 60, "（暂无条目，输入标题点「＋ 添加」）", fontBody, colDim, dtLeft)
		return
	}
	kbCards = kbs2cards()
	for _, c := range kbCards {
		if c.y < top-160 || c.y > bottom {
			continue // offscreen
		}
		fill := uintptr(colCard)
		if hoverAct == "card" && hoverID == c.id {
			fill = colCard2
			fillRectColor(dc, c.x-2, c.y-2, c.w+4, c.h+4, colAcc) // accent rim
		}
		fillRectColor(dc, c.x, c.y, c.w, c.h, fill)
		coverH := c.w * 14 / 10
		ci := getCover(c.id)
		if ci != nil && ci.loaded {
			drawStretch(dc, c.x, c.y, c.w, coverH, ci)
		} else {
			fillRectColor(dc, c.x, c.y, c.w, coverH, colCard2)
			// cover placeholder
			drawTextRect(dc, c.x, c.y+coverH/2-30, c.w, 60, firstRune(c.title), fontTiny, colDim, dtCenter|dtVCenter)
		}
		ty := c.y + coverH
		drawTextRect(dc, c.x+6, ty+2, c.w-12, 40, c.title, fontCard, colFg, dtSingle)
		sc := statusColor(c.status)
		fillRectColor(dc, c.x+6, ty+44, c.w-12, 26, sc)
		drawTextRect(dc, c.x+6, ty+44, c.w-12, 26, c.status, fontTiny, colOnAcc, dtSingle|dtVCenter)
	}
}

func firstRune(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) == 0 {
		return "无图"
	}
	return string(r[0])
}

func statusColor(s string) uintptr {
	switch s {
	case "在看", "看过":
		return colAcc
	case "想追", "想看", "想玩", "规划中":
		return colAcc
	case "搁置", "弃":
		return colDim
	default:
		return colCard2
	}
}

// --- KB detail view ---

func curDetailRecord() *kb.Record {
	for i := range kbRecs {
		if kbRecs[i].ID == detailID {
			return &kbRecs[i]
		}
	}
	return nil
}

func paintKBDetail(dc uintptr) {
	r := curDetailRecord()
	cx, cw, top, bottom := kbGeom()
	detHits = nil
	fillRectColor(dc, cx, top, cw, bottom-top, colSide)
	if r == nil {
		drawTextRect(dc, cx, top, cw, 60, "（条目不存在或已删除）", fontBody, colDim, dtLeft)
		return
	}
	data := r.Data
	title, _ := data["title"].(string)
	status, _ := data["status"].(string)
	rate, _ := data["rate"].(float64)
	total := ""
	if tv, ok := data["total"].(float64); ok && tv > 0 {
		total = fmt.Sprintf("%v", tv)
	} else if tv, ok := data["total"].(int); ok && tv > 0 {
		total = fmt.Sprintf("%v", tv)
	}
	watched, _ := data["watched"].(string)
	note, _ := data["note"].(string)

	pad := 20
	lw := 220
	lh := 340
	if ci := getCover(r.ID); ci != nil && ci.loaded {
		drawStretch(dc, cx+pad, top+pad, lw, lh, ci)
	} else {
		fillRectColor(dc, cx+pad, top+pad, lw, lh, colCard2)
		drawTextRect(dc, cx+pad, top+pad, lw, lh, title, fontCard, colDim, dtCenter|dtVCenter)
	}
	ix := cx + pad + lw + 24
	iw := cw - pad - lw - 24 - pad
	if iw < 140 {
		iw = 140
	}
	drawTextRect(dc, ix, top+pad, iw, 52, title, fontTitle, colFg, dtWordBreak)
	sty := top + pad + 62
	drawTextRect(dc, ix, sty, 70, 38, "状态", fontNav, colDim, dtSingle|dtVCenter)
	sx := ix + 78
	for _, s := range []string{"想追", "在看", "看过", "搁置"} {
		w := 96
		sel := s == status
		sc := uintptr(colCard2)
		tc := uintptr(colFg)
		if sel {
			sc = colAcc
			tc = colOnAcc
		}
		fillRectColor(dc, sx, sty, w, 38, sc)
		drawTextRect(dc, sx, sty, w, 38, s, fontNav, tc, dtSingle|dtVCenter)
		detHits = append(detHits, detHit{sx, sty, w, 38, "status", s})
		sx += w + 10
	}
	my := sty + 56
	meta := "评分 " + fmt.Sprintf("%.1f", rate)
	if total != "" {
		meta += "    集数 " + total
	}
	if watched != "" {
		meta += "    已看 " + watched
	}
	drawTextRect(dc, ix, my, iw, 38, meta, fontNav, colDim, dtSingle|dtVCenter)
	ny := my + 52
	nh := bottom - ny - 116 // leave room for the link bar above the buttons
	if nh < 50 {
		nh = 50
	}
	if note != "" {
		drawTextRect(dc, ix, ny, iw, nh, note, fontBody, colFg, dtWordBreak)
	}
	// studios + main cast, each with a clickable favorite dot
	if detailInfo != nil && detailLoading != r.ID {
		if len(detailInfo.Studios) > 0 {
			sy := sty + 56
			drawTextRect(dc, ix, sy, 70, 30, "制作", fontNav, colDim, dtSingle|dtVCenter)
			sxx := ix + 78
			for _, s := range detailInfo.Studios {
				if sxx > cx+cw-120 {
					break
				}
				faved := favExists(s.Name)
				sc := uintptr(colCard2)
				if faved {
					sc = colAcc
				}
				fillRectColor(dc, sxx, sy, 30, 30, sc)
				drawTextRect(dc, sxx, sy, 30, 30, "★", fontNav, colOnAcc, dtCenter|dtVCenter)
				detHits = append(detHits, detHit{sxx, sy, 30, 30, "dettoggle", "studio|" + s.Name + "|" + strconv.Itoa(s.ID)})
				drawTextRect(dc, sxx+36, sy, 160, 30, s.Name, fontBody, colFg, dtSingle|dtVCenter)
				sxx += 200
			}
		}
		if len(detailInfo.Characters) > 0 {
			cy2 := sty + 96
			drawTextRect(dc, ix, cy2, 70, 30, "声优", fontNav, colDim, dtSingle|dtVCenter)
			cxx := ix + 78
			rows := 0
			line := 0
			for _, ch := range detailInfo.Characters {
				if len(ch.VAs) == 0 {
					continue
				}
				va := ch.VAs[0]
				faved := favExists(va.Name)
				sc := uintptr(colCard2)
				if faved {
					sc = colAcc
				}
				px := cxx + line*170
				if px+160 > cx+cw-20 {
					line = 0
					rows++
					px = cxx
				}
				py := cy2 + rows*34
				fillRectColor(dc, px, py, 30, 30, sc)
				drawTextRect(dc, px, py, 30, 30, "★", fontNav, colOnAcc, dtCenter|dtVCenter)
				detHits = append(detHits, detHit{px, py, 30, 30, "dettoggle", "cv|" + va.Name + "|" + strconv.Itoa(va.ID)})
				drawTextRect(dc, px+36, py, 130, 30, va.Name, fontBody, colFg, dtSingle|dtVCenter)
				line++
				if rows >= 4 {
					break
				}
			}
		}
	}
	by := bottom - 66
	bw := 140
	bh := 48
	// back
	backFill := uintptr(colCard2)
	if hoverAct == "back" {
		backFill = colAcc
	}
	fillRectColor(dc, cx+pad, by, bw, bh, backFill)
	drawTextRect(dc, cx+pad, by, bw, bh, "← 返回", fontNav, colFg, dtSingle|dtVCenter|dtCenter)
	detHits = append(detHits, detHit{cx + pad, by, bw, bh, "back", ""})
	// watch +1
	wx := cx + pad + bw + 12
	watchFill := uintptr(colAcc)
	watchTx := uintptr(colOnAcc)
	if hoverAct == "watch" {
		watchFill = colFg
		watchTx = colBg
	}
	fillRectColor(dc, wx, by, bw, bh, watchFill)
	drawTextRect(dc, wx, by, bw, bh, "▶ 看一集 +1", fontNav, watchTx, dtSingle|dtVCenter|dtCenter)
	detHits = append(detHits, detHit{wx, by, bw, bh, "watch", r.ID})
	// delete
	dx := cx + cw - pad - bw
	delFill := uintptr(colRed)
	if hoverAct == "delete" {
		delFill = 0x0000D0 // brighten on hover
	}
	fillRectColor(dc, dx, by, bw, bh, delFill)
	drawTextRect(dc, dx, by, bw, bh, "删除", fontNav, colFg, dtSingle|dtVCenter|dtCenter)
	detHits = append(detHits, detHit{dx, by, bw, bh, "delete", r.ID})
	if link, _ := data["link"].(string); link != "" {
		// draw a real, clickable-looking link bar at the bottom of the info column
		lw2 := iw
		if lw2 > bw {
			lw2 = bw
		}
		drawTextRect(dc, ix, by-34, lw2, 34, "链接: "+link, fontTiny, colAcc, dtSingle|dtVCenter)
		detHits = append(detHits, detHit{ix, by - 34, lw2, 34, "openlink", link})
	}
}

// --- KB mutations ---

func recByID(id string) *kb.Record {
	for i := range kbRecs {
		if kbRecs[i].ID == id {
			return &kbRecs[i]
		}
	}
	return nil
}

func copyMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func kbReload() {
	refreshKB()
	pInvalidateRect.Call(hwndMain, 0, 1)
}

func kbSetStatus(id, status string) {
	rec := recByID(id)
	if rec == nil {
		return
	}
	d := copyMap(rec.Data)
	d["status"] = status
	_, _ = st.Update(kbCol, id, d)
	kbReload()
}

func kbWatchInc(id string) {
	rec := recByID(id)
	if rec == nil {
		return
	}
	d := copyMap(rec.Data)
	w := 0
	switch cur := d["watched"].(type) {
	case float64:
		w = int(cur)
	case int:
		w = cur
	case string:
		fmt.Sscanf(cur, "%d", &w)
	}
	w++
	d["watched"] = fmt.Sprintf("%d", w)
	_, _ = st.Update(kbCol, id, d)
	kbReload()
}

func kbDelete(id string) {
	if !confirmBox("确定删除该条目？删除后无法恢复。", "删除确认") {
		return
	}
	_ = st.Delete(kbCol, id)
	detailID = ""
	kbReload()
}

func kbAdd() {
	title := strings.TrimSpace(getText(hKbToA))
	if title == "" {
		return
	}
	data := map[string]interface{}{"title": title}
	switch kbCol {
	case "anime":
		data["status"] = "想追"
	case "study":
		data["status"] = "规划中"
	case "games":
		data["status"] = "想玩"
	}
	rec, _ := st.Add(kbCol, data)
	setText(hKbToA, "")
	if kbCardMode() {
		refreshKB()
		if kbCol == "anime" {
			bgmCoverAsync(rec.ID, title)
		}
		pInvalidateRect.Call(hwndMain, 0, 1)
		return
	}
	setText(hBody, kbText())
}

// bgmCoverAsync looks up Chinese metadata + a cover for a newly-added anime and
// stores it back, then refreshes the grid.
func bgmCoverAsync(id, title string) {
	go func() {
		res, err := anime.BangumiSearch(title)
		if err != nil || len(res) == 0 {
			return
		}
		best := res[0]
		data := map[string]interface{}{
			"title":  title,
			"status": "想追",
			"cover":  best.Cover,
			"link":   best.URL,
		}
		if best.Score > 0 {
			data["rate"] = best.Score
		}
		if rec := recByID(id); rec != nil {
			for k, v := range rec.Data {
				if _, ok := data[k]; !ok {
					data[k] = v
				}
			}
		}
		_, _ = st.Update("anime", id, data)
		refreshKB()
		pPostMessage.Call(hwndMain, uintptr(wmAppRefresh), 0, 0)
	}()
}

// fetchDetailAsync pulls studios/cast from AniList for the record being viewed.
func fetchDetailAsync(id string) {
	rec := recByID(id)
	if rec == nil {
		return
	}
	v, _ := rec.Data["anilist_id"].(string)
	if v == "" {
		return // only AniList-backed records carry full cast info
	}
	alID, _ := strconv.Atoi(v)
	if alID == 0 || detailBusy {
		return
	}
	detailBusy = true
	detailLoading = id
	go func() {
		d, err := anime.GetDetail(alID)
		if err == nil && detailLoading == id {
			detailInfo = &d
		}
		detailBusy = false
		pPostMessage.Call(hwndMain, uintptr(wmDetail), 0, 0)
	}()
}

// favExists reports whether this studio/cv is already favorited.
func favExists(name string) bool {
	recs, _ := st.List("favs")
	for _, r := range recs {
		if n, _ := r.Data["name"].(string); n == name {
			return true
		}
	}
	return false
}

// favToggle adds or removes a studio/cast favorite; al id links back to works.
func favToggle(name, typ string, alID int) {
	recs, _ := st.List("favs")
	for _, r := range recs {
		if n, _ := r.Data["name"].(string); n == name {
			_ = st.Delete("favs", r.ID)
			return
		}
	}
	_, _ = st.Add("favs", map[string]interface{}{
		"name":  name,
		"type":  typ,
		"al_id": float64(alID),
	})
}

// hitAt resolves the custom-drawn region under the client point (kb first,
// then generic lists) — mirrors the click handlers.
func hitAt(x, y int) (string, string) {
	if kbCardMode() {
		if h := hitTestKB(x, y); h != "" {
			p := strings.SplitN(h, "|", 2)
			if len(p) == 2 {
				return p[0], p[1]
			}
			return p[0], ""
		}
		return "", ""
	}
	if listMode() {
		if h := hitTestList(x, y); h != "" {
			p := strings.SplitN(h, "|", 2)
			if len(p) == 2 {
				return p[0], p[1]
			}
			return p[0], ""
		}
	}
	return "", ""
}

// trackHover asks for WM_MOUSELEAVE so the hover state can be cleared.
func trackHover(hwnd uintptr) {
	if hoverTrk {
		return
	}
	type tme struct {
		cbSize  uint32
		dwFlags uint32
		hwnd    uintptr
	}
	ev := tme{cbSize: 24, dwFlags: 0x00000002, hwnd: hwnd} // TME_LEAVE
	pTrackMouseEvent.Call(uintptr(unsafe.Pointer(&ev)))
	hoverTrk = true
}

// updateHover refreshes hover state + hand cursor; true when repaint needed.
func updateHover(x, y int) bool {
	action, id := hitAt(x, y)
	if action != "" && curHand != 0 {
		pSetCursor.Call(curHand)
	}
	changed := action != hoverAct || id != hoverID
	hoverAct, hoverID = action, id
	return changed
}

// ---------- hover + cursor management (custom-drawn buttons) ----------

func onKBHit(action, id string) {
	switch action {
	case "card":
		detailID = id
		detailInfo = nil
		pInvalidateRect.Call(hwndMain, 0, 1)
		fetchDetailAsync(id)
	case "back":
		detailID = ""
		pInvalidateRect.Call(hwndMain, 0, 1)
	case "delete":
		kbDelete(id)
	case "watch":
		kbWatchInc(id)
	case "status":
		kbSetStatus(detailID, id)
	case "dettoggle":
		// id = "<type>|<name>|<alid>"
		p := strings.SplitN(id, "|", 3)
		if len(p) == 3 {
			al, _ := strconv.Atoi(p[2])
			favToggle(p[1], p[0], al)
			pInvalidateRect.Call(hwndMain, 0, 1)
		}
	case "searchcancel":
		cancelSearch()
	case "openlink":
		openURL(id)
	case "seadd":
		if n, err := strconv.Atoi(id); err == nil {
			addAnimeFromSearch(n)
		}
	}
}

// --- anime search-and-pick ---

func runAnimeSearch() {
	q := strings.TrimSpace(getText(hKbToA))
	if q == "" || searchBusy {
		return
	}
	searchBusy = true
	searchQuery = q
	searchMode = true
	searchResults = nil
	pInvalidateRect.Call(hwndMain, 0, 1)
	go func() {
		res, err := anime.Search(q)
		if err == nil {
			searchResults = res
		}
		for _, r := range res {
			if r.Cover != "" {
				ensureCover("sfv"+strconv.Itoa(r.ID), r.Cover)
			}
		}
		searchBusy = false
		pPostMessage.Call(hwndMain, uintptr(wmSearchDone), 0, 0)
	}()
}

func cancelSearch() {
	searchMode = false
	searchResults = nil
	pInvalidateRect.Call(hwndMain, 0, 1)
}

func addAnimeFromSearch(idx int) {
	if idx < 0 || idx >= len(searchResults) {
		return
	}
	r := searchResults[idx]
	data := map[string]interface{}{
		"title":      r.Title,
		"status":     "想追",
		"anilist_id": strconv.Itoa(r.ID),
		"cover":      r.Cover,
		"link":       r.URL,
		"rate":       r.Score,
		"air_start":  r.StartDate,
		"note":       r.Synopsis,
	}
	if r.Episodes != nil {
		data["total"] = *r.Episodes
	}
	rec, _ := st.Add("anime", data)
	if rec.ID != "" && r.Cover != "" {
		ensureCover(rec.ID, r.Cover)
	}
	searchMode = false
	searchResults = nil
	setText(hKbToA, "")
	refreshKB()
	pInvalidateRect.Call(hwndMain, 0, 1)
}

func paintSearchResults(dc uintptr) {
	cx, cw, top, bottom := kbGeom()
	fillRectColor(dc, cx, top, cw, bottom-top, colSide)
	detHits = nil
	bw, bh := 150, 46
	fillRectColor(dc, cx+16, top+16, bw, bh, colCard2)
	drawTextRect(dc, cx+16, top+16, bw, bh, "✕ 取消搜索", fontNav, colFg, dtSingle|dtVCenter|dtCenter)
	detHits = append(detHits, detHit{cx + 16, top + 16, bw, bh, "searchcancel", ""})
	drawTextRect(dc, cx+16+bw+16, top+16, cw-bw-16-32, bh, "搜索: "+searchQuery, fontTitle, colFg, dtSingle|dtVCenter)
	gy := top + 80
	if searchBusy {
		drawTextRect(dc, cx+16, gy, cw-32, 40, "（正在搜索…）", fontBody, colDim, dtLeft)
		return
	}
	if len(searchResults) == 0 {
		drawTextRect(dc, cx+16, gy, cw-32, 40, "（未找到结果，试试英文名）", fontBody, colDim, dtLeft)
		return
	}
	const gap = 16
	colW := 180
	cols := (cw - 32 + gap) / (colW + gap)
	if cols < 1 {
		cols = 1
	}
	wW := (cw - 32 - (cols-1)*gap) / cols
	if wW < 100 {
		wW = 100
	}
	coverH := wW * 14 / 10
	cardH := coverH + 68
	for i, r := range searchResults {
		col := i % cols
		row := i / cols
		x := cx + 16 + col*(wW+gap)
		y := gy + row*(cardH+gap)
		if y+cardH > bottom {
			break
		}
		fillRectColor(dc, x, y, wW, cardH, colCard)
		ci := getCover("sfv" + strconv.Itoa(r.ID))
		if ci != nil && ci.loaded {
			drawStretch(dc, x, y, wW, coverH, ci)
		} else {
			fillRectColor(dc, x, y, wW, coverH, colCard2)
			drawTextRect(dc, x, y+coverH/2-24, wW, 48, r.Title, fontTiny, colDim, dtCenter|dtVCenter)
		}
		meta := fmt.Sprintf("%.1f", r.Score)
		if r.Year > 0 {
			meta += " · " + fmt.Sprintf("%d", r.Year)
		}
		drawTextRect(dc, x+6, y+coverH+2, wW-12, 28, meta, fontTiny, colAcc, dtSingle)
		drawTextRect(dc, x+6, y+coverH+30, wW-12, 36, r.Title, fontTiny, colFg, dtWordBreak)
		detHits = append(detHits, detHit{x, y, wW, cardH, "seadd", strconv.Itoa(i)})
	}
}

// --- generic themed list page (favs / notify / rules) ---

func listMode() bool { return page == "notify" || page == "favs" || page == "rules" }

func listColl() string {
	if listPage == "notify" {
		return "notif"
	}
	return listPage
}

func findRec(coll, id string) *kb.Record {
	recs, _ := st.List(coll)
	for i := range recs {
		if recs[i].ID == id {
			return &recs[i]
		}
	}
	return nil
}

func refreshList() {
	recs, _ := st.List(listColl())
	listRows = listRows[:0]
	listHits = listHits[:0]
	listAct = false
	switch listPage {
	case "notify":
		sort.Slice(recs, func(i, j int) bool {
			u1, _ := recs[i].Data["unix"].(float64)
			u2, _ := recs[j].Data["unix"].(float64)
			return u1 > u2
		})
		for _, r := range recs {
			title, _ := r.Data["title"].(string)
			body, _ := r.Data["body"].(string)
			typ, _ := r.Data["type"].(string)
			read, _ := r.Data["read"].(bool)
			listRows = append(listRows, listRow{id: r.ID, title: title, sub: body, tag: typ, accent: !read})
		}
		listAct = true
		listActL = "全部已读"
	case "favs":
		for _, r := range recs {
			name, _ := r.Data["name"].(string)
			typ, _ := r.Data["type"].(string)
			tag := typ
			if typ == "studio" {
				tag = "公司"
			} else if typ == "cv" {
				tag = "声优"
			}
			listRows = append(listRows, listRow{id: r.ID, title: name, sub: typ, tag: tag})
		}
	case "rules":
		for _, r := range recs {
			title, _ := r.Data["title"].(string)
			listRows = append(listRows, listRow{id: r.ID, title: title})
		}
	}
}

func paintListPage(dc uintptr) {
	cx, cw, top, bottom := kbGeom()
	fillRectColor(dc, cx, top, cw, bottom-top, colSide)
	if listPage == "favs" && favDetailID != "" {
		paintFavWorks(dc)
		return
	}
	listHits = listHits[:0]
	// toolbar row (action button top-right)
	hy := top + 8
	if listAct {
		aw, ah := 110, 34
		ax := cx + cw - aw - 8
		fillRectColor(dc, ax, hy, aw, ah, colAcc)
		drawTextRect(dc, ax, hy, aw, ah, listActL, fontNav, colOnAcc, dtSingle|dtVCenter|dtCenter)
		listHits = append(listHits, detHit{ax, hy, aw, ah, "listaction", ""})
	}
	ry := top + 60
	rh := 88
	gap := 12
	if len(listRows) == 0 {
		msg := "（暂无条目）"
		switch listPage {
		case "notify":
			msg = "（暂无通知）"
		case "favs":
			msg = "（暂无收藏，去番剧详情点亮 ☆）"
		case "rules":
			msg = "（暂无规则）"
		}
		drawTextRect(dc, cx+12, ry, cw-24, 40, msg, fontBody, colDim, dtLeft)
		return
	}
	totalH := len(listRows)*(rh+gap)
	if listScroll > totalH-(bottom-ry) && totalH > (bottom-ry) {
		listScroll = totalH - (bottom - ry)
	}
	if listScroll < 0 {
		listScroll = 0
	}
	for i, row := range listRows {
		y := ry + i*(rh+gap) - listScroll
		if y > bottom {
			break
		}
		if y+rh < top {
			continue
		}
		bg := uintptr(colCard)
		if row.accent {
			bg = colCard2
		}
		fillRectColor(dc, cx+12, y, cw-24, rh, bg)
		if row.accent {
			fillRectColor(dc, cx+12, y, 4, rh, colAcc)
		}
		tx := cx + 24
		if row.tag != "" {
			tw := 64
			fillRectColor(dc, cx+24, y+10, tw, 26, colAcc)
			drawTextRect(dc, cx+24, y+10, tw, 26, row.tag, fontTiny, colOnAcc, dtSingle|dtVCenter|dtCenter)
			tx = cx + 24 + 72
		}
		rightPad := cw - 12 - (tx - cx) - 12
		if rightPad < 20 {
			rightPad = 20
		}
		drawTextRect(dc, tx, y+8, rightPad, 30, row.title, fontCard, colFg, dtSingle)
		drawTextRect(dc, tx, y+40, rightPad, 24, row.sub, fontBody, colDim, dtSingle)
		listHits = append(listHits, detHit{cx + 12, y, cw - 24, rh, "row", row.id})
	}
}

func hitTestList(x, y int) string {
	for _, h := range listHits {
		if x >= h.x && x < h.x+h.w && y >= h.y && y < h.y+h.h {
			return h.action + "|" + h.id
		}
	}
	return ""
}

func onListHit(action, id string) {
	switch action {
	case "listaction":
		if listPage == "notify" {
			for _, r := range listRows {
				if rec := findRec("notif", r.id); rec != nil {
					d := copyMap(rec.Data)
					d["read"] = true
					_, _ = st.Update("notif", r.id, d)
				}
			}
			refreshList()
			pInvalidateRect.Call(hwndMain, 0, 1)
		}
	case "row":
		if listPage == "notify" {
			if rec := findRec("notif", id); rec != nil {
				d := copyMap(rec.Data)
				d["read"] = true
				_, _ = st.Update("notif", id, d)
			}
			refreshList()
			pInvalidateRect.Call(hwndMain, 0, 1)
		} else if listPage == "favs" {
			favDetailID = id
			favWorks = nil
			favEntName, favEntType, favEntImage = "", "", ""
			loadFavWorks()
			pInvalidateRect.Call(hwndMain, 0, 1)
		}
	case "favback":
		favDetailID = ""
		pInvalidateRect.Call(hwndMain, 0, 1)
	case "favdel":
		favDelete(id)
	}
}

// --- favorites works view ---

func favAlID(rec *kb.Record) int {
	switch v := rec.Data["al_id"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func loadFavWorks() {
	if favBusy {
		return
	}
	rec := findRec("favs", favDetailID)
	if rec == nil {
		return
	}
	favBusy = true
	favEntName, _ = rec.Data["name"].(string)
	favEntType, _ = rec.Data["type"].(string)
	if fp, ok := rec.Data["image"].(string); ok {
		favEntImage = fp
	}
	go func() {
		id := favAlID(rec)
		if favEntType == "cv" || favEntType == "staff" {
			if w, err := anime.GetStaff(id); err == nil {
				favEntImage = w.Image
				favWorks = w.Media
			}
		} else {
			if w, err := anime.GetStudio(id); err == nil {
				favEntImage = w.Image
				favWorks = w.Media
			}
		}
		for _, m := range favWorks {
			if m.Cover != "" {
				ensureCover("fvw"+strconv.Itoa(m.ID), m.Cover)
			}
		}
		favBusy = false
		pPostMessage.Call(hwndMain, uintptr(wmFavWorks), 0, 0)
	}()
}

func favDelete(id string) {
	if !confirmBox("确定删除该收藏？", "删除确认") {
		return
	}
	_ = st.Delete("favs", id)
	favDetailID = ""
	refreshList()
	pInvalidateRect.Call(hwndMain, 0, 1)
}

func paintFavWorks(dc uintptr) {
	cx, cw, top, bottom := kbGeom()
	fillRectColor(dc, cx, top, cw, bottom-top, colSide)
	listHits = nil
	bw, bh := 110, 44
	fillRectColor(dc, cx+16, top+16, bw, bh, colCard2)
	drawTextRect(dc, cx+16, top+16, bw, bh, "← 返回", fontNav, colFg, dtSingle|dtVCenter|dtCenter)
	listHits = append(listHits, detHit{cx + 16, top + 16, bw, bh, "favback", ""})
	dw := 110
	dx := cx + cw - 16 - dw
	fillRectColor(dc, dx, top+16, dw, bh, colRed)
	drawTextRect(dc, dx, top+16, dw, bh, "删除", fontNav, colFg, dtSingle|dtVCenter|dtCenter)
	listHits = append(listHits, detHit{dx, top + 16, dw, bh, "favdel", favDetailID})
	drawTextRect(dc, cx+16+bw+12, top+16, cw-bw-dw-12-32, bh, favEntName, fontTitle, colFg, dtSingle|dtVCenter)
	gy := top + 76
	drawTextRect(dc, cx+16, gy, cw-32, 30, "作品 ("+fmt.Sprintf("%d", len(favWorks))+")", fontNav, colDim, dtSingle)
	gy += 40
	if favBusy {
		drawTextRect(dc, cx+16, gy, cw-32, 40, "（正在获取作品…）", fontBody, colDim, dtLeft)
		return
	}
	if len(favWorks) == 0 {
		drawTextRect(dc, cx+16, gy, cw-32, 40, "（暂无作品数据）", fontBody, colDim, dtLeft)
		return
	}
	const gap = 16
	colW := 150
	cols := (cw - 32 + gap) / (colW + gap)
	if cols < 1 {
		cols = 1
	}
	wW := (cw - 32 - (cols-1)*gap) / cols
	if wW < 90 {
		wW = 90
	}
	coverH := wW * 14 / 10
	cardH := coverH + 54
	for i, m := range favWorks {
		col := i % cols
		row := i / cols
		x := cx + 16 + col*(wW+gap)
		y := gy + row*(cardH+gap)
		if y+cardH > bottom {
			break
		}
		fillRectColor(dc, x, y, wW, cardH, colCard)
		ci := getCover("fvw" + strconv.Itoa(m.ID))
		if ci != nil && ci.loaded {
			drawStretch(dc, x, y, wW, coverH, ci)
		} else {
			fillRectColor(dc, x, y, wW, coverH, colCard2)
			drawTextRect(dc, x, y+coverH/2-24, wW, 48, m.Title, fontTiny, colDim, dtCenter|dtVCenter)
		}
		drawTextRect(dc, x+6, y+coverH+2, wW-12, 52, m.Title, fontTiny, colFg, dtWordBreak)
	}
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

func moveWin(h uintptr, x, y, w, hh int) {
	pMoveWindow.Call(h, uintptr(x), uintptr(y), uintptr(w), uintptr(hh), 1)
}

// relayout positions all controls based on the current client size.
func relayout() {
	var rc rect
	pGetClientRect.Call(hwndMain, uintptr(unsafe.Pointer(&rc)))
	w, h := int(rc.Right), int(rc.Bottom)
	if w <= 0 || h <= 0 {
		return
	}
	sidebarW := 320
	contentX := sidebarW + 30
	contentW := w - contentX - 30
	if contentW < 260 {
		contentW = 260
	}
	moveWin(hBrand, 30, 30, sidebarW-50, 52)
	moveWin(hTag, 30, 94, sidebarW-50, 32)
	navH := 58
	for i := range pages {
		moveWin(hNav[i], 26, 132+i*navH, sidebarW-54, navH-6)
	}
	moveWin(hTitle, contentX, 30, contentW, 54)
	cardGap := 18
	cardW := (contentW - 3*cardGap) / 4
	if cardW < 120 {
		cardW = 120
	}
	cardH := 185
	for i := 0; i < 4; i++ {
		moveWin(hCards[i], contentX+i*(cardW+cardGap), 106, cardW, cardH)
	}
	bodyY := 106 + cardH + 30
	bodyH := h - bodyY - 34
	if bodyH < 60 {
		bodyH = 60
	}
	moveWin(hBody, contentX, bodyY, contentW, bodyH)
	// insight controls
	platGap := 8
	platW := (contentW - 3*platGap) / 4
	if platW < 120 {
		platW = 120
	}
	for i := 0; i < 4; i++ {
		moveWin(hPlat[i], contentX+i*(platW+platGap), 106, platW, 52)
	}
	moveWin(hAcc, contentX, 182, 460, 42)
	moveWin(hPass, contentX+480, 182, 460, 42)
	moveWin(hSave, contentX+960, 180, 140, 46)
	moveWin(hReff, contentX, 246, 140, 44)
	moveWin(hReffMine, contentX+150, 246, 140, 44)
	moveWin(hHint, contentX+150, 248, contentW-150, 36)
	moveWin(hInfo, contentX, 306, contentW, h-306-34)
	moveWin(hAuto, contentX, 106, 300, 50)
	moveWin(hAutoSave, contentX+310, 104, 130, 52)
	kbgap := 8
	kbw := (contentW - 4*kbgap) / 5
	if kbw < 110 {
		kbw = 110
	}
	for i := range kbCols {
		moveWin(hKbTab[i], contentX+i*(kbw+kbgap), 106, kbw, 52)
	}
	moveWin(hKbToA, contentX, 182, 560, 44)
	moveWin(hKbAddBtn, contentX+570, 178, 150, 48)
	moveWin(hKbSearchBtn, contentX+730, 178, 180, 48)
	kbScroll = 0
	if kbCardMode() {
		refreshKB()
	}
	pInvalidateRect.Call(hwndMain, 0, 1)
}

func renderPage() {
	title := pageLabels[page]
	overview := page == "overview"
	insight := page == "insight"
	kbon := page == "kb"
	setSet := page == "settings"
	for i := range hCards {
		pShowWindow.Call(hCards[i], pBool(overview))
	}
	for i := range hPlat {
		pShowWindow.Call(hPlat[i], pBool(insight))
	}
	pShowWindow.Call(hAcc, pBool(insight))
	pShowWindow.Call(hPass, pBool(insight))
	pShowWindow.Call(hSave, pBool(insight))
	pShowWindow.Call(hReff, pBool(insight))
	pShowWindow.Call(hReffMine, pBool(insight))
	pShowWindow.Call(hHint, pBool(insight))
	pShowWindow.Call(hInfo, pBool(insight))
	pShowWindow.Call(hAuto, pBool(setSet))
	pShowWindow.Call(hAutoSave, pBool(setSet))
	for i := range kbCols {
		pShowWindow.Call(hKbTab[i], pBool(kbon))
	}
	pShowWindow.Call(hKbToA, pBool(kbon))
	pShowWindow.Call(hKbAddBtn, pBool(kbon))
	pShowWindow.Call(hKbSearchBtn, pBool(kbon))

	cm := kbCardMode()
	lm := listMode()
	pShowWindow.Call(hBody, pBool(!insight && !setSet && !cm && !lm))
	if cm || lm {
		setText(hBody, "")
	}

	var body string
	switch {
	case overview:
		loadOverview()
		body = "（正在获取系统信息…）"
	case insight:
		loadBind()
		loadInsight()
	case setSet:
		pSendMessage.Call(hAuto, 0x00F1, pBool(settings.Load(dataDir).AutoStart), 0) // BM_SETCHECK
		body = "设置：\n\n（设置页其余选项后续接入）"
	case kbon:
		if cm {
			refreshKB()
			pInvalidateRect.Call(hwndMain, 0, 1)
		} else {
			body = kbText()
		}
	case page == "disk":
		loadDisk()
		body = "（正在扫描磁盘…）"
	case page == "rss":
		loadRSS()
		body = "（正在获取订阅…）"
	case page == "notify":
		collectNotifsOnce()
	case lm:
		listPage = page
		listScroll = 0
		favDetailID = ""
		refreshList()
		pInvalidateRect.Call(hwndMain, 0, 1)
	default:
		body = "「" + pageLabels[page] + "」页面移植中，将逐个接入后台数据。"
	}
	setText(hTitle, title)
	if !cm && !lm {
		setText(hBody, body)
	}
}

// ---------- message handling ----------

func mouseXY(lParam uintptr) (int, int) {
	x := int(int16(uint16(lParam & 0xFFFF)))
	y := int(int16(uint16((lParam >> 16) & 0xFFFF)))
	return x, y
}

func hitTestKB(x, y int) string {
	if !kbCardMode() {
		return ""
	}
	if searchMode {
		for _, h := range detHits {
			if x >= h.x && x < h.x+h.w && y >= h.y && y < h.y+h.h {
				return h.action + "|" + h.id
			}
		}
		return ""
	}
	if detailID != "" {
		for _, h := range detHits {
			if x >= h.x && x < h.x+h.w && y >= h.y && y < h.y+h.h {
				return h.action + "|" + h.id
			}
		}
		return ""
	}
	kbCards = kbs2cards()
	for _, c := range kbCards {
		if x >= c.x && x < c.x+c.w && y >= c.y && y < c.y+c.h {
			return "card|" + c.id
		}
	}
	return ""
}

func paintFragment(dc uintptr) {
	if kbCardMode() {
		if detailID != "" {
			paintKBDetail(dc)
		} else {
			paintKBCards(dc)
		}
		return
	}
	if listMode() {
		paintListPage(dc)
	}
}

func wndProcMain(hwnd uintptr, msg uint32, wParam uintptr, lParam uintptr) uintptr {
	switch msg {
	case 0x0111: // WM_COMMAND
		id := uintptr(0xFFFF) & wParam
		if id >= navBase && id < uintptr(navBase+len(pages)) {
			page = pages[id-navBase]
			detailID = ""
			favDetailID = ""
			searchMode = false
			highlightNav()
			renderPage()
			pInvalidateRect.Call(hwndMain, 0, 1)
			return 0
		}
		if id >= IDPlat && id < IDPlat+4 {
			curPlat = int(id - IDPlat)
			loadBind()
			return 0
		}
		if id == IDSave {
			// GitHub tab: verify via API first; other tabs stay local-only saves
			if curPlat == 0 {
				verifyBind(getText(hPass))
			} else {
				saveBind()
			}
			return 0
		}
		if id == IDReff {
			refreshInsight()
			return 0
		}
		if id == IDReffMine {
			refreshMyRepos()
			return 0
		}
		if id == IDSaveS {
			on := uintptr(0)
			r, _, _ := pSendMessage.Call(hAuto, 0x00F0, 0, 0) // BM_GETCHECK
			if r == 1 {
				on = 1
			}
			stt := settings.Load(dataDir)
			stt.AutoStart = on == 1
			exe, _ := os.Executable()
			_ = settings.SetAutoStart(stt.AutoStart, exe)
			_ = settings.Save(dataDir, stt)
			pInvalidateRect.Call(hwndMain, 0, 1)
			return 0
		}
		if id >= KBTab && id < uintptr(KBTab+5) {
			kbCol = kbCols[id-KBTab]
			detailID = ""
			kbScroll = 0
			searchMode = false
			if kbCardMode() {
				refreshKB()
				pInvalidateRect.Call(hwndMain, 0, 1)
			} else {
				setText(hBody, kbText())
			}
			return 0
		}
		if id == KBAdd {
			kbAdd()
			return 0
		}
		if id == KBSearch {
			runAnimeSearch()
			return 0
		}
	case 0x0202: // WM_LBUTTONUP
		if kbCardMode() {
			x, y := mouseXY(lParam)
			if h := hitTestKB(x, y); h != "" {
				parts := strings.SplitN(h, "|", 2)
				id := ""
				if len(parts) == 2 {
					id = parts[1]
				}
				onKBHit(parts[0], id)
				return 0
			}
		}
		if listMode() {
			x, y := mouseXY(lParam)
			if h := hitTestList(x, y); h != "" {
				parts := strings.SplitN(h, "|", 2)
				id := ""
				if len(parts) == 2 {
					id = parts[1]
				}
				onListHit(parts[0], id)
				return 0
			}
		}
	case 0x0200: // WM_MOUSEMOVE
		x, y := mouseXY(lParam)
		trackHover(hwnd)
		if updateHover(x, y) {
			pInvalidateRect.Call(hwnd, 0, 1)
		}
		return 0
	case 0x02A2: // WM_MOUSELEAVE
		hoverTrk = false
		if hoverAct != "" {
			hoverAct, hoverID = "", ""
			pInvalidateRect.Call(hwnd, 0, 1)
		}
		return 0
	case 0x020A: // WM_MOUSEWHEEL
		if kbCardMode() && detailID == "" && !searchMode {
			delta := int(int16(uint16((lParam >> 16) & 0xFFFF)))
			wheelAccum += delta
			step := 0
			for wheelAccum <= -120 {
				step += 90
				wheelAccum += 120
			}
			for wheelAccum >= 120 {
				step -= 90
				wheelAccum -= 120
			}
			kbScroll -= step
			if kbScroll < 0 {
				kbScroll = 0
			}
			kbCards = kbs2cards()
			clampKbScroll()
			pInvalidateRect.Call(hwndMain, 0, 1)
			return 0
		}
		if listMode() {
			delta := int(int16(uint16((lParam >> 16) & 0xFFFF)))
			listScroll -= delta / 120 * 90
			if listScroll < 0 {
				listScroll = 0
			}
			pInvalidateRect.Call(hwndMain, 0, 1)
			return 0
		}
	case 0x0113: // WM_TIMER
		if wParam == 1 && page == "overview" {
			loadOverview() // periodic refresh; guards itself on ovBusy
		}
		return 0
	case 0x000F: // WM_PAINT
		var ps paintStruct
		dc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if dc != 0 {
			w, h := clientSize()
			// draw into a memory DC, then blit once — kills repaint flicker
			mem, _, _ := pCreateCompatibleDC.Call(dc)
			if mem != 0 {
				var bi bitmapInfo
				bi.Size = 40
				bi.Width = int32(w)
				bi.Height = int32(-h)
				bi.Planes = 1
				bi.BitCount = 32
				bi.Compression = biRGB
				var bits *byte
				bmp, _, _ := pCreateDIBSection.Call(mem, uintptr(unsafe.Pointer(&bi)), dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
				if bmp != 0 && bits != nil {
					oldBmp, _, _ := pSelectObject.Call(mem, bmp)
					fillRectColor(mem, 0, 0, w, h, colBg)
					sidebarW := 320
					if w > sidebarW {
						fillRectColor(mem, 0, 0, sidebarW, h, colSide)
					}
					paintFragment(mem)
					pBitBlt.Call(dc, 0, 0, uintptr(w), uintptr(h), mem, 0, 0, srcCopy)
					pSelectObject.Call(mem, oldBmp)
					pDeleteObject.Call(bmp)
				}
				pDeleteDC.Call(mem)
			}
			pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		}
		return 0
	case wmAppCover, wmAppRefresh:
		// covers / data changed -> repaint
		pInvalidateRect.Call(hwnd, 0, 1)
		return 0
	case wmOverview:
		if page == "overview" {
			setText(hCards[0], "CPU:\n"+ovStat[0])
			setText(hCards[1], "内存:\n"+ovStat[1])
			setText(hCards[2], "运行:\n"+ovStat[2])
			setText(hCards[3], "磁盘:\n"+ovStat[3])
			setText(hBody, ovBody)
		}
		return 0
	case wmAppRefreshNow:
		if page == "insight" {
			setText(hInfo, insText)
		}
		return 0
	case wmInsight:
		if page == "insight" {
			setText(hInfo, insText)
		}
		return 0
	case wmDisk:
		if page == "disk" {
			setText(hBody, dskBody)
		}
		return 0
	case wmRss:
		if page == "rss" {
			setText(hBody, rssText)
		}
		return 0
	case wmFavWorks:
		if page == "favs" {
			pInvalidateRect.Call(hwnd, 0, 1)
		}
		return 0
	case wmBindDone:
		if page == "insight" {
			setText(hHint, bindStatus)
		}
		return 0
	case wmDetail:
		if kbCardMode() && detailID != "" {
			pInvalidateRect.Call(hwnd, 0, 1)
		}
		return 0
	case wmSearchDone:
		if kbCardMode() {
			pInvalidateRect.Call(hwnd, 0, 1)
		}
		return 0
	case 0x002B: // WM_DRAWITEM
		return drawItem(uintptr(lParam))
	case 0x0134: // WM_CTLCOLOREDIT
		pSetTextColor.Call(wParam, colFg)
		pSetBkMode.Call(wParam, 1)
		pSetBkColor.Call(wParam, colSide)
		return brushBg
	case 0x0138: // WM_CTLCOLORSTATIC
		id := uintptr(0)
		if lParam != 0 {
			r, _, _ := pGetDlgCtrlID.Call(lParam)
			id = r
		}
		switch {
		case id == IDBody:
			pSetTextColor.Call(wParam, colFg)
			pSetBkMode.Call(wParam, 0)
			pSetBkColor.Call(wParam, colSide)
			return brushBg
		case isCard(id):
			pSetTextColor.Call(wParam, colFg)
			pSetBkMode.Call(wParam, 0)
			pSetBkColor.Call(wParam, colCard)
			return brushCard
		default:
			pSetTextColor.Call(wParam, colFg)
			pSetBkMode.Call(wParam, 1)
			return brushBg
		}
	case 0x0005: // WM_SIZE
		relayout()
		r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
		return r
	case 0x0010: // WM_CLOSE
		pDestroyWindow.Call(hwnd)
		return 0
	case 0x0002: // WM_DESTROY
		if fontTitle != 0 {
			pDeleteObject.Call(fontTitle)
		}
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

func drawBtn(di *drawItemStruct, fill, tc uintptr) {
	br, _, _ := pCreateSolidBrush.Call(fill)
	pFillRect.Call(di.HDC, uintptr(unsafe.Pointer(&di.RcItem)), br)
	pDeleteObject.Call(br)
	pSetBkMode.Call(di.HDC, 1)
	pSetTextColor.Call(di.HDC, tc)
	tp, _ := windows.UTF16PtrFromString(getText(di.HwndItem))
	rc := di.RcItem
	// centered, single line, vcenter
	pDrawText.Call(di.HDC, uintptr(unsafe.Pointer(tp)), uintptr(0xFFFFFFFF), uintptr(unsafe.Pointer(&rc)), 0x25)
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
	}
	r, _, _ := pDefWindowProc.Call(hwndMain, 0x002B, 0, 0)
	return r
}

func main() {
	runtime.LockOSThread()
	if !acquireSingleInstance() {
		return // existing window was raised instead
	}
	user32.NewProc("SetProcessDPIAware").Call()
	mod, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	hInst := mod

	exe, _ := os.Executable()
	dataDir = filepath.Join(filepath.Dir(exe), dataDirName)
	coverDir = filepath.Join(dataDir, "covers")
	_ = os.MkdirAll(coverDir, 0o755)
	cfg, _ = config.Load(filepath.Join(filepath.Dir(exe), "config.json"))
	mgr = monitor.New()
	st = kb.New(dataDir)
	page = "overview"

	brushBg, _, _ = pCreateSolidBrush.Call(uintptr(colBg))
	brushCard, _, _ = pCreateSolidBrush.Call(uintptr(colCard))

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

	hwndMain, _, _ = pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(utf16("星匣 STARBOX"))),
		uintptr(wsOverlappedWindow),
		0x80000000, 0x80000000, 1560, 960,
		0, 0, hInst, 0)
	enableDarkTitleBar(hwndMain)
	curHand, _, _ = pLoadCursor.Call(0, 32649)  // IDC_HAND
	curArrow, _, _ = pLoadCursor.Call(0, 32512) // IDC_ARROW
	pSetTimer.Call(hwndMain, 1, 5000, 0) // overview auto-refresh (WM_TIMER id 1)

	fontTitle = createWin32Font(34, true)
	fontNav = createWin32Font(22, false)
	fontCard = createWin32Font(26, false)
	fontBody = createWin32Font(22, false)
	fontTiny = createWin32Font(17, false)

	hBrand = createChild("STATIC", "星匣 STARBOX", ssLeft, IDBrand, 30, 30, 230, 42, fontTitle)
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
	hSave = createChild("BUTTON", "保存账号", 0, IDSave, 1092, 158, 120, 36, fontNav)
	hReff = createChild("BUTTON", "刷新热门", 0, IDReff, 310, 214, 120, 34, fontNav)
	hReffMine = createChild("BUTTON", "我的仓库", 0, IDReffMine, 440, 214, 120, 34, fontNav)
	hHint = createChild("STATIC", "", ssLeft, IDHint, 440, 220, 760, 26, fontNav)
	hInfo = createChild("STATIC", "", ssLeft, IDInfo, 310, 264, 940, 440, fontBody)
	// settings page controls
	hAuto = createChild("BUTTON", "开机自启动", bsAutoCheckBox, IDAuto, 310, 120, 220, 40, fontNav)
	hAutoSave = createChild("BUTTON", "保存", 0, IDSaveS, 540, 118, 110, 40, fontNav)
	// kb page controls
	kbCol = "anime"
	for i := range kbCols {
		hKbTab[i] = createChild("BUTTON", kbColLabels[kbCols[i]], bsOwnerDraw, KBTab+i, 310+i*120, 96, 116, 40, fontNav)
	}
	hKbToA = createChild("EDIT", "", esAutoHScroll|wsTabStop, KBToA, 310, 160, 460, 36, fontBody)
	hKbAddBtn = createChild("BUTTON", "＋ 添加", bsOwnerDraw, KBAdd, 780, 158, 110, 40, fontNav)
	hKbSearchBtn = createChild("BUTTON", "搜索并添加", bsOwnerDraw, KBSearch, 900, 158, 150, 40, fontNav)
	// dark-theme the edit boxes (disable visual styles so WM_CTLCOLOREDIT applies)
	empty := utf16("")
	pSetWindowTheme.Call(hKbToA, uintptr(unsafe.Pointer(empty)), uintptr(unsafe.Pointer(empty)))
	pSetWindowTheme.Call(hAcc, uintptr(unsafe.Pointer(empty)), uintptr(unsafe.Pointer(empty)))
	pSetWindowTheme.Call(hPass, uintptr(unsafe.Pointer(empty)), uintptr(unsafe.Pointer(empty)))
	// forward wheel messages from the edits so scrolling never dies after typing
	subclassEditWheel(hKbToA)
	subclassEditWheel(hAcc)
	subclassEditWheel(hPass)
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
		user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
		user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}
}
