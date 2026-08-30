//go:build windows

package main

import (
	"path/filepath"

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
	if insCacheText != "" && time.Since(insCacheAt) < 10*time.Minute {
		insText = insCacheText
		pPostMessage.Call(hwndMain, uintptr(wmInsight), 0, 0)
		return
	}
	if insBusy {
		return
	}
	insBusy = true
	setText(hInfo, "（正在获取 GitHub 热门…）")
	go func() {
		insText = insightInfo()
		insCacheText = insText
		insCacheAt = time.Now()
		if repos, err := githot.Trending(7, ""); err == nil {
			insTrendCache = repos
		}
		if bindToken != "" {
			if repos, err := githot.MyRepos(bindToken); err == nil {
				insMineCache = repos
			}
		}
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
	insCacheText = "" // explicit refresh: bypass cache
	insBusy = true
	setText(hInfo, "（正在刷新…）")
	go func() {
		insText = insightInfo()
		if repos, err := githot.Trending(7, ""); err == nil {
			insTrendCache = repos
		}
		if bindToken != "" {
			if repos, err := githot.MyRepos(bindToken); err == nil {
				insMineCache = repos
			}
		}
		insBusy = false
		webRefreshPage("insight")
		pPostMessage.Call(hwndMain, uintptr(wmAppRefreshNow), 0, 0)
	}()
}

// trending cache: GitHub API is rate-limited; cache for 10 minutes.
var (
	insCacheText string
	insCacheAt   time.Time
	insTrendCache []githot.Repo
	insMineCache  []githot.Repo
)

// disk scan results are expensive (full C:\ walk); cache for 10 minutes.
var (
	dskCacheBody  string
	dskCacheAt    time.Time
)

func loadDisk() {
	if dskBusy {
		return
	}
	if dskCacheBody != "" && time.Since(dskCacheAt) < 10*time.Minute {
		dskBody = dskCacheBody
		pPostMessage.Call(hwndMain, uintptr(wmDisk), 0, 0)
		return
	}
	dskBusy = true
	setText(hBody, "（正在扫描磁盘，可能需要几秒…）")
	go func() {
		dskBody = dirText()
		dskCacheBody = dskBody
		dskCacheAt = time.Now()
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
var kbCols = []string{"anime", "books", "notes"}
var kbColLabels = map[string]string{"anime": "番剧", "books": "书库", "notes": "笔记"}
var kbSecField = map[string]string{"anime": "status", "books": "status", "notes": "tags"}

// kbStatuses lists the status chips shown on each column's detail page.
var kbStatuses = map[string][]string{
	"anime": {"想追", "在看", "看过", "搁置"},
	"books": {"想读", "在读", "读过", "搁置"},
	"notes": {},
}


// migrateStudyIntoBooks folds the retired "study" column into "books" once.
// Statuses are remapped onto the book vocabulary; the old study.json is
// renamed to study.migrated so the data is never silently destroyed.
func migrateStudyIntoBooks() {
	marker := filepath.Join(curProfDir, "study.migrated")
	if _, err := osStat(marker); err == nil {
		return
	}
	recs, err := st.List("study")
	if err != nil {
		_ = osWriteFile(marker, []byte("err"), 0o644)
		return
	}
	moved := 0
	if len(recs) > 0 {
		books, _ := st.List("books")
		exist := map[string]bool{}
		for _, b := range books {
			ti, _ := b.Data["title"].(string)
			exist[ti] = true
		}
		for _, r := range recs {
			ti, _ := r.Data["title"].(string)
			if ti == "" || exist[ti] {
				continue
			}
			d := copyMap(r.Data)
			switch s, _ := d["status"].(string); s {
			case "规划中":
				d["status"] = "想读"
			case "进行中":
				d["status"] = "在读"
			case "已完成":
				d["status"] = "读过"
			case "已放弃":
				d["status"] = "搁置"
			}
			delete(d, "watched")
			_, _ = st.Add("books", d)
			moved++
		}
	}
	_ = osWriteFile(marker, []byte("ok"), 0o644)
	if moved > 0 {
		SetStatus("已将学习栏目 %d 条记录并入书库", moved)
	}
}

// diskPartInfos converts gopsutil partitions into the web payload shape.
func diskPartInfos() []diskPartJSON {
	out := []diskPartJSON{}
	parts, err := disk.Partitions(false)
	if err != nil {
		return out
	}
	for _, p := range parts {
		if u, err := disk.Usage(p.Mountpoint); err == nil && u.Total > 0 {
			out = append(out, diskPartJSON{
				Mount: p.Mountpoint,
				Pct:   u.UsedPercent,
				Used:  humanBytes(u.Used),
				Total: humanBytes(u.Total),
			})
		}
	}
	return out
}

// ---------- full-page web renderers (called from paintFragment) ----------

// diskWebShow renders the disk page through WebView2; returns false when
// WebView2 is unavailable (caller falls back to the GDI text body).
func diskWebShow() bool {
	if page != "disk" {
		return false
	}
	if diskCachePath == "" && diskLastScan.Parts == nil && !dskBusy {
		go diskScanAsync("", false)
	}
	diskWebMu.Lock()
	pl := diskLastScan
	ver := webPageVers["disk"]
	diskWebMu.Unlock()
	key := "disk|" + pl.Path + "|" + fmt.Sprintf("%d", ver) + "|" + fmt.Sprintf("%d", len(pl.Parts)) + "|" + fmt.Sprintf("%d", len(pl.Items))
	return webShowPage("disk", key, buildDiskHTML(pl))
}

// insightWebShow renders the insight page through WebView2.
func insightWebShow() bool {
	if page != "insight" {
		return false
	}
	loadBind()
	loadInsightAsync()
	pl := insightPayload{
		Bound: bindToken != "",
		Login: bindLogin,
		CPU:   ovStat[0],
		Mem:   ovStat[1],
		Up:    ovStat[2],
		Disk:  ovStat[3],
	}
	if insTrendCache != nil {
		pl.Trend = insTrendCache
	}
	if insMineCache != nil {
		pl.Mine = insMineCache
	}
	ver := webPageVers["insight"]
	key := "insight|" + fmt.Sprintf("%d", ver) + "|" + pl.Login + "|" + fmt.Sprintf("%d", len(pl.Trend)) + "|" + fmt.Sprintf("%d", len(pl.Mine)) + "|" + pl.CPU
	return webShowPage("insight", key, buildInsightHTML(pl))
}

// settingsWebShow renders the settings page through WebView2.
func settingsWebShow() bool {
	if page != "settings" {
		return false
	}
	stt := settingsLoad()
	sc := stt.UiScale
	if sc != 125 && sc != 150 {
		sc = 100
	}
	ver := webPageVers["settings"]
	key := "settings|" + fmt.Sprintf("%d", ver) + "|" + stt.QuitAction + "|" + fmt.Sprintf("%v", stt.AutoStart) + fmt.Sprintf("%v", stt.SilentStart) + "|" + fmt.Sprintf("%d", sc)
	return webShowPage("settings", key, buildSettingsHTML(sc, stt.QuitAction == "exit", stt.AutoStart, stt.SilentStart))
}

// loadInsightAsync starts the insight fetch if not already running.
// (Kept separate from loadInsight so the web renderer never calls setText
// on GDI controls that may be hidden.)
func loadInsightAsync() {
	if insCacheText != "" && time.Since(insCacheAt) < 10*time.Minute {
		return
	}
	if insBusy {
		return
	}
	insBusy = true
	go func() {
		insText = insightInfo()
		insCacheText = insText
		insCacheAt = time.Now()
		if repos, err := githot.Trending(7, ""); err == nil {
			insTrendCache = repos
		}
		if bindToken != "" {
			if repos, err := githot.MyRepos(bindToken); err == nil {
				insMineCache = repos
			}
		}
		insBusy = false
		pPostMessage.Call(hwndMain, uintptr(wmInsight), 0, 0)
	}()
}
