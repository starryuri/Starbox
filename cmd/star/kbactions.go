//go:build windows

package main

import (
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strconv"
	"strings"


	"butler/internal/anime"

)
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

// runAnimeSearch queries Bangumi (Chinese-first) and falls back to AniList.
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
		if !bgmFallback {
			// primary path: Bangumi relay — Chinese titles/covers/scores
			if res, err := anime.BangumiSearch(q); err == nil && len(res) > 0 {
				for _, b := range res {
					searchResults = append(searchResults, anime.Result{
						ID:       b.ID,
						Title:    b.Title,
						Episodes: b.Eps,
						Score:    b.Score,
						Cover:    b.Cover,
						Year:     b.Year,
						URL:      b.URL,
					})
					if b.Cover != "" {
						ensureCover("sfv"+strconv.Itoa(b.ID), b.Cover)
					}
				}
				searchBusy = false
				pPostMessage.Call(hwndMain, uintptr(wmSearchDone), 0, 0)
				return
			}
			// Bangumi empty/unreachable -> AniList fallback for this session
			bgmFallback = true
		}
		// fallback: AniList (English metadata)
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
	rec, err := st.Add("anime", data)
	if err != nil {
		SetError("添加失败：%v", err)
		return
	}
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
		if !bgmFallback {
		drawTextRect(dc, cx+16, gy, cw-32, 40, "（Bangumi 无结果）", fontBody, colDim, dtLeft)
	} else {
		drawTextRect(dc, cx+16, gy, cw-32, 40, "（未找到结果）", fontBody, colDim, dtLeft)
	}
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

