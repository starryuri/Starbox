package httpd

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"butler/internal/account"
	"butler/internal/anime"
	"butler/internal/du"
	"butler/internal/ebook"
	"butler/internal/githot"
	"butler/internal/kb"
	"butler/internal/monitor"
	"butler/internal/rss"
	"butler/internal/settings"

	"github.com/shirou/gopsutil/v3/disk"
)

//go:embed dashboard.html
var dashboardHTML string

//go:embed assets/bootstrap.min.css
var bootstrapCSS string

//go:embed assets/bootstrap.bundle.min.js
var bootstrapJS string

var kbCols = map[string]bool{"anime": true, "study": true, "games": true, "notes": true, "books": true, "notif": true, "rules": true, "connect": true, "trending": true, "favs": true}

// Start begins a small localhost HTTP server.
func Start(addr string, st *monitor.State, kstore *kb.Store, dataDir string, acc *account.Manager) {
	booksDir := filepath.Join(dataDir, "books")
	_ = os.MkdirAll(booksDir, 0o755)
	essaysDir := filepath.Join(dataDir, "essays")
	_ = os.MkdirAll(essaysDir, 0o755)

	if acc == nil {
		acc, _ = account.New(dataDir)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(dashboardHTML))
	})

	mux.HandleFunc("/assets/bootstrap.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write([]byte(bootstrapCSS))
	})
	mux.HandleFunc("/assets/bootstrap.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte(bootstrapJS))
	})

	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(st.Snapshot()); err != nil {
			log.Printf("encode: %v", err)
		}
	})

	// ---- Local account system ----
	sessionCookie := func(r *http.Request) string {
		c, err := r.Cookie("starbox_session")
		if err != nil {
			return ""
		}
		return c.Value
	}
	setSession := func(w http.ResponseWriter, tok string) {
		// Use a long-lived cookie so WebView2 persists it to disk (the window's
		// user-data folder is stable across restarts). Without MaxAge/Expires this
		// would be a session cookie that is wiped every time the window closes,
		// forcing a re-login on every reopen.
		http.SetCookie(w, &http.Cookie{Name: "starbox_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 365 * 24 * 3600})
	}
	userJSON := func(u account.User) map[string]any {
		return map[string]any{"id": u.ID, "nickname": u.Nickname, "theme": u.Theme}
	}
	// Per-user data isolation: logged-in accounts get their own data dir + store,
	// guests use the shared dataDir store (existing behavior).
	uidOf := func(r *http.Request) string {
		u, ok := acc.Session(sessionCookie(r))
		if !ok {
			return ""
		}
		return u.ID
	}
	baseFor := func(uid string) string {
		if uid == "" {
			return dataDir
		}
		return filepath.Join(dataDir, "users", uid)
	}
	var storeMu sync.Mutex
	userStores := map[string]*kb.Store{}
	storeFor := func(uid string) *kb.Store {
		if uid == "" {
			return kstore
		}
		storeMu.Lock()
		defer storeMu.Unlock()
		if s, ok := userStores[uid]; ok {
			return s
		}
		s := kb.New(filepath.Join(dataDir, "users", uid))
		_ = os.MkdirAll(filepath.Join(dataDir, "users", uid, "books"), 0o755)
		_ = os.MkdirAll(filepath.Join(dataDir, "users", uid, "essays"), 0o755)
		userStores[uid] = s
		return s
	}
	// seedAnime adds the flagship 恋爱小行星 entry if an account's anime list is empty.
	seedAnime := func(uid string) {
		us := storeFor(uid)
		if recs, err := us.List("anime"); err == nil && len(recs) > 0 {
			return
		}
		_, _ = us.Add("anime", map[string]interface{}{
			"title": "恋爱小行星", "status": "想追", "total": 12, "watched": "",
			"air_start": "2020-01-03", "duration": 24, "rate": 6.9,
			"bgm_id": 276150, "anilist_id": "", "xid": "",
			"cover": "https://bgmimg.anibt.net/pic/cover/l/eb/9f/276150_tJJGx.jpg",
			"link": "https://bgm.tv/subject/276150",
			"note": "高中天文社少女真中苍与地学社的蓝原诗织，因一颗小行星而结缘，共同追逐「发现小行星」的梦想。",
		})
	}
	// migrateGuestTo copies existing guest/global data (kb collections + book/essay
	// files) into a newly registered account so first login isn't empty.
	migrateGuestTo := func(uid string) {
		if uid == "" {
			return
		}
		us := storeFor(uid)
		for _, c := range []string{"anime", "favs", "books", "study", "games", "notes", "notif", "rules", "connect", "trending"} {
			recs, err := kstore.List(c)
			if err != nil {
				continue
			}
			for _, rec := range recs {
				_, _ = us.Add(c, rec.Data)
			}
		}
		copyDir := func(src, dst string) {
			if entries, err := os.ReadDir(src); err == nil {
				_ = os.MkdirAll(dst, 0o755)
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					if b, err2 := os.ReadFile(filepath.Join(src, e.Name())); err2 == nil {
						_ = os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644)
					}
				}
			}
		}
		gb, ub := filepath.Join(dataDir, "books"), filepath.Join(dataDir, "users", uid, "books")
		ge, ue := filepath.Join(dataDir, "essays"), filepath.Join(dataDir, "users", uid, "essays")
		copyDir(gb, ub)
		copyDir(ge, ue)
		// rewrite book file paths to the user's dir
		if bks, err := us.List("books"); err == nil {
			for _, b := range bks {
				if f, ok := b.Data["file"].(string); ok {
					nd := filepath.Join(ub, filepath.Base(f))
					if f != nd {
						b.Data["file"] = nd
						_, _ = us.Update("books", b.ID, b.Data)
					}
				}
			}
		}
		// rewrite essay paths inside anime notes
		if anis, err := us.List("anime"); err == nil {
			for _, a := range anis {
				if notes, ok := a.Data["notes"].([]interface{}); ok {
					for _, n := range notes {
						if nm, ok := n.(map[string]interface{}); ok {
							if nf, ok := nm["file"].(string); ok && strings.HasPrefix(nf, ge) {
								nm["file"] = filepath.Join(ue, filepath.Base(nf))
							}
						}
					}
					_, _ = us.Update("anime", a.ID, a.Data)
				}
			}
		}
	}

	mux.HandleFunc("POST /account/register", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Nickname string `json:"nickname"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		u, tok, err := acc.Register(b.Nickname, b.Password)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		setSession(w, tok)
		go migrateGuestTo(u.ID) // carry existing guest data into the new account
		go seedAnime(u.ID)      // if the account has no anime yet, greet with 恋爱小行星
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"user": userJSON(u)})
	})

	mux.HandleFunc("POST /account/login", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Nickname string `json:"nickname"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		u, tok, err := acc.Login(b.Nickname, b.Password)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		setSession(w, tok)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"user": userJSON(u)})
	})

	mux.HandleFunc("POST /account/logout", func(w http.ResponseWriter, r *http.Request) {
		acc.Logout(sessionCookie(r))
		c := &http.Cookie{Name: "starbox_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode}
		http.SetCookie(w, c)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /account/session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		u, ok := acc.Session(sessionCookie(r))
		out := map[string]any{"has_accounts": acc.HasUsers(), "user": nil}
		if ok {
			out["user"] = userJSON(u)
		}
		json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("POST /account/theme", func(w http.ResponseWriter, r *http.Request) {
		u, ok := acc.Session(sessionCookie(r))
		if !ok {
			http.Error(w, "not logged in", http.StatusUnauthorized)
			return
		}
		var b struct {
			Theme string `json:"theme"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if err := acc.SetTheme(u.ID, b.Theme); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	// ---- App settings (开机自启动 / 退出行为) ----
	mux.HandleFunc("GET /settings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(settings.Load(dataDir))
	})

	mux.HandleFunc("POST /settings", func(w http.ResponseWriter, r *http.Request) {
		var s settings.Settings
		_ = json.NewDecoder(r.Body).Decode(&s)
		if s.QuitAction != "exit" && s.QuitAction != "tray" {
			s.QuitAction = "tray"
		}
		exe, _ := os.Executable()
		_ = settings.SetAutoStart(s.AutoStart, exe)
		_ = settings.Save(dataDir, s)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s)
	})

	mux.HandleFunc("/drives", func(w http.ResponseWriter, r *http.Request) {
		parts, err := disk.Partitions(true)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out := []map[string]any{}
		for _, p := range parts {
			u, err := disk.Usage(p.Mountpoint)
			if err != nil {
				continue
			}
			out = append(out, map[string]any{
				"name":    p.Mountpoint,
				"mount":   p.Mountpoint,
				"total":   u.Total,
				"free":    u.Free,
				"used":    u.Used,
				"usedpct": u.UsedPercent,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/dir", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p == "" {
			http.Error(w, "missing path", http.StatusBadRequest)
			return
		}
		limit := 12
		if ls := r.URL.Query().Get("limit"); ls != "" {
			if n, err := strconv.Atoi(ls); err == nil && n > 0 {
				limit = n
			}
		}
		items, err := du.Scan(p, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"path": p, "items": items})
	})

	mux.HandleFunc("GET /anime/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "missing q", http.StatusBadRequest)
			return
		}
		res, err := anime.Search(q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
	})

	// full anime detail (studios + cast + CVs)
	mux.HandleFunc("GET /anime/detail", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		i, _ := strconv.Atoi(id)
		d, err := anime.GetDetail(i)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(d)
	})

	// a studio's anime works (for favorites)
	mux.HandleFunc("GET /anime/studio", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		i, _ := strconv.Atoi(id)
		ws, err := anime.GetStudio(i)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ws)
	})

	// a staff/voice-actor's anime works (for favorites)
	mux.HandleFunc("GET /anime/staff", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		i, _ := strconv.Atoi(id)
		ws, err := anime.GetStaff(i)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ws)
	})

	// 萌娘百科 search
	mux.HandleFunc("GET /moegirl/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "missing q", http.StatusBadRequest)
			return
		}
		hits, err := anime.MoegirlSearch(q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(hits)
	})

	// 萌娘百科 detail (Chinese cover + summary for given titles)
	mux.HandleFunc("GET /moegirl/detail", func(w http.ResponseWriter, r *http.Request) {
		t := r.URL.Query().Get("titles")
		if t == "" {
			http.Error(w, "missing titles", http.StatusBadRequest)
			return
		}
		items, err := anime.MoegirlDetails(strings.Split(t, "|"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(items)
	})

	// bangumi Chinese search (fallback to moegirl on failure)
	mux.HandleFunc("GET /bangumi/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "missing q", http.StatusBadRequest)
			return
		}
		res, err := anime.BangumiSearch(q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
	})

	// bangumi: fetch a user's anime collection (中文数据)
	mux.HandleFunc("GET /bangumi/user", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("id")
		if user == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		typ, _ := strconv.Atoi(r.URL.Query().Get("type"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		items, err := anime.BangumiUserCollection(user, typ, limit, offset)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(items)
	})

	// bangumi: credited persons/studios for a subject (中文 studio/CV)
	mux.HandleFunc("GET /bangumi/persons", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		if id == 0 {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		items, err := anime.BangumiSubjectPersons(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(items)
	})

	// XinyuuDB: Chinese anime search
	mux.HandleFunc("GET /xinyuu/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "missing q", http.StatusBadRequest)
			return
		}
		items, err := anime.XinyuuSearch(q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(items)
	})

	// XinyuuDB: combined Chinese detail (metadata + staff + characters)
	mux.HandleFunc("GET /xinyuu/detail", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		if id == 0 {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		d, err := anime.GetXinyuuDetail(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(d)
	})

	// XinyuuDB: works credited to a staff (studio/CV), Chinese
	mux.HandleFunc("GET /xinyuu/staff-works", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		if id == 0 {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		items, err := anime.XinyuuStaffAnimes(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(items)
	})

	// bangumi relay: subject detail (Chinese)
	mux.HandleFunc("GET /bangumi/detail", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		if id == 0 {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		d, err := anime.BangumiGetDetail(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(d)
	})

	// bangumi relay: characters + voice actors for a subject
	mux.HandleFunc("GET /bangumi/characters", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		if id == 0 {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		items, err := anime.BangumiCharactersGet(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(items)
	})

	// XinyuuDB: find staff by name (to bridge old AniList studio favorites -> Chinese works)
	mux.HandleFunc("GET /xinyuu/staff-search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "missing q", http.StatusBadRequest)
			return
		}
		items, err := anime.XinyuuStaffSearch(q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(items)
	})

	// import e-books from a local path (folder or file)
	mux.HandleFunc("POST /books/import", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p == "" {
			http.Error(w, "missing path", http.StatusBadRequest)
			return
		}
		n, err := importBooks(storeFor(uidOf(r)), p)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"imported": n})
	})

	// upload a dragged/dropped e-book and auto-catalogue it
	mux.HandleFunc("POST /books/upload", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, "parse: "+err.Error(), http.StatusBadRequest)
			return
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "no file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		name := filepath.Base(hdr.Filename)
		dst := filepath.Join(filepath.Join(baseFor(uidOf(r)), "books"), name)
		out, err := os.Create(dst)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = io.Copy(out, file)
		out.Close()

		m := ebook.Extract(dst)
		rec, err := storeFor(uidOf(r)).Add("books", map[string]interface{}{
			"title":  m.Title,
			"author": m.Author,
			"format": m.Format,
			"file":   dst,
			"cat":    "",
			"note":   "",
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rec)
	})

	// open a registered book's file with the OS default app
	mux.HandleFunc("POST /books/open", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" || !kbCols["books"] {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		recs, err := storeFor(uidOf(r)).List("books")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		file := ""
		for _, x := range recs {
			if x.ID == id {
				if f, ok := x.Data["file"].(string); ok {
					file = f
				}
				break
			}
		}
		if file == "" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", file).Start(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	// upload an anime 观后感/后日谈 file (Word/txt) into data/essays
	mux.HandleFunc("POST /essay/upload", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, "parse: "+err.Error(), http.StatusBadRequest)
			return
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "no file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		name := fmt.Sprintf("%d_%s", time.Now().Unix(), filepath.Base(hdr.Filename))
		dst := filepath.Join(filepath.Join(baseFor(uidOf(r)), "essays"), name)
		out, err := os.Create(dst)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = io.Copy(out, file)
		out.Close()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"path": dst, "name": hdr.Filename})
	})

	// open a file that lives under the app's data dir (essays/books)
	mux.HandleFunc("POST /file/open", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p == "" || !strings.HasPrefix(p, dataDir) {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", p).Start(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	// GitHub trending repositories
	mux.HandleFunc("GET /github/trending", func(w http.ResponseWriter, r *http.Request) {
		days := 7
		if ds := r.URL.Query().Get("days"); ds != "" {
			if n, err := strconv.Atoi(ds); err == nil && n > 0 {
				days = n
			}
		}
		repos, err := githot.Trending(days, r.URL.Query().Get("token"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(repos)
	})

	// verify a GitHub personal access token and return the account
	mux.HandleFunc("POST /github/auth", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Token == "" {
			http.Error(w, "missing token", http.StatusBadRequest)
			return
		}
		acct, err := githot.Auth(b.Token)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(acct)
	})

	// fetch the authenticated user's repos
	mux.HandleFunc("GET /github/myrepos", func(w http.ResponseWriter, r *http.Request) {
		t := r.URL.Query().Get("token")
		if t == "" {
			http.Error(w, "missing token", http.StatusBadRequest)
			return
		}
		repos, err := githot.MyRepos(t)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(repos)
	})

	// parse a CSDN blog RSS for the given username
	mux.HandleFunc("GET /csdn/blog", func(w http.ResponseWriter, r *http.Request) {
		u := r.URL.Query().Get("user")
		if u == "" {
			http.Error(w, "missing user", http.StatusBadRequest)
			return
		}
		feed, err := rss.Fetch(r.Context(), "https://blog.csdn.net/"+u+"/rss/list", 20)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"title": feed.Title, "items": feed.Items})
	})

	// knowledge base CRUD
	mux.HandleFunc("GET /kb/{c}", func(w http.ResponseWriter, r *http.Request) {
		c := r.PathValue("c")
		if !kbCols[c] {
			http.NotFound(w, r)
			return
		}
		recs, err := storeFor(uidOf(r)).List(c)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(recs)
	})

	mux.HandleFunc("POST /kb/{c}", func(w http.ResponseWriter, r *http.Request) {
		c := r.PathValue("c")
		if !kbCols[c] {
			http.NotFound(w, r)
			return
		}
		var data map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rec, err := storeFor(uidOf(r)).Add(c, data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rec)
	})

	mux.HandleFunc("PUT /kb/{c}/{id}", func(w http.ResponseWriter, r *http.Request) {
		c := r.PathValue("c")
		if !kbCols[c] {
			http.NotFound(w, r)
			return
		}
		var data map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&data)
		rec, err := storeFor(uidOf(r)).Update(c, r.PathValue("id"), data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rec)
	})

	mux.HandleFunc("DELETE /kb/{c}/{id}", func(w http.ResponseWriter, r *http.Request) {
		c := r.PathValue("c")
		if !kbCols[c] {
			http.NotFound(w, r)
			return
		}
		if err := storeFor(uidOf(r)).Delete(c, r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	go func() {
		log.Printf("http listening on %s", addr)
		if err := http.ListenAndServe(addr, mux); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()
}

func importBooks(kstore *kb.Store, path string) (int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	var files []string
	if info.IsDir() {
		entries, _ := os.ReadDir(path)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if isEbook(e.Name()) {
				files = append(files, filepath.Join(path, e.Name()))
			}
		}
	} else {
		files = []string{path}
	}
	n := 0
	for _, f := range files {
		m := ebook.Extract(f)
		data := map[string]interface{}{
			"title":  m.Title,
			"author": m.Author,
			"format": m.Format,
			"file":   f,
			"cat":    "",
			"note":   "",
		}
		if _, err := kstore.Add("books", data); err == nil {
			n++
		}
	}
	return n, nil
}

func isEbook(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".epub", ".pdf", ".txt", ".md", ".mobi", ".azw3":
		return true
	}
	return false
}
