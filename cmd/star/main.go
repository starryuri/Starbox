//go:build windows

// STARBOX — native Win32 desktop app (no WebView2, no Gio). Reliable clicks.
// Dark theme (navy + cyan accent), owner-drawn sidebar nav, responsive layout.
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"butler/internal/anime"
	"butler/internal/config"
	"butler/internal/du"
	"butler/internal/githot"
	"butler/internal/kb"
	"butler/internal/monitor"
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
	IDInfo  = 509
	IDHint  = 510
	IDAuto  = 601
	IDSaveS = 602
	KBTab   = 701 // 5 tabs: 701..705
	KBToA   = 706 // add title edit
	KBAdd   = 707 // add button
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
	pDefWindowProc      = user32.NewProc("DefWindowProcW")
	pDestroyWindow      = user32.NewProc("DestroyWindow")
	pRegisterClassEx    = user32.NewProc("RegisterClassExW")
	pCreateFont         = gdi32.NewProc("CreateFontW")
	pSendMessage        = user32.NewProc("SendMessageW")
	pPostMessage        = user32.NewProc("PostMessageW")
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
	hAuto, hAutoSave    uintptr
	kbCol               string
	hKbTab              [5]uintptr
	hKbToA, hKbAddBtn   uintptr
	page                string
	mgr                 *monitor.State
	dataDir             string
	wndProc             = syscall.NewCallback(wndProcMain)
)

// --- KB anime card / detail state ---
var (
	kbRecs   []kb.Record
	kbCards  []kbCard
	kbScroll int
	detailID string
	detHits  []detHit
	coverDir string
	covers   sync.Map // id -> *covInfo
)

// --- generic themed list state (favs / notify / rules) ---
var (
	listRows  []listRow
	listHits  []detHit
	listPage  string // "favs" | "notify" | "rules"
	listAct   bool   // whether an action button (top-right) is shown
	listActL  string // action label
	listScroll int
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
	ovStat                    [4]string
	ovBody, insText, dskBody  string
)

func loadOverview() {
	if ovBusy {
		return
	}
	ovBusy = true
	setText(hCards[0], "CPU:\n…")
	setText(hCards[1], "内存:\n…")
	setText(hCards[2], "运行:\n…")
	setText(hCards[3], "磁盘:\n…")
	go func() {
		c0, m0, u0, d0 := computeStats()
		ovStat[0], ovStat[1], ovStat[2], ovStat[3] = c0, m0, u0, d0
		ovBody = diskText()
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
		resp, err := http.Get(url)
		if err != nil {
			covers.Store(id, &covInfo{path: path})
			return
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
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
	sidebarW := 280
	contentX := sidebarW + 30
	contentW := w - contentX - 30
	if contentW < 240 {
		contentW = 240
	}
	top = 216
	bottom = h - 30
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

func kbs2cards() []kbCard {
	cx, cw, top, _ := kbGeom()
	if len(kbRecs) == 0 || cw <= 0 {
		return nil
	}
	const gap = 16
	const minW = 150
	cols := (cw + gap) / (minW + gap)
	if cols < 1 {
		cols = 1
	}
	cardW := (cw - (cols-1)*gap) / cols
	if cardW < 90 {
		cardW = 90
	}
	coverH := cardW * 14 / 10
	titleH := 58
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
	if len(kbRecs) == 0 {
		drawTextRect(dc, cx, top, cw, 60, "（暂无条目，输入标题点「＋ 添加」）", fontBody, colDim, dtLeft)
		return
	}
	kbCards = kbs2cards()
	for _, c := range kbCards {
		if c.y < top-160 || c.y > bottom {
			continue // offscreen
		}
		fillRectColor(dc, c.x, c.y, c.w, c.h, colCard)
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
		drawTextRect(dc, c.x+6, ty+4, c.w-12, 34, c.title, fontCard, colFg, dtSingle)
		sc := statusColor(c.status)
		fillRectColor(dc, c.x+6, ty+38, c.w-12, 20, sc)
		drawTextRect(dc, c.x+6, ty+38, c.w-12, 20, c.status, fontTiny, colOnAcc, dtSingle|dtVCenter)
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

	pad := 16
	lw := 200
	lh := 286
	if ci := getCover(r.ID); ci != nil && ci.loaded {
		drawStretch(dc, cx+pad, top+pad, lw, lh, ci)
	} else {
		fillRectColor(dc, cx+pad, top+pad, lw, lh, colCard2)
		drawTextRect(dc, cx+pad, top+pad, lw, lh, title, fontCard, colDim, dtCenter|dtVCenter)
	}
	ix := cx + pad + lw + 20
	iw := cw - pad - lw - 20 - pad
	if iw < 120 {
		iw = 120
	}
	drawTextRect(dc, ix, top+pad, iw, 40, title, fontTitle, colFg, dtWordBreak)
	sty := top + pad + 52
	drawTextRect(dc, ix, sty, 60, 30, "状态", fontNav, colDim, dtSingle|dtVCenter)
	sx := ix + 64
	for _, s := range []string{"想追", "在看", "看过", "搁置"} {
		w := 76
		sel := s == status
		sc := uintptr(colCard2)
		tc := uintptr(colFg)
		if sel {
			sc = colAcc
			tc = colOnAcc
		}
		fillRectColor(dc, sx, sty, w, 30, sc)
		drawTextRect(dc, sx, sty, w, 30, s, fontNav, tc, dtSingle|dtVCenter)
		detHits = append(detHits, detHit{sx, sty, w, 30, "status", s})
		sx += w + 8
	}
	my := sty + 48
	meta := "评分 " + fmt.Sprintf("%.1f", rate)
	if total != "" {
		meta += "    集数 " + total
	}
	if watched != "" {
		meta += "    已看 " + watched
	}
	drawTextRect(dc, ix, my, iw, 30, meta, fontNav, colDim, dtSingle|dtVCenter)
	ny := my + 44
	nh := bottom - ny - 70
	if nh < 40 {
		nh = 40
	}
	if note != "" {
		drawTextRect(dc, ix, ny, iw, nh, note, fontBody, colFg, dtWordBreak)
	}
	by := bottom - 52
	bw := 120
	bh := 38
	// back
	fillRectColor(dc, cx+pad, by, bw, bh, colCard2)
	drawTextRect(dc, cx+pad, by, bw, bh, "← 返回", fontNav, colFg, dtSingle|dtVCenter|dtCenter)
	detHits = append(detHits, detHit{cx + pad, by, bw, bh, "back", ""})
	// watch +1
	wx := cx + pad + bw + 12
	fillRectColor(dc, wx, by, bw, bh, colAcc)
	drawTextRect(dc, wx, by, bw, bh, "▶ 看一集 +1", fontNav, colOnAcc, dtSingle|dtVCenter|dtCenter)
	detHits = append(detHits, detHit{wx, by, bw, bh, "watch", r.ID})
	// delete
	dx := cx + cw - pad - bw
	fillRectColor(dc, dx, by, bw, bh, colRed)
	drawTextRect(dc, dx, by, bw, bh, "删除", fontNav, colFg, dtSingle|dtVCenter|dtCenter)
	detHits = append(detHits, detHit{dx, by, bw, bh, "delete", r.ID})
	if link, _ := data["link"].(string); link != "" {
		drawTextRect(dc, ix, by+2, iw-10, 34, "链接: "+link, fontTiny, colDim, dtSingle)
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

func onKBHit(action, id string) {
	switch action {
	case "card":
		detailID = id
		pInvalidateRect.Call(hwndMain, 0, 1)
	case "back":
		detailID = ""
		pInvalidateRect.Call(hwndMain, 0, 1)
	case "delete":
		kbDelete(id)
	case "watch":
		kbWatchInc(id)
	case "status":
		kbSetStatus(detailID, id)
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
	ry := top + 54
	rh := 72
	gap := 10
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
		}
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
	sidebarW := 280
	contentX := sidebarW + 30
	contentW := w - contentX - 30
	if contentW < 240 {
		contentW = 240
	}
	moveWin(hBrand, 30, 30, sidebarW-50, 42)
	moveWin(hTag, 30, 86, sidebarW-50, 26)
	navH := 48
	for i := range pages {
		moveWin(hNav[i], 26, 126+i*navH, sidebarW-52, navH-6)
	}
	moveWin(hTitle, contentX, 30, contentW, 46)
	cardGap := 16
	cardW := (contentW - 3*cardGap) / 4
	if cardW < 100 {
		cardW = 100
	}
	cardH := 150
	for i := 0; i < 4; i++ {
		moveWin(hCards[i], contentX+i*(cardW+cardGap), 96, cardW, cardH)
	}
	bodyY := 96 + cardH + 26
	bodyH := h - bodyY - 30
	if bodyH < 60 {
		bodyH = 60
	}
	moveWin(hBody, contentX, bodyY, contentW, bodyH)
	// insight controls
	platGap := 8
	platW := (contentW - 3*platGap) / 4
	if platW < 100 {
		platW = 100
	}
	for i := 0; i < 4; i++ {
		moveWin(hPlat[i], contentX+i*(platW+platGap), 96, platW, 44)
	}
	moveWin(hAcc, contentX, 162, 380, 34)
	moveWin(hPass, contentX+400, 162, 380, 34)
	moveWin(hSave, contentX+800, 160, 120, 38)
	moveWin(hReff, contentX, 214, 120, 36)
	moveWin(hHint, contentX+130, 216, contentW-130, 30)
	moveWin(hInfo, contentX, 264, contentW, h-264-30)
	moveWin(hAuto, contentX, 96, 240, 40)
	moveWin(hAutoSave, contentX+250, 94, 110, 42)
	kbgap := 8
	kbw := (contentW - 4*kbgap) / 5
	if kbw < 90 {
		kbw = 90
	}
	for i := range kbCols {
		moveWin(hKbTab[i], contentX+i*(kbw+kbgap), 96, kbw, 44)
	}
	moveWin(hKbToA, contentX, 162, 460, 36)
	moveWin(hKbAddBtn, contentX+470, 158, 110, 40)
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
	pShowWindow.Call(hHint, pBool(insight))
	pShowWindow.Call(hInfo, pBool(insight))
	pShowWindow.Call(hAuto, pBool(setSet))
	pShowWindow.Call(hAutoSave, pBool(setSet))
	for i := range kbCols {
		pShowWindow.Call(hKbTab[i], pBool(kbon))
	}
	pShowWindow.Call(hKbToA, pBool(kbon))
	pShowWindow.Call(hKbAddBtn, pBool(kbon))

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
	case lm:
		listPage = page
		listScroll = 0
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
			saveBind()
			return 0
		}
		if id == IDReff {
			setText(hInfo, insightInfo())
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
	case 0x020A: // WM_MOUSEWHEEL
		if kbCardMode() && detailID == "" {
			delta := int(int16(uint16((lParam >> 16) & 0xFFFF)))
			kbScroll -= delta / 120 * 90
			if kbScroll < 0 {
				kbScroll = 0
			}
			kbCards = kbs2cards()
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
	case 0x000F: // WM_PAINT
		var ps paintStruct
		dc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if dc != 0 {
			w, h := clientSize()
			// content background
			fillRectColor(dc, 0, 0, w, h, colBg)
			// sidebar panel
			sidebarW := 288
			if w > sidebarW {
				fillRectColor(dc, 0, 0, sidebarW, h, colSide)
			}
			br, _, _ := pCreateSolidBrush.Call(colBg)
			pDeleteObject.Call(br)
			paintFragment(dc)
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
	user32.NewProc("SetProcessDPIAware").Call()
	mod, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	hInst := mod

	exe, _ := os.Executable()
	dataDir = filepath.Join(filepath.Dir(exe), dataDirName)
	coverDir = filepath.Join(dataDir, "covers")
	_ = os.MkdirAll(coverDir, 0o755)
	_, _ = config.Load(filepath.Join(filepath.Dir(exe), "config.json"))
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
		0x80000000, 0x80000000, 1280, 820,
		0, 0, hInst, 0)

	fontTitle = createWin32Font(26, true)
	fontNav = createWin32Font(18, false)
	fontCard = createWin32Font(20, false)
	fontBody = createWin32Font(18, false)
	fontTiny = createWin32Font(15, false)

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
	// dark-theme the edit boxes (disable visual styles so WM_CTLCOLOREDIT applies)
	empty := utf16("")
	pSetWindowTheme.Call(hKbToA, uintptr(unsafe.Pointer(empty)), uintptr(unsafe.Pointer(empty)))
	pSetWindowTheme.Call(hAcc, uintptr(unsafe.Pointer(empty)), uintptr(unsafe.Pointer(empty)))
	pSetWindowTheme.Call(hPass, uintptr(unsafe.Pointer(empty)), uintptr(unsafe.Pointer(empty)))
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
