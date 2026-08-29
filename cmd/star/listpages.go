//go:build windows

package main

import (
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"sort"
	"strconv"


	"butler/internal/anime"
	"butler/internal/kb"

)
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
	recs, err := st.List(listColl())
	if err != nil {
		SetError("读取 %s 集合失败：%v", listColl(), err)
	}
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
			sub := "制作公司"
			if typ == "cv" {
				sub = "声优 / 配音"
			} else if typ != "studio" {
				sub = typ
			}
			listRows = append(listRows, listRow{id: r.ID, title: name, sub: sub, tag: tag})
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
	ry := top + 64
	rh := 112
	gap := 14
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
		rowHover := hoverAct == "row" && hoverID == row.id
		if row.accent {
			bg = colCard2
		}
		if rowHover {
			bg = colCard2
		}
		fillRectColor(dc, cx+12, y, cw-24, rh, bg)
		if row.accent {
			fillRectColor(dc, cx+12, y, 4, rh, colAcc)
		}
		// delete button (books/study/games/notes/favs rows), right side
		if listPage != "notify" {
			dbw, dbh := 72, 38
			dbx := cx + cw - 12 - dbw - 10
			dby := y + (rh-dbh)/2
			delFill := uintptr(colRed)
			if hoverAct == "rowdel" && hoverID == row.id {
				delFill = 0x0000D0
			}
			fillRectColor(dc, dbx, dby, dbw, dbh, delFill)
			drawTextRect(dc, dbx, dby, dbw, dbh, "删除", fontBody, colFg, dtSingle|dtVCenter|dtCenter)
			listHits = append(listHits, detHit{dbx, dby, dbw, dbh, "rowdel", row.id})
		}
		tx := cx + 24
		if row.tag != "" {
			tw := 72
			fillRectColor(dc, cx+24, y+16, tw, 30, colAcc)
			drawTextRect(dc, cx+24, y+16, tw, 30, row.tag, fontTiny, colOnAcc, dtSingle|dtVCenter|dtCenter)
			tx = cx + 24 + 84
		}
		rightPad := cw - 12 - (tx - cx) - 12
		if rightPad < 20 {
			rightPad = 20
		}
		drawTextRect(dc, tx, y+10, rightPad, 42, row.title, fontCard, colFg, dtSingle|0x00008000)
		drawTextRect(dc, tx, y+60, rightPad, 30, row.sub, fontBody, colDim, dtSingle)
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
			var link string
			if rec := findRec("notif", id); rec != nil {
				d := copyMap(rec.Data)
				d["read"] = true
				link, _ = d["link"].(string)
				_, _ = st.Update("notif", id, d)
			}
			refreshList()
			pInvalidateRect.Call(hwndMain, 0, 1)
			if link != "" {
				openURL(link)
			}
		} else if listPage == "favs" {
			favDetailID = id
			favWorks = nil
			favEntName, favEntType, favEntImage = "", "", ""
			loadFavWorks()
			pInvalidateRect.Call(hwndMain, 0, 1)
		}
	case "rowdel":
		coll := listColl()
		name := ""
		if rec := findRec(coll, id); rec != nil {
			name, _ = rec.Data["title"].(string)
			if name == "" {
				name, _ = rec.Data["name"].(string)
			}
		}
		if confirmBox("确定删除「"+name+"」？删除后无法恢复。", "删除确认") {
			_ = st.Delete(coll, id)
			if listPage == "favs" && favDetailID == id {
				favDetailID = ""
			}
			refreshList()
			pInvalidateRect.Call(hwndMain, 0, 1)
		}
		return
	case "favback":
		favDetailID = ""
		favScroll = 0
		pInvalidateRect.Call(hwndMain, 0, 1)
	case "favwork":
		// id = bangumi subject id of the work; add-or-jump into KB detail
		if n, err := strconv.Atoi(id); err == nil {
			openFavWork(n)
		}
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
	typ := favEntType
	bgmID := favBgmID(rec)
	alID := favAlID(rec)
	name := favEntName
	go func() {
		if bgmID > 0 {
			// Chinese-first: Bangumi person subjects (Chinese titles + covers)
			if subs, err := anime.BangumiPersonSubjects(bgmID); err == nil && len(subs) > 0 {
				for _, s := range subs {
					favWorks = append(favWorks, anime.Media{ID: s.ID, Title: s.NameCN, Cover: s.Image, CN: s.NameCN, BgmID: s.ID})
				}
			}
		}
		if len(favWorks) == 0 && name != "" {
			// no stored id (Xinyuu-sourced favourites): resolve by name
			if pid, _, e := anime.BangumiPersonSearch(name); e == nil && pid > 0 {
				if subs, err := anime.BangumiPersonSubjects(pid); err == nil {
					for _, s := range subs {
						favWorks = append(favWorks, anime.Media{ID: s.ID, Title: s.NameCN, Cover: s.Image, CN: s.NameCN, BgmID: s.ID})
					}
				}
			}
		}
		if len(favWorks) == 0 {
			// fallback: AniList by anilist id
			id := alID
			if typ == "cv" || typ == "staff" {
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
		} else if typ == "cv" || typ == "staff" {
			// fetch person image for the header
			if w, err := anime.GetStaff(bgmID); err == nil && w.Image != "" {
				favEntImage = w.Image
			}
		}
		for _, m := range favWorks {
			if m.Cover != "" {
				ensureCover("fvw" + strconv.Itoa(m.ID), m.Cover)
			}
		}
		favBusy = false
		_ = name
		pPostMessage.Call(hwndMain, uintptr(wmFavWorks), 0, 0)
	}()
}

// favBgmID reads the stored Bangumi person id from a fav record.
func favBgmID(rec *kb.Record) int {
	switch v := rec.Data["bgm_id"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
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
	bw, bh := scale(120), scale(46)
	fillRectColor(dc, cx+scale(16), top+scale(16), bw, bh, colCard2)
	drawTextRect(dc, cx+scale(16), top+scale(16), bw, bh, "← 返回", fontNav, colFg, dtSingle|dtVCenter|dtCenter)
	listHits = append(listHits, detHit{cx + scale(16), top + scale(16), bw, bh, "favback", ""})
	dw := scale(120)
	dx := cx + cw - scale(16) - dw
	fillRectColor(dc, dx, top+scale(16), dw, bh, colRed)
	drawTextRect(dc, dx, top+scale(16), dw, bh, "删除", fontNav, colFg, dtSingle|dtVCenter|dtCenter)
	listHits = append(listHits, detHit{dx, top + scale(16), dw, bh, "favdel", favDetailID})
	drawTextRect(dc, cx+scale(16)+bw+scale(12), top+scale(16), cw-bw-dw-scale(12)-scale(32), bh, favEntName, fontTitle, colFg, dtSingle|dtVCenter|0x00008000)
	gy := top + scale(84)
	drawTextRect(dc, cx+scale(16), gy, cw-scale(32), scale(32), "作品 ("+fmt.Sprintf("%d", len(favWorks))+")", fontNav, colDim, dtSingle)
	gy += scale(44)
	if favBusy {
		drawTextRect(dc, cx+scale(16), gy, cw-scale(32), scale(40), "（正在获取作品…）", fontBody, colDim, dtLeft)
		return
	}
	if len(favWorks) == 0 {
		drawTextRect(dc, cx+scale(16), gy, cw-scale(32), scale(40), "（暂无作品数据）", fontBody, colDim, dtLeft)
		return
	}
	const gap = 18
	minW := 190
	cols := (cw-scale(32)+gap)/(minW+gap)
	if cols < 1 {
		cols = 1
	}
	wW := (cw - scale(32) - (cols-1)*gap) / cols
	coverH := wW * 14 / 10
	titleH := scale(56)
	cardH := coverH + titleH
	// scroll clamp
	rows := (len(favWorks) + cols - 1) / cols
	totalH := rows*(cardH+gap)
	maxScroll := totalH - (bottom - gy)
	if maxScroll < 0 {
		maxScroll = 0
	}
	if favScroll > maxScroll {
		favScroll = maxScroll
	}
	if favScroll < 0 {
		favScroll = 0
	}
	for i, m := range favWorks {
		col := i % cols
		row := i / cols
		x := cx + scale(16) + col*(wW+gap)
		y := gy + row*(cardH+gap) - favScroll
		if y > bottom {
			break
		}
		if y+cardH < gy-scale(200) {
			continue
		}
		fillRectColor(dc, x, y, wW, cardH, colCard)
		ci := getCover("fvw" + strconv.Itoa(m.ID))
		if ci != nil && ci.loaded {
			drawStretch(dc, x, y, wW, coverH, ci)
		} else {
			fillRectColor(dc, x, y, wW, coverH, colCard2)
			drawTextRect(dc, x+scale(6), y+coverH/2-scale(20), wW-scale(12), scale(40), m.Title, fontBody, colDim, dtCenter|dtVCenter)
		}
		drawTextRect(dc, x+scale(6), y+coverH+scale(4), wW-scale(12), titleH-scale(8), m.Title, fontBody, colFg, dtWordBreak)
		listHits = append(listHits, detHit{x, y, wW, cardH, "favwork", strconv.Itoa(m.ID)})
	}
	if totalH > bottom-gy {
		drawScrollIndicator(dc, totalH, bottom-gy, favScroll, cx+cw-scale(10), 4)
	}
}


// openFavWork jumps to a works-grid title: reuse an existing KB record with
// the same bgm id, otherwise create one, then open the KB detail page.
func openFavWork(subjectID int) {
	// already in the library?
	if recs, err := st.List("anime"); err == nil {
		for _, r := range recs {
			if bid, _ := r.Data["bgm_id"].(float64); bid > 0 && int(bid) == subjectID {
				jumpToKBDetail(r.ID)
				return
			}
		}
	}
	// fetch Chinese metadata and add
	s, err := anime.BangumiGetDetail(subjectID)
	if err != nil {
		SetError("获取条目失败：%v", err)
		return
	}
	title := s.NameCN
	if title == "" {
		title = s.Name
	}
	data := map[string]interface{}{
		"title":   title,
		"status": "想追",
		"bgm_id":  float64(s.ID),
		"cover":  s.Images.Large,
		"link":   s.URL,
		"air_start": s.Date,
		"note":   s.Summary,
	}
	if s.Eps != nil && *s.Eps > 0 {
		data["total"] = *s.Eps
	}
	if s.Rating.Score > 0 {
		data["rate"] = s.Rating.Score
	}
	rec, err := st.Add("anime", data)
	if err != nil {
		SetError("添加失败：%v", err)
		return
	}
	if rec.ID != "" && s.Images.Large != "" {
		ensureCover(rec.ID, s.Images.Large)
	}
	SetStatus("已加入番剧库：%s", title)
	jumpToKBDetail(rec.ID)
}

// jumpToKBDetail switches to the KB anime tab and opens a record detail.
func jumpToKBDetail(id string) {
	page = "kb"
	kbCol = "anime"
	kbScroll = 0
	searchMode = false
	favDetailID = ""
	refreshKB()
	highlightNav()
	renderPage()
	onKBHit("card", id)
}