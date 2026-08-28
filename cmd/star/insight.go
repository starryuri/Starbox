//go:build windows

package main

import (
	"context"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strconv"
	"strings"
	"time"


	"butler/internal/anime"
	"butler/internal/config"
	"butler/internal/du"
	"butler/internal/githot"
	"butler/internal/rss"
	"butler/internal/settings"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
)
func loadBind() {
	bindStatus = ""
	recs, _ := st.List("connect")
	m := map[string]interface{}{}
	if len(recs) > 0 {
		m = recs[0].Data
	}
	acc, _ := m[bindKeys[curPlat]].(string)
	pass, _ := m[bindKeys[curPlat]+"_pass"].(string)
	// stored values are DPAPI-encrypted ("dpapi:<b64>"); plain values are
	// legacy from before encryption and are shown as-is
	if dec := settings.DPAPIUnprotect(pass); dec != "" {
		pass = dec
	}
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
	// passwords/tokens at rest are DPAPI-encrypted (user scope)
	m[bindKeys[curPlat]+"_pass"] = settings.DPAPIProtect(getText(hPass))
	var err error
	if len(recs) > 0 {
		_, err = st.Update("connect", recs[0].ID, m)
	} else {
		_, err = st.Add("connect", m)
	}
	if err != nil {
		SetError("保存绑定失败：%v", err)
	} else {
		setText(hHint, "已保存到本机")
		SetStatus("凭据已保存")
	}
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
		"link":  "",
	}
	if key != "" {
		data["key"] = key
	}
	_, _ = st.Add("notif", data)
}

// setNotifLink attaches a click-through link to the most recent notification
// with this dedupe key (used by airing reminders).
func setNotifLink(key, link string) {
	recs, _ := st.List("notif")
	for i := len(recs) - 1; i >= 0; i-- {
		if k, _ := recs[i].Data["key"].(string); k == key {
			d := copyMap(recs[i].Data)
			d["link"] = link
			_, _ = st.Update("notif", recs[i].ID, d)
			return
		}
	}
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
		page := "https://anilist.co/anime/" + strconv.Itoa(u.MediaID)
		notifyPush(key, "追更", u.Title+" 第 "+strconv.Itoa(u.Episode)+" 集", when+" 播出", u.AiringAt)
		setNotifLink(key, page)
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
			if it.Link != "" {
				setNotifLink("feed-"+key, it.Link)
			}
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

