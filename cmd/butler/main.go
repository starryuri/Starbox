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
	"unsafe"

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

	"golang.org/x/sys/windows"
)

// ---- single instance ----
var (
	k32           = windows.NewLazyDLL("kernel32.dll")
	mCreateMutex  = k32.NewProc("CreateMutexW")
	mCloseHandle  = k32.NewProc("CloseHandle")
	u32           = windows.NewLazyDLL("user32.dll")
	wFindWindow   = u32.NewProc("FindWindowW")
	wSetForeground = u32.NewProc("SetForegroundWindow")
	wShowWindow   = u32.NewProc("ShowWindow")
)

const singleInstanceName = "STARBOX_SingleInstance"

// acquireSingle claims a named mutex so only one STARBOX server/tray instance runs.
// Returns (true, release) if this process is the primary; (false, nil) if another
// instance is already running.
func acquireSingle() (bool, func()) {
	name, err := windows.UTF16PtrFromString(singleInstanceName)
	if err != nil {
		return true, func() {}
	}
	r, _, ecall := mCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if r == 0 {
		// Could not create/open the mutex; allow running rather than block.
		return true, func() {}
	}
	if ecall == windows.ERROR_ALREADY_EXISTS {
		mCloseHandle.Call(r)
		return false, nil
	}
	return true, func() { mCloseHandle.Call(r) }
}

// bringToFront finds the STARBOX desktop window by title and restores + focuses
// it. Returns false if no window is currently open.
func bringToFront(title string) bool {
	t, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return false
	}
	hwnd, _, _ := wFindWindow.Call(0, uintptr(unsafe.Pointer(t)))
	if hwnd == 0 {
		return false
	}
	wShowWindow.Call(hwnd, 9) // SW_RESTORE
	wSetForeground.Call(hwnd)
	return true
}

func main() {
	cfgPath := flag.String("config", "config.json", "path to config file")
	openUI := flag.Bool("open", false, "open the dashboard in your default browser on start")
	asDesktop := flag.Bool("desktop", false, "open a native desktop window (starts the server if needed)")
	asWindow := flag.Bool("window", false, "open a native desktop window pointing to an already-running server")
	trayOn := flag.Bool("tray", false, "run silently in the system tray without opening the window")
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

	// Limit to a single running instance. If a STARBOX process is already running,
	// this duplicate just brings the window to the front (or reopens one into the
	// running server) and exits instead of spawning a second server/tray/copy.
	if primary, release := acquireSingle(); !primary {
		if cfg.HTTPAddr != "" && httpAlive(cfg.HTTPAddr) {
			if !bringToFront("星匣 STARBOX") {
				desk.Open(url)
			}
		}
		return
	} else {
		defer release()
	}

	// Only start the background server if no STARBOX server is already listening on
	// the configured address. This lets a second launch (e.g. double-clicking the
	// desktop icon while the app is already running) simply open a window instead of
	// failing to re-bind the port.
	serverRunning := cfg.HTTPAddr != "" && httpAlive(cfg.HTTPAddr)
	if !serverRunning {
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
	}

	// Decide whether this invocation should show the desktop window. Default (no
	// flags) and `-desktop` open the window; `-window` opens a window into a running
	// server; `-tray` stays silent (server + tray only).
	showWindow := *asWindow || *asDesktop || !*trayOn
	if showWindow {
		// If this process started the server and the user configured "tray" mode,
		// show the tray icon so the app can be reopened/quit even after the window
		// is closed.
		ownsServer := !serverRunning
		if ownsServer && cfgSettings.QuitAction != "exit" {
			go tray.Run()
		}
		desk.Open(url)
		// Keep the process alive (with the tray) after the window closes.
		if ownsServer && cfgSettings.QuitAction != "exit" {
			<-ctx.Done()
			return
		}
		log.Println("window closed")
		return
	}

	// Silent tray/server-only mode.
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
