package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"butler/internal/anime"
	"butler/internal/account"
	"butler/internal/config"
	desk "butler/internal/desktop"
	"butler/internal/githot"
	"butler/internal/httpd"
	"butler/internal/kb"
	"butler/internal/monitor"
	"butler/internal/rules"
	"butler/internal/sched"
	"butler/internal/settings"
	"butler/internal/tray"
)

func main() {
	cfgPath := flag.String("config", "config.json", "path to config file")
	openUI := flag.Bool("open", false, "open the dashboard in your default browser on start")
	asDesktop := flag.Bool("desktop", false, "open a native desktop window (starts the server if needed)")
	asWindow := flag.Bool("window", false, "open a native desktop window pointing to an already-running server")
	trayOn := flag.Bool("tray", true, "show a system tray icon (server mode)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	url := "http://" + cfg.HTTPAddr + "/"
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st := monitor.New()
	exe, _ := os.Executable()
	dataDir := filepath.Join(filepath.Dir(exe), "data")
	kstore := kb.New(dataDir)
	acc, _ := account.New(dataDir)

	// Apply app-level settings at startup (开机自启动 & 退出行为).
	cfgSettings := settings.Load(dataDir)
	if cfgSettings.AutoStart {
		_ = settings.SetAutoStart(true, exe)
	}

	// allStores returns the guest store plus every registered account's store, so
	// system notifications (rules / airing / trending / RSS-email) reach everyone.
	allStores := func() []*kb.Store {
		out := []*kb.Store{kstore}
		for _, uid := range acc.UserIDs() {
			out = append(out, kb.New(filepath.Join(dataDir, "users", uid)))
		}
		return out
	}

	// window mode: start the server if it isn't already running, then show the window.
	if *asWindow {
		if !httpAlive(cfg.HTTPAddr) {
			sched.Run(ctx, cfg, st)
			httpd.Start(cfg.HTTPAddr, st, kstore, dataDir, acc)
		}
		desk.Open(url)
		return
	}

	sched.Run(ctx, cfg, st)
	go pushNotifications(ctx, st, allStores)
	go trackAiring(ctx, allStores)
	go trackTrending(ctx, allStores)
	go rules.Run(ctx, func() map[string]string { return st.Snapshot() }, allStores)

	if cfg.HTTPAddr != "" {
		httpd.Start(cfg.HTTPAddr, st, kstore, dataDir, acc)
		if *openUI {
			openBrowser(url)
		}
	}

	if *asDesktop {
		// 退出行为："tray" 则在窗口关闭后收纳到托盘继续运行；"exit" 则关闭即退出。
		if cfgSettings.QuitAction != "exit" {
			go tray.Run()
		}
		desk.Open(url)
		if cfgSettings.QuitAction == "exit" {
			return
		}
		<-ctx.Done()
		return
	}

	if *trayOn {
		tray.Run() // blocks until the user quits from the tray
	} else {
		<-ctx.Done()
	}
	log.Println("shutting down")
}

// openBrowser opens url in the default browser (Windows).
func trackTrending(ctx context.Context, stores func() []*kb.Store) {
	seen := map[string]bool{}
	first := true
	run := func() {
		stos := stores()
		// load known repo names from the first store that has a trending record
		for _, s := range stos {
			recs, _ := s.List("trending")
			if len(recs) > 0 {
				if ns, ok := recs[0].Data["names"].([]interface{}); ok {
					for _, n := range ns {
						if str, ok := n.(string); ok {
							seen[str] = true
						}
					}
				}
				break
			}
		}
		// github token from any store that has one
		tok := ""
		for _, s := range stos {
			recs, _ := s.List("connect")
			if len(recs) > 0 {
				if t, ok := recs[0].Data["ghToken"].(string); ok && t != "" {
					tok = t
					break
				}
			}
		}
		repos, err := githot.Trending(7, tok)
		if err != nil {
			return
		}
		newCount := 0
		for _, r := range repos {
			if !seen[r.Name] {
				seen[r.Name] = true
				newCount++
			}
		}
		names := make([]string, 0, len(seen))
		for n := range seen {
			names = append(names, n)
		}
		for _, s := range stos {
			recs, _ := s.List("trending")
			data := map[string]interface{}{"names": names}
			if len(recs) > 0 {
				_, _ = s.Update("trending", recs[0].ID, data)
			} else {
				_, _ = s.Add("trending", data)
			}
			if !first && newCount > 0 {
				_, _ = s.Add("notif", map[string]interface{}{
					"type": "规则", "title": "GitHub 热门更新",
					"body": fmt.Sprintf("新增 %d 个热门仓库", newCount),
					"unix": time.Now().Unix(), "read": false,
				})
			}
		}
		first = false
	}
	run()
	tick := time.NewTicker(24 * time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			run()
		}
	}
}

func trackAiring(ctx context.Context, stores func() []*kb.Store) {
	notified := map[string]bool{}
	run := func() {
		stos := stores()
		for si, s := range stos {
			recs, err := s.List("anime")
			if err != nil {
				continue
			}
			var ids []int
			for _, r := range recs {
				if r.Data["status"] == "在追" {
					if id, ok := r.Data["anilist_id"].(float64); ok && id > 0 {
						ids = append(ids, int(id))
					}
				}
			}
			if len(ids) == 0 {
				continue
			}
			ups, err := anime.Upcoming(ids)
			if err != nil {
				continue
			}
			now := time.Now().Unix()
			for _, u := range ups {
				if u.AiringAt > now && u.AiringAt-now < 12*3600 {
					key := fmt.Sprintf("%d:%d:%d", si, u.MediaID, u.Episode)
					if !notified[key] {
						notified[key] = true
						_, _ = s.Add("notif", map[string]interface{}{
							"type": "追更", "title": u.Title,
							"body": fmt.Sprintf("第 %d 集即将更新", u.Episode),
							"unix": time.Now().Unix(), "read": false,
						})
					}
				}
			}
		}
	}
	run()
	tick := time.NewTicker(30 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			run()
		}
	}
}

// httpAlive reports whether a BUTLER server is already serving on addr.
func httpAlive(addr string) bool {
	if addr == "" {
		return false
	}
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func openBrowser(url string) {
	go func() {
		if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start(); err != nil {
			log.Printf("open browser: %v", err)
		}
	}()
}

// pushNotifications watches the monitor snapshot every 30s and generates
// notifications for new RSS content and unread email.
func pushNotifications(ctx context.Context, st *monitor.State, stores func() []*kb.Store) {
	lastNew := map[string]int{}
	lastUnread := map[string]int{}
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	reNew := regexp.MustCompile(`new=(\d+)`)
	reUnread := regexp.MustCompile(`unread=(\d+)`)
	add := func(ntype, title, body string) {
		for _, s := range stores() {
			_, _ = s.Add("notif", map[string]interface{}{
				"type": ntype, "title": title, "body": body,
				"unix": time.Now().Unix(), "read": false,
			})
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for k, v := range st.Snapshot() {
				if strings.HasPrefix(k, "rss_") {
					if m := reNew.FindStringSubmatch(v); len(m) > 1 {
						if n, _ := strconv.Atoi(m[1]); n > 0 && n != lastNew[k] {
							lastNew[k] = n
							add("订阅", k, fmt.Sprintf("%s 更新了 %d 条内容", k, n))
						}
					}
				} else if strings.Contains(k, "email") {
					if m := reUnread.FindStringSubmatch(v); len(m) > 1 {
						if n, _ := strconv.Atoi(m[1]); n > 0 && n != lastUnread[k] {
							lastUnread[k] = n
							add("邮箱", "有新邮件", fmt.Sprintf("有 %d 封未读邮件", n))
						}
					}
				}
			}
		}
	}
}
