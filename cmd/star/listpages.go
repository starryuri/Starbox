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
			dbw, dbh := 64, 34
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
			tw := 64
			fillRectColor(dc, cx+24, y+10, tw, 26, colAcc)
			drawTextRect(dc, cx+24, y+10, tw, 26, row.tag, fontTiny, colOnAcc, dtSingle|dtVCenter|dtCenter)
			tx = cx + 24 + 72
		}
		rightPad := cw - 12 - (tx - cx) - 12
		if rightPad < 20 {
			rightPad = 20
		}
		drawTextRect(dc, tx, y+8, rightPad, 30, row.title, fontCard, colFg, dtSingle|0x00008000)
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
	typ := favEntType
	alID := favAlID(rec)
	go func() {
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

