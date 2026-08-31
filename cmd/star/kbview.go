//go:build windows

package main

import (
	"bytes"
	"encoding/json"
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
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"butler/internal/anime"
	"butler/internal/kb"

)
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
			SetStatus("封面下载失败")
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

// ---------- detail page section ordering (task: 自由排列) ----------

var detailSections = []string{"meta", "studios", "cast", "staff"} // default order

func detailLayoutPath() string { return filepath.Join(curProfDir, "detail_layout.json") }

func loadDetailLayout() {
	b, err := os.ReadFile(detailLayoutPath())
	if err != nil {
		return
	}
	var v struct {
		Order []string `json:"order"`
	}
	if json.Unmarshal(b, &v) != nil {
		return
	}
	// validate: same set of sections
	if len(v.Order) != len(detailSections) {
		return
	}
	seen := map[string]bool{}
	for _, s := range v.Order {
		if !sectionValid(s) || seen[s] {
			return
		}
		seen[s] = true
	}
	detailSections = v.Order
}

func saveDetailLayout() {
	b, _ := json.MarshalIndent(struct {
		Order []string `json:"order"`
	}{detailSections}, "", "  ")
	_ = os.MkdirAll(dataDir, 0o755)
	_ = os.WriteFile(detailLayoutPath(), b, 0o644)
}

func sectionValid(s string) bool {
	for _, v := range []string{"meta", "studios", "cast", "staff"} {
		if s == v {
			return true
		}
	}
	return false
}

func moveSection(id string, delta int) {
	idx := -1
	for i, s := range detailSections {
		if s == id {
			idx = i
			break
		}
	}
	n := idx + delta
	if idx < 0 || n < 0 || n >= len(detailSections) {
		return
	}
	detailSections[idx], detailSections[n] = detailSections[n], detailSections[idx]
	saveDetailLayout()
	pInvalidateRect.Call(hwndMain, 0, 1)
}

// drawSectionHeader renders a section title with ↑/↓ reorder buttons.
func drawSectionHeader(dc uintptr, x, y, w int, title, sec string, hits *[]detHit) {
	drawTextRect(dc, x, y, 220, 30, title, fontNav, colAcc, dtSingle|dtVCenter)
	bx := x + w - 96
	fillRectColor(dc, bx, y, 44, 28, colCard)
	drawTextRect(dc, bx, y, 44, 28, "↑", fontTiny, colFg, dtCenter|dtVCenter)
	*hits = append(*hits, detHit{bx, y, 44, 28, "secup", sec})
	fillRectColor(dc, bx+50, y, 44, 28, colCard)
	drawTextRect(dc, bx+50, y, 44, 28, "↓", fontTiny, colFg, dtCenter|dtVCenter)
	*hits = append(*hits, detHit{bx + 50, y, 44, 28, "secdown", sec})
}

// drawStretch draws the cover fitted (letterboxed) inside the target rect
// instead of stretching, so portrait covers keep their shape.
func drawStretch(dc uintptr, x, y, w, h int, ci *covInfo) {
	if ci == nil || ci.hbmp == 0 || w <= 0 || h <= 0 {
		return
	}
	mem, _, _ := pCreateCompatibleDC.Call(dc)
	pSelectObject.Call(mem, ci.hbmp)
	pSetStretchBltMode.Call(dc, colorOnColor)
	// fit rect: largest box that preserves the image aspect ratio
	dw, dh := w, h
	if ci.w > 0 && ci.h > 0 {
		srcRatio := float64(ci.w) / float64(ci.h)
		boxRatio := float64(w) / float64(h)
		if srcRatio < boxRatio {
			dw = int(float64(h) * srcRatio)
		} else {
			dh = int(float64(w) / srcRatio)
		}
	}
	ox, oy := x+(w-dw)/2, y+(h-dh)/2
	pStretchBlt.Call(dc, uintptr(ox), uintptr(oy), uintptr(dw), uintptr(dh),
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

func kbCardMode() bool { return page == "kb" }

func kbGeom() (cx, cw, top, bottom int) {
	w, h := clientSize()
	sidebarW := scale(320)
	contentX := sidebarW + scale(30)
	contentW := w - contentX - scale(30)
	if contentW < scale(260) {
		contentW = 260
	}
	top = 240
	bottom = h - 36
	return contentX, contentW, top, bottom
}

func refreshKB() {
	recs, err := st.List(kbCol)
	if err != nil {
		SetError("读取 %s 集合失败：%v", kbCol, err)
	} else if statusVisible() && statusIsErr {
		statusText = "" // recovered
	}
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
	titleH := 84
	cardH := coverH + titleH
	out := make([]kbCard, 0, len(kbRecs))
	for i, r := range kbRecs {
		col := i % cols
		row := i / cols
		title, _ := r.Data["title"].(string)
		status, _ := r.Data["status"].(string)
		if kbCol == "books" && status == "" {
			author, _ := r.Data["author"].(string)
			fmtS, _ := r.Data["format"].(string)
			sub := author
			if fmtS != "" {
				if sub != "" {
					sub += " · "
				}
				sub += strings.ToUpper(fmtS)
			}
			status = sub
		}
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
	defer func() {
		if last := kbCards; len(last) > 0 {
			lastBottom := last[len(last)-1].y + last[len(last)-1].h
			drawScrollIndicator(dc, lastBottom-top, bottom-top, kbScroll, cx+cw-6, 4)
		}
	}()
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
		// DT_END_ELLIPSIS (0x00008000): "长标题…" instead of mid-glyph cut
		drawTextRect(dc, c.x+6, ty+scale(4), c.w-12, scale(30), c.title, fontBody, colFg, dtSingle|0x00008000)
		if kbCol == "books" {
			if rec := recByID(c.id); rec != nil {
				if bt, ok := bookProgress(rec); ok {
					drawTextRect(dc, c.x+6, ty+scale(38), c.w-12, scale(26), bt, fontTiny, colDim, dtSingle|dtVCenter)
				}
			}
		}
		sc := statusColor(c.status)
		fillRectColor(dc, c.x+6, ty+scale(40), c.w-12, scale(26), sc)
		drawTextRect(dc, c.x+6, ty+scale(40), c.w-12, scale(26), c.status, fontTiny, colOnAcc, dtSingle|dtVCenter)
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
	case "在看", "看过", "在读", "读过", "进行中":
		return colAcc
	case "想追", "想看", "想玩", "想读", "规划中":
		return colAcc
	case "搁置", "弃", "已放弃":
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
	// WebView2 path: the Chromium host draws over this area; GDI fallback
	// keeps working when WebView2 is missing.
	if webShowDetail() {
		return
	}
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

	pad := scale(20)
	lw := scale(220)
	lh := scale(340)
	if ci := getCover(r.ID); ci != nil && ci.loaded {
		drawStretch(dc, cx+pad, top+pad, lw, lh, ci)
	} else {
		fillRectColor(dc, cx+pad, top+pad, lw, lh, colCard2)
		drawTextRect(dc, cx+pad, top+pad, lw, lh, title, fontCard, colDim, dtCenter|dtVCenter)
	}
	ix := cx + pad + lw + scale(24)
	iw := cw - pad - lw - scale(24) - pad
	if iw < scale(140) {
		iw = scale(140)
	}
	drawTextRect(dc, ix, top+pad, iw, scale(48), title, fontTitle, colFg, dtSingle|0x00008000)
	sty := top + pad + scale(60)
	drawTextRect(dc, ix, sty, scale(70), scale(38), "状态", fontNav, colDim, dtSingle|dtVCenter)
	sx := ix + scale(78)
	for _, s := range []string{"想追", "在看", "看过", "搁置"} {
		w := scale(96)
		sel := s == status
		sc := uintptr(colCard2)
		tc := uintptr(colFg)
		if sel {
			sc = colAcc
			tc = colOnAcc
		}
		fillRectColor(dc, sx, sty, w, scale(38), sc)
		drawTextRect(dc, sx, sty, w, scale(38), s, fontNav, tc, dtSingle|dtVCenter)
		detHits = append(detHits, detHit{sx, sty, w, scale(38), "status", s})
		sx += w + scale(10)
	}
	my := sty + scale(50)
	secW := cw - (ix - cx) - scale(20)
	// content flows from here; buttons live at the bottom; link drawn in flow
	btnTop := bottom - scale(76)
	contentTop := my - detailScroll
	curY := contentTop
	if note != "" {
		drawTextRect(dc, ix, contentTop, iw, scale(66), note, fontBody, colFg, dtWordBreak)
		curY += scale(62) + scale(10)
	} else {
		curY = contentTop + scale(8)
	}
	linkText, _ := data["link"].(string)
	drawn := map[string]bool{}
	for _, sec := range detailSections {
		if drawn[sec] {
			continue
		}
		drawn[sec] = true
		switch sec {
		case "meta":
			meta := "评分 " + fmt.Sprintf("%.1f", rate)
			if total != "" {
				meta += "    集数 " + total
			}
			if watched != "" {
				meta += "    已看 " + watched
			}
			if air, _ := data["air_start"].(string); air != "" {
				meta += "    播出 " + air
			}
			drawSectionHeader(dc, ix, curY, secW, "信息", sec, &detHits)
			drawTextRectFit(dc, ix, curY+scale(40), secW, scale(34), meta, scale(19), false, colDim, dtSingle|dtVCenter)
			curY += scale(80)
		case "studios":
			if detailInfo == nil || detailLoading == r.ID || len(detailInfo.Studios) == 0 {
				continue
			}
			drawSectionHeader(dc, ix, curY, secW, "制作公司", sec, &detHits)
			sy := curY + scale(42)
			for _, s := range detailInfo.Studios {
				if sy+scale(40) > btnTop {
					break
				}
				faved := favExists(s.Name)
				sc := uintptr(colCard2)
				if faved {
					sc = colAcc
				}
				fillRectColor(dc, ix+scale(8), sy, scale(32), scale(32), sc)
				drawTextRect(dc, ix+scale(8), sy, scale(32), scale(32), "★", fontBody, colOnAcc, dtCenter|dtVCenter)
				nameW := scale(280)
				drawTextRect(dc, ix+scale(52), sy, nameW, scale(34), s.Name, fontCard, colFg, dtSingle|dtVCenter|0x00008000)
				// whole row (star + name) toggles favourite
				detHits = append(detHits, detHit{ix + scale(8), sy, scale(52) + nameW, scale(34), "dettoggle", "studio|" + s.Name + "|" + strconv.Itoa(s.ID)})
				sy += scale(40)
			}
			curY = sy + scale(14)
		case "cast":
			if detailInfo == nil || detailLoading == r.ID || len(detailInfo.Characters) == 0 {
				continue
			}
			drawSectionHeader(dc, ix, curY, secW, "声优 CV", sec, &detHits)
			colW := scale(300)
			nCols := int(iw-scale(12)) / colW
			if nCols < 1 {
				nCols = 1
			}
			rowH := scale(40)
			slots := 9
			shown := 0
			for shown < len(detailInfo.Characters) && shown < slots {
				ch := detailInfo.Characters[shown]
				row, col := shown/nCols, shown%nCols
				px := ix + scale(8) + col*colW
				py := curY + scale(44) + row*rowH
				if py+rowH > btnTop {
					break
				}
				shown++
				if len(ch.VAs) == 0 {
					continue
				}
				va := ch.VAs[0]
				faved := favExists(va.Name)
				sc := uintptr(colCard2)
				if faved {
					sc = colAcc
				}
				fillRectColor(dc, px, py, scale(34), scale(34), sc)
				drawTextRect(dc, px, py, scale(34), scale(34), "★", fontBody, colOnAcc, dtCenter|dtVCenter)
				nameW := colW - scale(52)
				drawTextRect(dc, px+scale(44), py, nameW, scale(34), va.Name, fontCard, colFg, dtSingle|dtVCenter|0x00008000)
				detHits = append(detHits, detHit{px, py, scale(44) + nameW, scale(34), "dettoggle", "cv|" + va.Name + "|" + strconv.Itoa(va.ID)})
			}
			rowsUsed := (shown + nCols - 1) / nCols
			curY = curY + scale(42) + rowsUsed*rowH + scale(14)
		case "staff":
			if detailInfo == nil || detailLoading == r.ID || len(detailInfo.Staff) == 0 {
				continue
			}
			drawSectionHeader(dc, ix, curY, secW, "制作人员 Staff", sec, &detHits)
			colW2 := int(secW-scale(12)) / 2
			if colW2 < scale(220) {
				colW2 = secW - scale(12)
			}
			rowH2 := scale(34)
			maxRows := int(btnTop-(curY+scale(44))) / rowH2
			if maxRows < 1 {
				maxRows = 1
			}
			staffY := curY + scale(44)
			shown := 0
			for _, s := range detailInfo.Staff {
				row, col := shown/2, shown%2
				if row >= maxRows {
					break
				}
				label := s.Role + "：" + s.Name
				drawTextRectFit(dc, ix+scale(12)+col*colW2, staffY+row*rowH2, colW2-scale(16), scale(28), label, scale(20), false, colFg, dtSingle|dtVCenter)
				shown++
			}
			rowsUsed2 := (shown + 1) / 2
			staffY += rowsUsed2 * rowH2
			if shown < len(detailInfo.Staff) {
				if staffY > btnTop-scale(28) {
					staffY = btnTop - scale(28)
				}
				drawTextRect(dc, ix+scale(12), staffY, secW-scale(12), scale(24), fmt.Sprintf("…等 %d 项", len(detailInfo.Staff)-shown), fontBody, colDim, dtSingle)
				staffY += scale(28)
			}
			curY = staffY + scale(16)
		}
	}
	detailContentBottom = curY
	// link (clickable) drawn in flow at the end of the content
	if linkText != "" && curY+scale(40) < btnTop {
		lw2 := iw
		if lw2 > scale(360) {
			lw2 = scale(360)
		}
		drawTextRectFit(dc, ix, curY, lw2, scale(34), "链接: "+linkText, scale(19), false, colAcc, dtSingle|dtVCenter)
		detHits = append(detHits, detHit{ix, curY, lw2, scale(34), "openlink", linkText})
	}
	// clamp scroll so content never floats above the header
	if contentTop > my {
		detailScroll = 0
	}
	// bottom buttons
	bw := scale(150)
	bh := scale(52)
	by := bottom - scale(76)
	backFill := uintptr(colCard2)
	if hoverAct == "back" {
		backFill = colAcc
	}
	fillRectColor(dc, cx+pad, by, bw, bh, backFill)
	drawTextRect(dc, cx+pad, by, bw, bh, "← 返回", fontNav, colFg, dtSingle|dtVCenter|dtCenter)
	detHits = append(detHits, detHit{cx + pad, by, bw, bh, "back", ""})
	wx := cx + pad + bw + scale(12)
	watchFill := uintptr(colAcc)
	watchTx := uintptr(colOnAcc)
	if hoverAct == "watch" {
		watchFill = colFg
		watchTx = colBg
	}
	fillRectColor(dc, wx, by, bw, bh, watchFill)
	drawTextRect(dc, wx, by, bw, bh, "▶ 看一集 +1", fontNav, watchTx, dtSingle|dtVCenter|dtCenter)
	detHits = append(detHits, detHit{wx, by, bw, bh, "watch", r.ID})
	dx := cx + cw - pad - bw
	delFill := uintptr(colRed)
	if hoverAct == "delete" {
		delFill = 0x0000D0
	}
	fillRectColor(dc, dx, by, bw, bh, delFill)
	drawTextRect(dc, dx, by, bw, bh, "删除", fontNav, colFg, dtSingle|dtVCenter|dtCenter)
	detHits = append(detHits, detHit{dx, by, bw, bh, "delete", r.ID})
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
	webRefreshDetail()
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
	webRefreshDetail()
}

func kbDelete(id string) {
	if !confirmBox("确定删除该条目？删除后无法恢复。", "删除确认") {
		return
	}
	_ = st.Delete(kbCol, id)
	detailID = ""
	kbReload()
	webRefreshDetail()
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
	case "books":
		data["status"] = "想读"
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
		// re-read from the store (never touch the shared kbRecs slice off-thread)
		if all, err := st.List("anime"); err == nil {
			for _, r := range all {
				if r.ID == id {
					for k, v := range r.Data {
						if _, ok := data[k]; !ok {
							data[k] = v
						}
					}
					break
				}
			}
		}
		_, _ = st.Update("anime", id, data)
		pPostMessage.Call(hwndMain, uintptr(wmAppRefresh), 0, 0)
	}()
}

// fetchDetailAsync pulls studios/cast/staff for the record being viewed,
// then persists them into the record so later opens are instant.
func fetchDetailAsync(id string) {
	if kbCol != "anime" {
		return // local-only columns never hit the network
	}
	rec := recByID(id)
	if rec == nil {
		return
	}
	// already enriched and cached in the record? show instantly, no network.
	// Pre-staff-split caches parked staff rows inside studios; refetch when the
	// studio list looks like a staff roster (more than 3 entries).
	if cached, ok := rec.Data["_detail"].(map[string]interface{}); ok && cached != nil {
		stale := false
		if arr, ok := cached["studios"].([]interface{}); ok && len(arr) > 3 {
			stale = true
		}
		if !stale {
			if d := detailFromCache(cached); d != nil {
				detailInfo = d
				pInvalidateRect.Call(hwndMain, 0, 1)
				return
			}
		}
	}
	v, _ := rec.Data["anilist_id"].(string)
	if v == "" {
		// Bangumi-added record: resolve via Xinyuu (Chinese studios/CV/staff)
		title, _ := rec.Data["title"].(string)
		if title == "" || detailBusy {
			return
		}
		detailBusy = true
		detailLoading = id
		bgmID, _ := rec.Data["bgm_id"].(float64)
		go func() {
			d := enrichViaXinyuu(title)
			if d == nil {
				d = enrichViaBangumi(int(bgmID))
			}
			if d != nil && detailLoading == id {
				detailInfo = d
				persistDetail(id, d)
			}
			detailBusy = false
			detailLoading = ""
			pPostMessage.Call(hwndMain, uintptr(wmDetail), 0, 0)
		}()
		return
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
			persistDetail(id, &d)
		}
		detailBusy = false
		detailLoading = ""
		pPostMessage.Call(hwndMain, uintptr(wmDetail), 0, 0)
	}()
}

// xyTitleMatch picks the best Xinyuu hit for a title.
// exact match > full containment > shared first-two-words prefix.
func xyTitleMatch(title string, xs []anime.XinyuuAnime) (anime.XinyuuAnime, bool) {
	for _, x := range xs {
		if x.TitleChinese == title || x.TitleOriginal == title {
			return x, true
		}
	}
	words := splitTitleWords(title)
	for _, x := range xs {
		if len(words) > 0 && strings.Contains(x.TitleChinese, title) || title != "" && strings.Contains(title, x.TitleChinese) && len([]rune(x.TitleChinese)) >= 4 {
			return x, true
		}
	}
	for _, x := range xs {
		if prefixWords(x.TitleChinese, words, 2) {
			return x, true
		}
	}
	return anime.XinyuuAnime{}, false
}

// splitTitleWords breaks a title into space/separator words.
func splitTitleWords(title string) []string {
	f := strings.FieldsFunc(title, func(r rune) bool {
		return r == ' ' || r == '　' || r == '·' || r == '！' || r == '!'
	})
	return f
}

// prefixWords reports whether the first n words of title match s.
func prefixWords(s string, words []string, n int) bool {
	if len(words) < n {
		return false
	}
	need := strings.Join(words[:n], " ")
	return strings.HasPrefix(s, need)
}

// enrichViaXinyuu resolves studios/cast via XinyuuDB; nil when not found.
func enrichViaXinyuu(title string) *anime.Detail {
	xs, err := anime.XinyuuSearch(title)
	if err != nil || len(xs) == 0 {
		return nil
	}
	pick, ok := xyTitleMatch(title, xs)
	if !ok {
		return nil
	}
	aid := pick.AnimeID
	stf, e1 := anime.XinyuuStaffGet(aid)
	chs, e2 := anime.XinyuuCharactersGet(aid)
	if (e1 != nil || len(stf) == 0) && (e2 != nil || len(chs) == 0) {
		return nil
	}
	return buildDetail(stf, chs)
}

// enrichViaBangumi falls back to Bangumi persons/characters via bgm id.
// Chinese data: 动画制作 → Studios, every other credited person → Staff.
func enrichViaBangumi(bgmID int) *anime.Detail {
	if bgmID <= 0 {
		return nil
	}
	d := &anime.Detail{}
	if persons, err := anime.BangumiSubjectPersons(bgmID); err == nil {
		for _, p := range persons {
			if p.Relation == "动画制作" {
				d.Studios = append(d.Studios, anime.Studio{ID: p.ID, Name: p.Name})
			} else if p.Relation != "配音" {
				d.Staff = append(d.Staff, anime.StaffMember{ID: p.ID, Name: p.Name, Role: p.Relation})
			}
		}
	}
	if chs, err := anime.BangumiCharactersGet(bgmID); err == nil {
		for _, ch := range chs {
			ce := anime.Character{ID: ch.ID, Name: ch.Name}
			for _, a := range ch.Actors {
				ce.VAs = append(ce.VAs, anime.VA{Name: a.Name})
			}
			d.Characters = append(d.Characters, ce)
		}
	}
	if len(d.Studios) == 0 && len(d.Characters) == 0 {
		return nil
	}
	return d
}

// buildDetail converts Xinyuu staff/characters into a Detail.
// Xinyuu credits studios as staff entries with role 动画制作; everything else
// (导演/系列构成/人设/音乐…) becomes the Staff section, all in Chinese.
func buildDetail(stf []anime.XinyuuStaff, chs []anime.XinyuuCharacter) *anime.Detail {
	d := &anime.Detail{}
	seen := map[string]bool{}
	for _, s := range stf {
		if s.NameChinese == "" || seen[strconv.Itoa(s.StaffID)+s.RoleType] {
			continue
		}
		seen[strconv.Itoa(s.StaffID)+s.RoleType] = true
		if s.RoleType == "动画制作" {
			d.Studios = append(d.Studios, anime.Studio{ID: s.StaffID, Name: s.NameChinese})
		} else {
			d.Staff = append(d.Staff, anime.StaffMember{ID: s.StaffID, Name: s.NameChinese, Role: s.RoleType})
		}
	}
	seenCh := map[int]bool{}
	for _, ch := range chs {
		if seenCh[ch.CharacterID] {
			continue
		}
		seenCh[ch.CharacterID] = true
		ce := anime.Character{ID: ch.CharacterID, Name: ch.NameChinese}
		for _, va := range ch.VoiceActors {
			ce.VAs = append(ce.VAs, anime.VA{Name: va})
		}
		d.Characters = append(d.Characters, ce)
	}
	return d
}

// persistDetail caches the enriched detail into the record (key "_detail").
func persistDetail(id string, d *anime.Detail) {
	rec := recByID(id)
	if rec == nil || d == nil {
		return
	}
	if len(d.Studios) == 0 && len(d.Characters) == 0 && len(d.Staff) == 0 {
		return
	}
	data := rec.Data
	// enrich record-level fields the detail page reads directly
	if d.StartDate != "" {
		data["air_start"] = d.StartDate
	}
	if d.Duration > 0 {
		data["duration"] = d.Duration
	}
	if d.Status != "" {
		data["air_status"] = zhAirStatus(d.Status)
	}
	cached := map[string]interface{}{}
	stArr := make([]map[string]interface{}, 0, len(d.Studios))
	for _, s := range d.Studios {
		stArr = append(stArr, map[string]interface{}{"id": s.ID, "name": s.Name})
	}
	sfArr := make([]map[string]interface{}, 0, len(d.Staff))
	for _, s := range d.Staff {
		sfArr = append(sfArr, map[string]interface{}{"id": s.ID, "name": s.Name, "role": s.Role})
	}
	chArr := make([]map[string]interface{}, 0, len(d.Characters))
	for _, c := range d.Characters {
		vas := make([]map[string]interface{}, 0, len(c.VAs))
		for _, va := range c.VAs {
			vas = append(vas, map[string]interface{}{"id": va.ID, "name": va.Name})
		}
		chArr = append(chArr, map[string]interface{}{"id": c.ID, "name": c.Name, "vas": vas})
	}
	cached["studios"] = stArr
	cached["staff"] = sfArr
	cached["characters"] = chArr
	data["_detail"] = cached
	if _, err := st.Update("anime", id, data); err != nil {
		_ = err
	}
}

// detailFromCache rebuilds a Detail from the cached record field.
func detailFromCache(cached map[string]interface{}) *anime.Detail {
	d := &anime.Detail{}
	if stArr, ok := cached["studios"].([]interface{}); ok {
		for _, raw := range stArr {
			m, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			idf, _ := m["id"].(float64)
			if name != "" {
				d.Studios = append(d.Studios, anime.Studio{ID: int(idf), Name: name})
			}
		}
	}
	if sfArr, ok := cached["staff"].([]interface{}); ok {
		for _, raw := range sfArr {
			m, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			role, _ := m["role"].(string)
			idf, _ := m["id"].(float64)
			if name != "" {
				d.Staff = append(d.Staff, anime.StaffMember{ID: int(idf), Name: name, Role: role})
			}
		}
	}
	if chArr, ok := cached["characters"].([]interface{}); ok {
		for _, raw := range chArr {
			m, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			idf, _ := m["id"].(float64)
			ce := anime.Character{ID: int(idf), Name: name}
			if vas, ok := m["vas"].([]interface{}); ok {
				for _, vraw := range vas {
					vm, ok := vraw.(map[string]interface{})
					if !ok {
						continue
					}
					vn, _ := vm["name"].(string)
					vid, _ := vm["id"].(float64)
					if vn != "" {
						ce.VAs = append(ce.VAs, anime.VA{ID: int(vid), Name: vn})
					}
				}
			}
			if ce.Name != "" {
				d.Characters = append(d.Characters, ce)
			}
		}
	}
	if len(d.Studios) == 0 && len(d.Characters) == 0 && len(d.Staff) == 0 {
		return nil
	}
	return d
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

// favToggle adds or removes a studio/cast favorite.
// alID is the Bangumi person id (Chinese works) with anilist id as fallback.
func favToggle(name, typ string, alID int) {
	defer webRefreshDetail()
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
		"bgm_id": float64(alID),
		"al_id": float64(alID),
	})
}

// drawScrollIndicator renders a slim scrollbar hint for scrollable content.
func drawScrollIndicator(dc uintptr, contentH, viewH, scroll, x, w int) {
	if contentH <= viewH || viewH <= 0 || w <= 0 {
		return
	}
	trackH := viewH * viewH / contentH
	if trackH < 24 {
		trackH = 24
	}
	maxScroll := contentH - viewH
	if maxScroll <= 0 {
		return
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	y := scroll * (viewH - trackH) / maxScroll
	fillRectColor(dc, x, y, w, trackH, colAcc)
}

// hitAt resolves a click position to (action, id) for card walls and
// then generic lists — mirrors the click handlers.
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
	if page == "favs" && favDetailID != "" {
		if h := hitTestList(x, y); h != "" {
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


// bookProgress renders "在读 34%" / "读过" for book cards when progress
// information exists (progress percent or watched pages vs total).
func bookProgress(r *kb.Record) (string, bool) {
	if p, ok := r.Data["progress"].(float64); ok && p > 0 {
		if s, _ := r.Data["status"].(string); s != "" {
			return s + " " + fmt.Sprintf("%.0f%%", p), true
		}
	}
	return "", false
}

// zhAirStatus converts an AniList status into Chinese.
func zhAirStatus(s string) string {
	switch s {
	case "FINISHED":
		return "已完结"
	case "RELEASING":
		return "放送中"
	case "NOT_YET_RELEASED":
		return "未开播"
	case "CANCELLED":
		return "已取消"
	case "HIATUS":
		return "暂停放送"
	}
	return s
}
