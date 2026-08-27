//go:build windows

// STARBOX — native Win32 desktop app (no WebView2, no Gio). Reliable clicks.
// Dark theme (navy + cyan accent), owner-drawn sidebar nav, responsive layout.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

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

const dataDirName = "data"

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
	pCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	pSetTextColor     = gdi32.NewProc("SetTextColor")
	pSetBkMode        = gdi32.NewProc("SetBkMode")
	pSetBkColor       = gdi32.NewProc("SetBkColor")
	pFillRect         = user32.NewProc("FillRect")
	pDrawText         = user32.NewProc("DrawTextW")
	pGetDlgCtrlID     = user32.NewProc("GetDlgCtrlID")
	pGetClientRect    = user32.NewProc("GetClientRect")
	pMoveWindow       = user32.NewProc("MoveWindow")
)

var (
	hwndMain            uintptr
	fontTitle, fontNav  uintptr
	fontCard, fontBody  uintptr
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

func updateCards() {
	var c0, m0, u0, d0 string
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
	setText(hCards[0], "CPU\n"+c0)
	setText(hCards[1], "内存\n"+m0)
	setText(hCards[2], "运行\n"+u0)
	setText(hCards[3], "磁盘\n"+d0)
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

func boolShow(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

func renderPage() {
	title := pageLabels[page]
	overview := page == "overview"
	insight := page == "insight"
	for i := range hCards {
		pShowWindow.Call(hCards[i], boolShow(overview))
	}
	for i := range hPlat {
		pShowWindow.Call(hPlat[i], boolShow(insight))
	}
	pShowWindow.Call(hAcc, boolShow(insight))
	pShowWindow.Call(hPass, boolShow(insight))
	pShowWindow.Call(hSave, boolShow(insight))
	pShowWindow.Call(hReff, boolShow(insight))
	pShowWindow.Call(hHint, boolShow(insight))
	pShowWindow.Call(hInfo, boolShow(insight))
	setSet := page == "settings"
	pShowWindow.Call(hAuto, boolShow(setSet))
	pShowWindow.Call(hAutoSave, boolShow(setSet))
	kbon := page == "kb"
	for i := range kbCols {
		pShowWindow.Call(hKbTab[i], boolShow(kbon))
	}
	pShowWindow.Call(hKbToA, boolShow(kbon))
	pShowWindow.Call(hKbAddBtn, boolShow(kbon))
	pShowWindow.Call(hBody, boolShow(!insight && !setSet))

	var body string
	switch {
	case overview:
		updateCards()
		body = diskText()
	case insight:
		setText(hInfo, insightInfo())
		loadBind()
	case page == "settings":
		pSendMessage.Call(hAuto, 0x00F1, boolShow(settings.Load(dataDir).AutoStart), 0) // BM_SETCHECK
		body = "设置：\n\n（设置页其余选项后续接入）"
	case page == "kb":
		body = kbText()
	case page == "disk":
		body = dirText()
	default:
		body = "「" + pageLabels[page] + "」页面移植中，将逐个接入后台数据。"
	}
	setText(hTitle, title)
	setText(hBody, body)
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
	_, _ = st.Add(kbCol, data)
	setText(hKbToA, "")
	setText(hBody, kbText())
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
	pInvalidateRect.Call(hwndMain, 0, 1)
}

func wndProcMain(hwnd uintptr, msg uint32, wParam uintptr, lParam uintptr) uintptr {
	switch msg {
	case 0x0111: // WM_COMMAND
		id := uintptr(0xFFFF) & wParam
		if id >= navBase && id < uintptr(navBase+len(pages)) {
			page = pages[id-navBase]
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
			st := settings.Load(dataDir)
			st.AutoStart = on == 1
			exe, _ := os.Executable()
			_ = settings.SetAutoStart(st.AutoStart, exe)
			_ = settings.Save(dataDir, st)
			pInvalidateRect.Call(hwndMain, 0, 1)
			return 0
		}
		if id >= KBTab && id < uintptr(KBTab+5) {
			kbCol = kbCols[id-KBTab]
			setText(hBody, kbText())
			return 0
		}
		if id == KBAdd {
			kbAdd()
			return 0
		}
	case 0x002B: // WM_DRAWITEM
		return drawItem(uintptr(lParam))
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

func drawItem(diPtr uintptr) uintptr {
	di := (*drawItemStruct)(unsafe.Pointer(diPtr))
	if di.CtlID < navBase || di.CtlID >= uint32(navBase+len(pages)) {
		r, _, _ := pDefWindowProc.Call(hwndMain, 0x002B, 0, 0)
		return r
	}
	idx := di.CtlID - navBase
	fill := colSide
	tc := colFg
	if pages[idx] == page {
		fill = colAcc
		tc = colOnAcc
	}
	brush, _, _ := pCreateSolidBrush.Call(uintptr(fill))
	pFillRect.Call(di.HDC, uintptr(unsafe.Pointer(&di.RcItem)), brush)
	pDeleteObject.Call(brush)
	pSetBkMode.Call(di.HDC, 1)
	pSetTextColor.Call(di.HDC, uintptr(tc))
	tp, _ := windows.UTF16PtrFromString(getText(di.HwndItem))
	rc := di.RcItem
	pDrawText.Call(di.HDC, uintptr(unsafe.Pointer(tp)), uintptr(0xFFFFFFFF), uintptr(unsafe.Pointer(&rc)), 0x25)
	return 1
}

func main() {
	runtime.LockOSThread()
	user32.NewProc("SetProcessDPIAware").Call()
	mod, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	hInst := mod

	exe, _ := os.Executable()
	dataDir = filepath.Join(filepath.Dir(exe), dataDirName)
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
		hPlat[i] = createChild("BUTTON", platLabels[i], 0, IDPlat+i, 310+i*130, 96, 124, 40, fontNav)
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
		hKbTab[i] = createChild("BUTTON", kbColLabels[kbCols[i]], 0, KBTab+i, 310+i*120, 96, 116, 40, fontNav)
	}
	hKbToA = createChild("EDIT", "", esAutoHScroll|wsTabStop, KBToA, 310, 160, 460, 36, fontBody)
	hKbAddBtn = createChild("BUTTON", "＋ 添加", 0, KBAdd, 780, 158, 110, 40, fontNav)
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
