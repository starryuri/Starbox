//go:build windows

// Starbox setup — a native Windows GUI installer / uninstaller built on WebView2.
//
//	setup.exe               -> GUI install (pick dir / start menu / desktop)
//	setup.exe -uninstall    -> GUI uninstall
//	unins.exe (any name w/ "unins") -> always uninstall, even with no args
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

//go:embed payload/starbox.exe
var payloadExe []byte

//go:embed payload/WebView2Loader.dll
var payloadDLL []byte

//go:embed payload/config.json
var payloadCfg []byte

//go:embed installer.html
var installerHTML string

const (
	runKey          = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	uninstallKey    = `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\STARBOX`
	createNoWindow  = 0x08000000
)

// runNoWindow runs a command without flashing a console window.
func runNoWindow(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return cmd.Run()
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func defaultDir() string {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "STARBOX")
	}
	return filepath.Join(os.Getenv("USERPROFILE"), "STARBOX")
}

func shortLinkPaths() (sm, desktop string) {
	sm = filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "STARBOX.lnk")
	desktop = filepath.Join(os.Getenv("USERPROFILE"), "Desktop", "STARBOX.lnk")
	return
}

func shortcut(lnk, target, args, workdir, desc string) {
	ps := fmt.Sprintf(`$ws=New-Object -ComObject WScript.Shell;$s=$ws.CreateShortcut('%s');$s.TargetPath='%s';$s.Arguments='%s';$s.WorkingDirectory='%s';$s.Description='%s';$s.Save()`,
		strings.ReplaceAll(lnk, "'", "''"), strings.ReplaceAll(target, "'", "''"),
		strings.ReplaceAll(args, "'", "''"), strings.ReplaceAll(workdir, "'", "''"), strings.ReplaceAll(desc, "'", "''"))
	_ = runNoWindow("powershell", "-NoProfile", "-Command", ps)
}

func reg(args ...string) error {
	return runNoWindow("reg", args...)
}

// install copies the payload to dir and wires shortcuts + uninstall registry.
func install(dir string, startMenu, desktop bool) error {
	if dir == "" {
		dir = defaultDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	exePath := filepath.Join(dir, "starbox.exe")
	if err := writeFile(exePath, payloadExe); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "WebView2Loader.dll"), payloadDLL); err != nil {
		return err
	}
	cfgPath := filepath.Join(dir, "config.json")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := writeFile(cfgPath, payloadCfg); err != nil {
			return err
		}
	}
	if self, err := os.Executable(); err == nil {
		if b, err := os.ReadFile(self); err == nil {
			_ = writeFile(filepath.Join(dir, "unins.exe"), b)
		}
	}
	if startMenu {
		sm, _ := shortLinkPaths()
		shortcut(sm, exePath, "", dir, "STARBOX · 你的次元 · 收于一匣")
		// Add an uninstall entry in the same start-menu folder.
		shortcut(filepath.Join(filepath.Dir(sm), "卸载 STARBOX.lnk"), filepath.Join(dir, "unins.exe"), "-uninstall", dir, "卸载 STARBOX")
	}
	if desktop {
		_, dd := shortLinkPaths()
		shortcut(dd, exePath, "", dir, "STARBOX · 你的次元 · 收于一匣")
	}
	unins := filepath.Join(dir, "unins.exe")
	kv := func(k, ty, v string) {
		_ = reg("add", uninstallKey, "/v", k, "/t", ty, "/d", v, "/f")
	}
	kv("DisplayName", "REG_SZ", "STARBOX")
	kv("DisplayVersion", "REG_SZ", "1.0.0")
	kv("Publisher", "REG_SZ", "starryuri")
	kv("InstallLocation", "REG_SZ", dir)
	kv("UninstallString", "REG_SZ", fmt.Sprintf(`"%s" -uninstall`, unins))
	kv("DisplayIcon", "REG_SZ", exePath)
	kv("NoModify", "REG_DWORD", "1")
	kv("NoRepair", "REG_DWORD", "1")
	return nil
}

// uninstall removes shortcuts, registry, and files (self scheduled for reboot).
func uninstall(dir string) {
	if dir == "" {
		dir = defaultDir()
		if self, err := os.Executable(); err == nil {
			dir = filepath.Dir(self)
		}
	}
	sm, dd := shortLinkPaths()
	_ = os.Remove(sm)
	_ = os.Remove(filepath.Join(filepath.Dir(sm), "卸载 STARBOX.lnk"))
	_ = os.Remove(dd)
	_ = reg("delete", uninstallKey, "/f")

	self, _ := os.Executable()
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if e.IsDir() {
				_ = os.RemoveAll(p)
				continue
			}
			if self == p {
				continue
			}
			_ = os.Remove(p)
		}
	}
	if self != "" {
		var k32 = syscall.NewLazyDLL("kernel32.dll")
		moveFileEx := k32.NewProc("MoveFileExW")
		if p, err := syscall.UTF16PtrFromString(self); err == nil {
			moveFileEx.Call(uintptr(unsafe.Pointer(p)), 0, 0x4) // MOVEFILE_DELAY_UNTIL_REBOOT
		}
	}
}

// isUninstall returns true when running as an uninstaller, either because the
// binary was copied to "unins.exe" (regardless of args) or "-uninstall" was given.
func isUninstall() bool {
	if strings.Contains(strings.ToLower(filepath.Base(os.Args[0])), "unins") {
		return true
	}
	for _, a := range os.Args[1:] {
		if a == "-uninstall" {
			return true
		}
	}
	return false
}

// ---- native fallback UI ----
var (
	shell32 = windows.NewLazyDLL("shell32.dll")
	bBrowse = shell32.NewProc("SHBrowseForFolderW")
	bPath   = shell32.NewProc("SHGetPathFromIDListW")
	ole32   = windows.NewLazyDLL("ole32.dll")
	bCoFree = ole32.NewProc("CoTaskMemFree")
	user32  = windows.NewLazyDLL("user32.dll")
	bMsgBox = user32.NewProc("MessageBoxW")
)

func msgBox(text, title string) {
	t, _ := windows.UTF16PtrFromString(title)
	c, _ := windows.UTF16PtrFromString(text)
	bMsgBox.Call(0, uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(t)), 0)
}

// pickFolder shows the native Windows folder picker and returns the chosen path.
// It is exposed to the WebView2 page as window.pickFolder().
func pickFolder() string {
	title, _ := windows.UTF16PtrFromString("选择安装位置")
	bi := struct {
		HwndOwner      uintptr
		PidlRoot       uintptr
		PszDisplayName uintptr
		LpszTitle      *uint16
		UlFlags        uint32
		Lpfn           uintptr
		LParam         uintptr
		IImage         int32
	}{HwndOwner: 0, PidlRoot: 0, PszDisplayName: 0, LpszTitle: title, UlFlags: 0x1 /*BIF_RETURNONLYFSDIRS*/ | 0x40 /*BIF_NEWDIALOGSTYLE*/, Lpfn: 0, LParam: 0, IImage: 0}
	r, _, _ := bBrowse.Call(uintptr(unsafe.Pointer(&bi)))
	if r == 0 {
		return ""
	}
	defer bCoFree.Call(r)
	var buf [windows.MAX_PATH]uint16
	if ok, _, _ := bPath.Call(r, uintptr(unsafe.Pointer(&buf[0]))); ok == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:])
}

// ---- HTTP API for the WebView2 installer page ----
func setHandlers(mux *http.ServeMux, mode string) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(installerHTML))
	})
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"mode": mode, "defaultDir": defaultDir()})
	})
	mux.HandleFunc("/install", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Dir       string `json:"dir"`
			StartMenu bool   `json:"startMenu"`
			Desktop   bool   `json:"desktop"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if strings.TrimSpace(b.Dir) == "" {
			b.Dir = defaultDir()
		}
		if err := install(b.Dir, b.StartMenu, b.Desktop); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "dir": b.Dir})
	})
	mux.HandleFunc("/launch", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Dir string `json:"dir"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		dir := b.Dir
		if strings.TrimSpace(dir) == "" {
			dir = defaultDir()
		}
		exe := filepath.Join(dir, "starbox.exe")
		if _, err := os.Stat(exe); err == nil {
			_ = exec.Command(exe, "-desktop").Start()
		}
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/uninstall", func(w http.ResponseWriter, r *http.Request) {
		uninstall("")
		_, _ = w.Write([]byte("ok"))
	})
}

func main() {
	mode := "install"
	if isUninstall() {
		mode = "uninstall"
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		msgBox("启动安装器失败："+err.Error(), "STARBOX 安装器")
		return
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	setHandlers(mux, mode)
	go func() {
		if err := http.Serve(ln, mux); err != nil && err != http.ErrServerClosed {
			log.Printf("installer http serve: %v", err)
		}
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d/?mode=%s", port, mode)
	dataPath := filepath.Join(os.TempDir(), "starbox_installer_"+mode)

	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  "星匣 STARBOX 安装器",
			Width:  620,
			Height: 560,
			IconId: 1, // app icon resource (rsrc)
			Center: true,
		},
	})
	if wv == nil {
		// WebView2 runtime missing — fall back to a plain message.
		msgBox("无法加载 WebView2 运行时。请先安装 Microsoft Edge WebView2 Runtime 后重试。\n\n或直接从源码构建后手动安装。", "STARBOX 安装器")
		return
	}
	defer wv.Destroy()
	if err := wv.Bind("pickFolder", pickFolder); err != nil {
		log.Printf("bind pickFolder: %v", err)
	}
	wv.SetTitle("星匣 STARBOX 安装器")
	wv.SetSize(620, 560, webview2.HintNone)
	wv.Navigate(url)
	wv.Run()
}
