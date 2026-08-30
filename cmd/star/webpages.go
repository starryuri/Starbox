//go:build windows

// webpages.go — full-page WebView2 renderers for the disk / insight / settings
// pages. Each page is a self-contained HTML doc styled from the active theme;
// interactivity (drill-down, open in explorer, scale switching) flows back
// through the same postMessage bridge as the KB detail page.
package main

import (
	"sort"
	"strings"
	"sync"
	"time"

	"butler/internal/du"
	"butler/internal/githot"
)

// ---------- shared page chrome ----------

func pageCSS() string {
	bg, side, card, card2, acc, fg, dim, _ := themeColors()
	return "<style>" +
		"*{box-sizing:border-box;margin:0;padding:0}" +
		"body{background:" + side + ";color:" + fg + ";font:15px/1.6 'Segoe UI','Microsoft YaHei UI',sans-serif;-webkit-font-smoothing:antialiased}" +
		"::-webkit-scrollbar{width:10px}::-webkit-scrollbar-thumb{background:" + card2 + ";border-radius:5px}" +
		".wrap{max-width:1080px;margin:0 auto;padding:22px 26px 90px}" +
		"h2{font-size:20px;margin:26px 0 14px;font-weight:600}" +
		"h2:first-child{margin-top:0}" +
		".grid{display:flex;flex-wrap:wrap;gap:14px}" +
		".card{background:" + card + ";border-radius:12px;padding:18px 20px;box-shadow:0 4px 18px rgba(0,0,0,.16)}" +
		".muted{color:" + dim + "}" +
		".btn{display:inline-block;background:" + card2 + ";color:" + fg + ";border-radius:8px;padding:8px 18px;cursor:pointer;font-size:14px;border:none}" +
		".btn.acc{background:" + acc + ";color:" + bg + ";font-weight:600}" +
		".btn:hover{filter:brightness(1.12)}" +
		".bar{height:10px;border-radius:5px;background:" + card2 + ";overflow:hidden}" +
		".bar>i{display:block;height:100%;border-radius:5px;background:" + acc + "}" +
		".row{display:flex;align-items:center;gap:12px}" +
		"</style>"
}

// esc escapes for HTML output.
func esc(s string) string { return htmlEscape(s) }

// fmtGB renders bytes as human units.
func fmtGB(n int64) string { return humanBytes(uint64(n)) }

// ---------- disk page ----------

type diskPayload struct {
	Parts  []diskPartJSON `json:"parts"`
	Path   string         `json:"path"`
	Items  []du.Item      `json:"items"`
	ScanMs int64          `json:"scan_ms"`
	Err    string         `json:"err,omitempty"`
}

type diskPartJSON struct {
	Mount string  `json:"mount"`
	Pct   float64 `json:"pct"`
	Used  string  `json:"used"`
	Total string  `json:"total"`
}

var (
	diskWebMu     sync.Mutex
	diskLastScan  diskPayload
	diskCacheAt   time.Time
	diskScanBusy  bool
	diskCachePath string
)

// buildDiskHTML renders the disk page. Path=="" lists partitions; otherwise
// it lists the biggest children of path with bars (clickable to drill).
func buildDiskHTML(pl diskPayload) string {
	_, _, card, _, acc, _, dim, red := themeColors()
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">")
	sb.WriteString(pageCSS())
	sb.WriteString("<style>" +
		".part{width:290px;cursor:default}" +
		".part .big{font-size:26px;font-weight:700;margin:2px 0 10px}" +
		".item{background:" + card + ";border-radius:10px;padding:12px 16px;margin-bottom:10px;cursor:pointer}" +
		".item:hover{outline:2px solid " + acc + "}" +
		".item .name{font-weight:600;font-size:15px}" +
		".item .sz{color:" + dim + ";font-size:13px}" +
		".crumb{display:flex;gap:8px;flex-wrap:wrap;margin-bottom:16px}" +
		".crumb .btn{padding:5px 14px;font-size:13px}" +
		".warn{color:" + red + ";font-weight:600}" +
		"</style></head><body><div class='wrap'>")
	if len(pl.Parts) > 0 && pl.Path == "" {
		sb.WriteString("<h2>磁盘概览</h2><div class='grid'>")
		for _, p := range pl.Parts {
			warn := ""
			if p.Pct >= 90 {
				warn = " warn"
			}
			sb.WriteString("<div class='card part'><div class='muted'>卷 " + esc(p.Mount) + "</div>")
			sb.WriteString("<div class='big" + warn + "'>" + fmtF(p.Pct, 0) + "%</div>")
			sb.WriteString("<div class='bar'><i style='width:" + fmtF(p.Pct, 1) + "%'></i></div>")
			sb.WriteString("<div class='muted' style='margin-top:8px'>" + esc(p.Used) + " / " + esc(p.Total) + "</div>")
			sb.WriteString("<div style='margin-top:12px'><button class='btn' onclick=\"send('opendir','" + esc(p.Mount) + "\\\\')\">查看空间分布</button></div>")
			sb.WriteString("</div>")
		}
		sb.WriteString("</div>")
	}
	if pl.Path != "" {
		sb.WriteString("<h2>空间分布 <span class='muted' style='font-size:14px'>" + esc(pl.Path) + "</span></h2>")
		sb.WriteString("<div class='crumb'><button class='btn' onclick=\"send('opendir','')\">← 返回磁盘概览</button>")
		sb.WriteString("<button class='btn' onclick=\"send('opendir','" + esc(parentDir(pl.Path)) + "\\\\')\">← 上一级</button></div>")
		if pl.Err != "" {
			sb.WriteString("<div class='muted'>（无法扫描：" + esc(pl.Err) + "）</div>")
		}
		mx := int64(1)
		for _, it := range pl.Items {
			if it.Size > mx {
				mx = it.Size
			}
		}
		for _, it := range pl.Items {
			pct := float64(it.Size) * 100 / float64(mx)
			label := it.Name
			if it.IsDir {
				label += " ／"
			}
			openPath := strings.TrimRight(pl.Path, "\\/") + "\\" + it.Name
			sb.WriteString("<div class='item' onclick=\"send('opendir','" + esc(openPath) + "')\">")
			sb.WriteString("<div class='row'><div class='name'>" + esc(label) + "</div><div class='sz'>" + esc(fmtGB(int64(it.Size))) + "</div></div>")
			sb.WriteString("<div class='bar' style='margin-top:8px'><i style='width:" + fmtF(pct, 1) + "%'></i></div>")
			sb.WriteString("</div>")
		}
		if pl.ScanMs > 0 {
			sb.WriteString("<div class='muted' style='margin-top:6px'>扫描用时 " + fmtI(pl.ScanMs) + " ms · 点击条目继续下钻（文件调用系统打开）</div>")
		}
	}
	sb.WriteString("<script>function send(t,v){window.chrome.webview.postMessage(JSON.stringify({t:t,v:v}))}</script>")
	sb.WriteString("</div></body></html>")
	return sb.String()
}

func parentDir(p string) string {
	i := strings.LastIndexAny(p, "\\/")
	if i <= 2 {
		return p
	}
	return p[:i]
}

// openLocalFolder opens a folder in Explorer (or file with its app).
func openLocalFolder(path string) {
	if path == "" {
		return
	}
	shellExecuteOpen(path)
}

// diskDrillInto scans path and switches the web layer to the disk page.
func diskDrillInto(path string) {
	go diskScanAsync(path, true)
}

// diskScanAsync walks path (or all partitions when empty), caches the payload
// and posts a repaint on the UI thread.
func diskScanAsync(path string, force bool) {
	if diskScanBusy {
		return
	}
	diskScanBusy = true
	defer func() { diskScanBusy = false }()
	diskWebMu.Lock()
	if !force && diskCachePath == path && time.Since(diskCacheAt) < 5*time.Minute {
		diskWebMu.Unlock()
		pPostMessage.Call(hwndMain, uintptr(wmDiskWeb), 0, 0)
		return
	}
	diskWebMu.Unlock()
	pl := diskPayload{Path: path}
	if path == "" {
		for _, p := range diskPartInfos() {
			pl.Parts = append(pl.Parts, p)
		}
	} else {
		t0 := time.Now()
		items, err := du.Scan(path, 20)
		pl.ScanMs = time.Since(t0).Milliseconds()
		if err != nil {
			pl.Err = err.Error()
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Size > items[j].Size })
		pl.Items = items
	}
	diskWebMu.Lock()
	diskLastScan = pl
	diskCachePath = path
	diskCacheAt = time.Now()
	diskWebMu.Unlock()
	pPostMessage.Call(hwndMain, uintptr(wmDiskWeb), 0, 0)
}

// ---------- insight page ----------

type insightPayload struct {
	Bound bool          `json:"bound"`
	Login string        `json:"login,omitempty"`
	Trend []githot.Repo `json:"trend"`
	Mine  []githot.Repo `json:"mine"`
	CPU   string        `json:"cpu"`
	Mem   string        `json:"mem"`
	Up    string        `json:"up"`
	Disk  string        `json:"disk"`
	Err   string        `json:"err,omitempty"`
}

func buildInsightHTML(pl insightPayload) string {
	_, _, card, _, acc, _, dim, _ := themeColors()
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">")
	sb.WriteString(pageCSS())
	sb.WriteString("<style>" +
		".stat{width:210px;text-align:center}" +
		".stat .v{font-size:30px;font-weight:700;color:" + acc + "}" +
		".repo{background:" + card + ";border-radius:10px;padding:14px 18px;margin-bottom:10px}" +
		".repo .name{font-weight:600;cursor:pointer}" +
		".repo .name:hover{color:" + acc + "}" +
		".repo .desc{color:" + dim + ";font-size:13px;margin-top:4px}" +
		".stars{color:" + acc + ";font-weight:700}" +
		".lang{background:" + card + ";border-radius:6px;padding:2px 10px;font-size:12px;color:" + dim + "}" +
		"</style></head><body><div class='wrap'>")

	sb.WriteString("<h2>系统状态</h2><div class='grid'>")
	for _, s := range []struct{ k, v, note string }{
		{"CPU", pl.CPU, "处理器占用"},
		{"内存", pl.Mem, "内存占用"},
		{"开机", pl.Up, "已运行时长"},
		{"磁盘 C:", pl.Disk, "系统盘占用"},
	} {
		sb.WriteString("<div class='card stat'><div class='muted'>" + s.note + "</div><div class='v'>" + esc(s.v) + "</div><div class='muted'>" + s.k + "</div></div>")
	}
	sb.WriteString("</div>")

	sb.WriteString("<h2>GitHub 绑定</h2><div class='card'>")
	if pl.Bound {
		sb.WriteString("<div class='row'>已绑定 <b>@" + esc(pl.Login) + "</b><span class='muted'>（凭据加密保存在本机）</span>")
		sb.WriteString("<button class='btn' style='margin-left:auto' onclick=\"send('refreshins','')\">刷新我的仓库</button></div>")
	} else {
		sb.WriteString("<div class='row'><span class='muted'>在上方输入 GitHub 用户名与令牌后点「保存账号」即可绑定</span></div>")
	}
	sb.WriteString("</div>")

	sb.WriteString("<h2>GitHub 本周热门</h2>")
	if len(pl.Trend) == 0 {
		if pl.Err != "" {
			sb.WriteString("<div class='muted'>（获取失败：" + esc(pl.Err) + "）</div>")
		} else {
			sb.WriteString("<div class='muted'>（暂无数据）</div>")
		}
	}
	for _, r := range pl.Trend {
		sb.WriteString("<div class='repo'><div class='row'><span class='name' onclick=\"send('open','" + esc(r.URL) + "')\">" + esc(r.Name) + "</span>")
		sb.WriteString("<span class='stars'>★ " + fmtI(int64(r.Stars)) + "</span>")
		if r.Lang != "" {
			sb.WriteString("<span class='lang'>" + esc(r.Lang) + "</span>")
		}
		sb.WriteString("</div><div class='desc'>" + esc(r.Desc) + "</div></div>")
	}
	if len(pl.Mine) > 0 {
		sb.WriteString("<h2>我的仓库</h2>")
		for _, r := range pl.Mine {
			sb.WriteString("<div class='repo'><div class='row'><span class='name' onclick=\"send('open','" + esc(r.URL) + "')\">" + esc(r.Name) + "</span>")
			sb.WriteString("<span class='stars'>★ " + fmtI(int64(r.Stars)) + "</span></div></div>")
		}
	}
	sb.WriteString("<script>function send(t,v){window.chrome.webview.postMessage(JSON.stringify({t:t,v:v}))}</script>")
	sb.WriteString("</div></body></html>")
	return sb.String()
}

// ---------- settings page ----------

func buildSettingsHTML(scale int, quitExit, autoStart, silent bool) string {
	bg, _, card, card2, acc, fg, dim, _ := themeColors()
	_ = fg
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">")
	sb.WriteString(pageCSS())
	sb.WriteString("<style>" +
		".group{background:" + card + ";border-radius:12px;padding:18px 22px;margin-bottom:16px}" +
		".group h3{font-size:16px;margin-bottom:12px}" +
		".opt{display:flex;align-items:center;gap:12px;padding:9px 0;cursor:pointer}" +
		".opt:hover .t{color:" + acc + "}" +
		".opt .t{font-weight:600}" +
		".opt .d{color:" + dim + ";font-size:13px}" +
		".seg{display:flex;gap:8px;margin-top:6px}" +
		".seg .btn.on{background:" + acc + ";color:" + bg + ";font-weight:700}" +
		".radio{width:18px;height:18px;border-radius:50%;border:2px solid " + card2 + ";flex:none}" +
		".radio.on{border-color:" + acc + ";background:" + acc + "}" +
		".check{width:18px;height:18px;border-radius:5px;border:2px solid " + card2 + ";flex:none}" +
		".check.on{border-color:" + acc + ";background:" + acc + "}" +
		".ver{color:" + dim + ";font-size:13px;text-align:center;margin-top:22px}" +
		"</style></head><body><div class='wrap'>")

	sb.WriteString("<div class='group'><h3>界面外观</h3><div class='muted' style='margin-bottom:6px'>界面缩放（立即生效）</div><div class='seg'>")
	for _, s := range []int{100, 125, 150} {
		on := ""
		if scale == s {
			on = " on"
		}
		sb.WriteString("<button class='btn" + on + "' onclick=\"send('setscale','" + fmtI(int64(s)) + "')\">" + fmtI(int64(s)) + "%</button>")
	}
	sb.WriteString("</div><div class='muted' style='margin-top:8px'>主题（夜幕/樱花/晴昼）在左侧边栏下方切换。</div></div>")

	sb.WriteString("<div class='group'><h3>启动</h3>")
	sb.WriteString(settingsCheckRow("auto", "开机自启动", "注册到系统启动项（HKCU Run）", autoStart))
	sb.WriteString(settingsCheckRow("silent", "静默启动", "自启动时直接最小化到托盘", silent))
	sb.WriteString("</div>")

	sb.WriteString("<div class='group'><h3>关闭窗口时</h3>")
	sb.WriteString(settingsRadioRow("quit:tray", "最小化到托盘", "推荐：继续在后台提醒更新", !quitExit))
	sb.WriteString(settingsRadioRow("quit:exit", "完全退出", "关闭即结束程序", quitExit))
	sb.WriteString("</div>")

	sb.WriteString("<div class='ver'>星匣 STARBOX " + esc(appVersion) + " · 数据目录 " + esc(dataDir) + "<br>启动与关闭行为即时保存；身份管理在下方按钮区</div>")
	sb.WriteString("<script>function send(t,v){window.chrome.webview.postMessage(JSON.stringify({t:t,v:v}))}</script>")
	sb.WriteString("</div></body></html>")
	return sb.String()
}

func settingsCheckRow(kind, title, desc string, on bool) string {
	cls := "check"
	if on {
		cls += " on"
	}
	mark := ""
	if on {
		mark = "✓"
	}
	return "<div class='opt' onclick=\"send('set','" + kind + "')\"><span class='" + cls + "'>" + mark + "</span><span class='t'>" + title + "</span><span class='d'>" + desc + "</span></div>"
}

func settingsRadioRow(val, title, desc string, on bool) string {
	cls := "radio"
	if on {
		cls += " on"
	}
	return "<div class='opt' onclick=\"send('set','" + val + "')\"><span class='" + cls + "'></span><span class='t'>" + title + "</span><span class='d'>" + desc + "</span></div>"
}

// ---------- web-side settings actions ----------

// webSettingsAction applies a <set> message from the settings page and
// persists immediately (no separate save step for these rows).
func webSettingsAction(kind string) {
	stt := settingsLoad()
	switch {
	case kind == "auto":
		stt.AutoStart = !stt.AutoStart
		exe, _ := osExecutable()
		if err := settingsSetAutoStart(stt.AutoStart, exe); err != nil {
			SetError("自启动设置失败：%v", err)
			return
		}
		SetStatus("已%s开机自启动", map[bool]string{true: "开启", false: "关闭"}[stt.AutoStart])
	case kind == "silent":
		stt.SilentStart = !stt.SilentStart
		SetStatus("已%s静默启动", map[bool]string{true: "开启", false: "关闭"}[stt.SilentStart])
	case strings.HasPrefix(kind, "quit:"):
		if strings.HasSuffix(kind, "exit") {
			stt.QuitAction = "exit"
		} else {
			stt.QuitAction = "tray"
		}
		applyTraySettings()
	}
	_ = settingsSave(stt)
	webRefreshPage("settings")
	pInvalidateRect.Call(hwndMain, 0, 1)
}
