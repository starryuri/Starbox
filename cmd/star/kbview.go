//go:build windows

package main

import (
	"bytes"
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

func kbCardMode() bool { return page == "kb" && kbCol == "anime" }

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
		drawTextRect(dc, c.x+6, ty+2, c.w-12, 40, c.title, fontCard, colFg, dtSingle|0x00008000)
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

// then generic lists) — mirrors the click handlers.
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

