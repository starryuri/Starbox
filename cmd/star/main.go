// Starbox — native UI build (Gio). This is the beginning of the WebView2 -> native
// rewrite. It reuses the internal backend packages directly (no HTTP layer for the
// UI) and renders a native window with a sidebar + pages.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/shirou/gopsutil/v3/disk"

	"butler/internal/account"
	"butler/internal/config"
	"butler/internal/kb"
	"butler/internal/monitor"
	"butler/internal/sched"
	"butler/internal/settings"
)

const dataDirName = "data"

type App struct {
	ctx    context.Context
	cancel context.CancelFunc
	dataDir string
	st    *monitor.State
	acc   *account.Manager
	cfg   *config.Config
	set   settings.Settings
	exe   string

	curUser account.User
	curTok  string

	page string
	nav  map[string]*widget.Clickable

	drives []driveInfo

	nickEd, passEd *widget.Editor
	acctMsg        string

	loginBtn, regBtn, logoutBtn, accountBtn *widget.Clickable

	notifList   []kb.Record
	notifLoaded bool
	notifAllRead, notifClear *widget.Clickable

	favList   []kb.Record
	favLoaded bool

	kbTab    string
	kbTabs   map[string]*widget.Clickable
	addTitle *widget.Editor
	kbAdd    *widget.Clickable
	kbEnts   []kb.Record
	kbLoaded bool
	kbMsg    string

	autostart widget.Bool
	saveMsg   string
}

type driveInfo struct {
	name     string
	total    uint64
	used     uint64
	free     uint64
	usedPct  float64
}

var pages = []string{"overview", "disk", "rss", "insight", "kb", "favs", "notify", "rules", "settings"}

var pageLabels = map[string]string{
	"overview": "概况", "disk": "磁盘", "rss": "订阅", "insight": "情报",
	"kb": "知识库", "favs": "收藏", "notify": "通知", "rules": "规则", "settings": "设置",
}

func newApp(dataDir string) *App {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	cfg, err := config.Load(filepath.Join(dataDir, "..", "config.json"))
	if err != nil {
		cfg = &config.Config{HTTPAddr: "127.0.0.1:8765"}
	}
	st := monitor.New()
	sched.Run(ctx, cfg, st)
	acc, _ := account.New(dataDir)
	exe, _ := os.Executable()
	a := &App{
		ctx: ctx, cancel: cancel, dataDir: dataDir, st: st, acc: acc, cfg: cfg,
		set: settings.Load(dataDir), exe: exe, page: "overview",
		nav:      map[string]*widget.Clickable{},
		nickEd:   &widget.Editor{SingleLine: true},
		passEd:   &widget.Editor{SingleLine: true, Submit: true},
		addTitle: &widget.Editor{SingleLine: true},
		kbTab:    "anime",
		kbTabs:   map[string]*widget.Clickable{},
		loginBtn: &widget.Clickable{}, regBtn: &widget.Clickable{}, logoutBtn: &widget.Clickable{},
		accountBtn: &widget.Clickable{}, kbAdd: &widget.Clickable{},
		notifAllRead: &widget.Clickable{}, notifClear: &widget.Clickable{},
	}
	for _, p := range pages {
		a.nav[p] = &widget.Clickable{}
	}
	for _, t := range []string{"anime", "books", "study", "games", "notes"} {
		a.kbTabs[t] = &widget.Clickable{}
	}
	if a.set.AutoStart {
		_ = settings.SetAutoStart(true, exe)
	}
	a.autostart.Value = a.set.AutoStart
	return a
}

func (a *App) store() *kb.Store {
	if a.curUser.ID != "" {
		return kb.New(filepath.Join(a.dataDir, "users", a.curUser.ID))
	}
	return kb.New(a.dataDir)
}

func loadCJKFont(coll []font.FontFace) []font.FontFace {
	candidates := []string{
		`C:\Windows\Fonts\msyh.ttc`, `C:\Windows\Fonts\msyh.ttf`,
		`C:\Windows\Fonts\simhei.ttf`, `C:\Windows\Fonts\Deng.ttf`,
	}
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if faces, err := opentype.ParseCollection(b); err == nil {
			return append(coll, faces...)
		}
		if f, err := opentype.Parse(b); err == nil {
			return append(coll, font.FontFace{Font: font.Font{Typeface: "cjk"}, Face: f})
		}
	}
	return coll
}

func main() {
	exe, _ := os.Executable()
	dataDir := filepath.Join(filepath.Dir(exe), dataDirName)
	a := newApp(dataDir)

	go func() {
		w := new(app.Window)
		var ops op.Ops
		var th *material.Theme
		for {
			e := w.Event()
			switch ev := e.(type) {
			case app.DestroyEvent:
				if ev.Err != nil {
					log.Fatal(ev.Err)
				}
				a.cancel()
				return
			case app.FrameEvent:
				if th == nil {
					th = material.NewTheme()
					coll := loadCJKFont(gofont.Collection())
					th.Shaper = text.NewShaper(text.WithCollection(coll))
					a.refreshDrives()
				}
				gtx := app.NewContext(&ops, ev)
				a.render(gtx, th)
				ev.Frame(gtx.Ops)
			}
		}
	}()

	app.Main()
}

func (a *App) refreshDrives() {
	a.drives = nil
	parts, err := disk.Partitions(true)
	if err != nil {
		return
	}
	for _, p := range parts {
		u, err := disk.Usage(p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		a.drives = append(a.drives, driveInfo{name: p.Mountpoint, total: u.Total, used: u.Used, free: u.Free, usedPct: u.UsedPercent})
	}
}

// btnClick renders a material button and reports whether it was clicked. In Gio a
// clickable must be laid out before Clicked() reflects the gesture, so they are
// done together here.
func (a *App) btnClick(th *material.Theme, gtx layout.Context, b *widget.Clickable, txt string) (bool, layout.Dimensions) {
	d := material.Button(th, b, txt).Layout(gtx)
	return b.Clicked(gtx), d
}

func (a *App) render(gtx layout.Context, th *material.Theme) {
	layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.renderSidebar(gtx, th)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(16), Left: unit.Dp(20), Right: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return a.renderPage(gtx, th, a.page)
			})
		}),
	)
}

func (a *App) renderSidebar(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(14), Left: unit.Dp(12), Right: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return material.H6(th, "星匣 STARBOX").Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return material.Caption(th, "你的次元 · 收于一匣").Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return (&layout.List{}).Layout(gtx, len(pages), func(gtx layout.Context, i int) layout.Dimensions {
					p := pages[i]
					btn := a.nav[p]
					clicked, d := a.btnClick(th, gtx, btn, pageLabels[p])
					if clicked {
						a.page = p
					}
					return d
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				label := "登录 / 注册"
				if a.curUser.ID != "" {
					label = "👤 " + a.curUser.Nickname
				}
				clicked, d := a.btnClick(th, gtx, a.accountBtn, label)
				if clicked {
					a.page = "account"
				}
				return d
			}),
		)
	})
}

func (a *App) renderPage(gtx layout.Context, th *material.Theme, p string) layout.Dimensions {
	switch p {
	case "overview":
		return a.renderOverview(gtx, th)
	case "settings":
		return a.renderSettings(gtx, th)
	case "disk":
		return a.renderDisk(gtx, th)
	case "account":
		return a.renderAccount(gtx, th)
	case "kb":
		return a.renderKB(gtx, th)
	case "notify":
		return a.renderNotif(gtx, th)
	case "rss":
		return a.renderRSS(gtx, th)
	case "favs":
		return a.renderFavs(gtx, th)
	default:
		return material.Caption(th, "模块「"+p+"」正在迁移到原生界面……").Layout(gtx)
	}
}

func humanBytes(n uint64) string {
	const u = 1024
	if n < u {
		return strconv.FormatUint(n, 10) + " B"
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
	return strconv.FormatFloat(val, 'f', prec, 64) + " " + units[i]
}

func lineKV(t string) map[string]string {
	o := map[string]string{}
	for _, l := range strings.Split(t, "\n") {
		if i := strings.Index(l, "="); i > 0 {
			o[strings.TrimSpace(l[:i])] = strings.TrimSpace(l[i+1:])
		}
	}
	return o
}

func (a *App) renderOverview(gtx layout.Context, th *material.Theme) layout.Dimensions {
	m := lineKV(a.st.Get("system_metrics"))
	cpu := m["cpu"]
	mem := m["mem"]
	up := m["uptime"]
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.H5(th, "概览").Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Body1(th, "CPU: "+cpu+"   |   内存: "+mem+"   |   运行: "+up).Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(18)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.H6(th, "磁盘").Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return (&layout.List{}).Layout(gtx, len(a.drives), func(gtx layout.Context, i int) layout.Dimensions {
				d := a.drives[i]
				return material.Body2(th, d.name+"  "+strconv.FormatFloat(d.usedPct, 'f', 1, 64)+"%  "+humanBytes(d.used)+" / "+humanBytes(d.total)).Layout(gtx)
			})
		}),
	)
}

func (a *App) renderSettings(gtx layout.Context, th *material.Theme) layout.Dimensions {
	var saveClicked bool
	btn := widget.Clickable{}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.H5(th, "设置").Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Switch(th, &a.autostart, "开机自启动").Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Body2(th, "开机自启动").Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Button(th, &btn, "保存设置").Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			saveClicked = btn.Clicked(gtx)
			if saveClicked {
				a.set.AutoStart = a.autostart.Value
				a.set.QuitAction = "tray"
				_ = settings.SetAutoStart(a.set.AutoStart, a.exe)
				_ = settings.Save(a.dataDir, a.set)
				a.saveMsg = "已保存"
			}
			return material.Caption(th, a.saveMsg).Layout(gtx)
		}),
	)
}

func (a *App) renderDisk(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.H5(th, "磁盘").Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Caption(th, "目录占用视图（原生实现后续补充）").Layout(gtx)
		}),
	)
}

func (a *App) renderAccount(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.H5(th, "账户").Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if a.curUser.ID != "" {
				return material.Body1(th, "已登录："+a.curUser.Nickname).Layout(gtx)
			}
			return material.Caption(th, "注册 / 登录本地账户，数据保存在本机。").Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if a.curUser.ID != "" {
				clicked, d := a.btnClick(th, gtx, a.logoutBtn, "退出登录")
				if clicked {
					a.acc.Logout(a.curTok)
					a.curUser = account.User{}
					a.curTok = ""
					a.acctMsg = "已退出登录"
				}
				return d
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.Editor(th, a.nickEd, "昵称").Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.Editor(th, a.passEd, "密码（至少 4 位）").Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							clicked, d := a.btnClick(th, gtx, a.regBtn, "注册")
							if clicked {
								u, tok, err := a.acc.Register(strings.TrimSpace(a.nickEd.Text()), a.passEd.Text())
								if err != nil {
									a.acctMsg = err.Error()
								} else {
									a.curUser = u
									a.curTok = tok
									a.acctMsg = "注册成功，已登录"
								}
							}
							return d
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							clicked, d := a.btnClick(th, gtx, a.loginBtn, "登录")
							if clicked {
								u, tok, err := a.acc.Login(strings.TrimSpace(a.nickEd.Text()), a.passEd.Text())
								if err != nil {
									a.acctMsg = err.Error()
								} else {
									a.curUser = u
									a.curTok = tok
									a.acctMsg = "已登录"
								}
							}
							return d
						}),
					)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.Caption(th, a.acctMsg).Layout(gtx)
				}),
			)
		}),
	)
}

var kbTabLabels = map[string]string{"anime": "番剧", "books": "书库", "study": "学习", "games": "游戏", "notes": "笔记"}
var kbTabHint  = map[string]string{"anime": "番剧标题", "books": "书名", "study": "学习主题", "games": "游戏名称", "notes": "笔记标题"}
var kbSecField = map[string]string{"anime": "status", "books": "author", "study": "status", "games": "platform", "notes": "tags"}

func (a *App) loadKB() {
	recs, _ := a.store().List(a.kbTab)
	a.kbEnts = recs
}

func (a *App) renderKB(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if !a.kbLoaded {
		a.loadKB()
		a.kbLoaded = true
	}
	keys := []string{"anime", "books", "study", "games", "notes"}
	row := make([]layout.FlexChild, 0, len(keys))
	for _, k := range keys {
		k := k
		row = append(row, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			clicked, d := a.btnClick(th, gtx, a.kbTabs[k], kbTabLabels[k])
			if clicked && a.kbTab != k {
				a.kbTab = k
				a.loadKB()
			}
			return d
		}))
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.H5(th, "知识库").Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, row...)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return material.Editor(th, a.addTitle, kbTabHint[a.kbTab]).Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					clicked, d := a.btnClick(th, gtx, a.kbAdd, "＋ 添加")
					if clicked {
						title := strings.TrimSpace(a.addTitle.Text())
						if title != "" {
							data := map[string]any{"title": title}
							switch a.kbTab {
							case "anime":
								data["status"] = "想追"
							case "study":
								data["status"] = "规划中"
							case "games":
								data["status"] = "想玩"
							}
							_, _ = a.store().Add(a.kbTab, data)
							a.addTitle.SetText("")
							a.loadKB()
						}
					}
					return d
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(a.kbEnts) == 0 {
				return material.Caption(th, "暂无条目").Layout(gtx)
			}
			return (&layout.List{}).Layout(gtx, len(a.kbEnts), func(gtx layout.Context, i int) layout.Dimensions {
				r := a.kbEnts[i]
				title, _ := r.Data["title"].(string)
				sec, _ := r.Data[kbSecField[a.kbTab]].(string)
				line := "•  " + title
				if sec != "" {
					line += "   [" + sec + "]"
				}
				return material.Body1(th, line).Layout(gtx)
			})
		}),
	)
}

func (a *App) renderNotif(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if !a.notifLoaded {
		a.notifList, _ = a.store().List("notif")
		a.notifLoaded = true
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.H5(th, "通知中心").Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					clicked, d := a.btnClick(th, gtx, a.notifAllRead, "全部标为已读")
					if clicked {
						list, _ := a.store().List("notif")
						for _, r := range list {
							dd := map[string]any{}
							for k, v := range r.Data {
								dd[k] = v
							}
							dd["read"] = true
							_, _ = a.store().Update("notif", r.ID, dd)
						}
						a.notifList, _ = a.store().List("notif")
					}
					return d
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					clicked, d := a.btnClick(th, gtx, a.notifClear, "清除已读")
					if clicked {
						list, _ := a.store().List("notif")
						for _, r := range list {
							if rd, ok := r.Data["read"].(bool); ok && rd {
								_ = a.store().Delete("notif", r.ID)
							}
						}
						a.notifList, _ = a.store().List("notif")
					}
					return d
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(a.notifList) == 0 {
				return material.Caption(th, "暂无通知").Layout(gtx)
			}
			return (&layout.List{}).Layout(gtx, len(a.notifList), func(gtx layout.Context, i int) layout.Dimensions {
				r := a.notifList[i]
				title, _ := r.Data["title"].(string)
				body, _ := r.Data["body"].(string)
				unix, _ := r.Data["unix"].(float64)
				line := "•  " + title
				if body != "" {
					line += " — " + body
				}
				if unix > 0 {
					line += "   (" + time.Unix(int64(unix), 0).Format("01-02 15:04") + ")"
				}
				return material.Body1(th, line).Layout(gtx)
			})
		}),
	)
}

func (a *App) renderRSS(gtx layout.Context, th *material.Theme) layout.Dimensions {
	snap := a.st.Snapshot()
	type entry struct{ feed, title string }
	var es []entry
	for k, v := range snap {
		if !strings.HasPrefix(k, "rss_") {
			continue
		}
		lines := strings.Split(v, "\n")
		head := ""
		if len(lines) > 0 {
			head = lines[0]
		}
		for _, l := range lines[1:] {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "- ") {
				es = append(es, entry{feed: head, title: strings.TrimSpace(strings.TrimPrefix(l, "-"))})
			}
		}
	}
	if len(es) == 0 {
		es = append(es, entry{feed: "", title: "暂无订阅内容（请确认 config.json 已配置 rss 任务）"})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.H5(th, "订阅").Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return (&layout.List{}).Layout(gtx, len(es), func(gtx layout.Context, i int) layout.Dimensions {
				return material.Body1(th, "·  "+es[i].title).Layout(gtx)
			})
		}),
	)
}

func (a *App) renderFavs(gtx layout.Context, th *material.Theme) layout.Dimensions {
	if !a.favLoaded {
		a.favList, _ = a.store().List("favs")
		a.favLoaded = true
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.H5(th, "收藏").Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(a.favList) == 0 {
				return material.Caption(th, "暂无收藏的声优 / 制作公司").Layout(gtx)
			}
			return (&layout.List{}).Layout(gtx, len(a.favList), func(gtx layout.Context, i int) layout.Dimensions {
				r := a.favList[i]
				typ, _ := r.Data["type"].(string)
				name, _ := r.Data["name"].(string)
				label := "声优"
				if typ == "studio" {
					label = "制作公司"
				}
				return material.Body1(th, "·  ["+label+"]  "+name).Layout(gtx)
			})
		}),
	)
}
