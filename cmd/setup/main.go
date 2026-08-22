//go:build windows

// Starbox setup — a native Windows GUI installer built on WebView2.
//
//	setup.exe   -> GUI install (pick dir / start menu / desktop)
//
// The uninstaller is a separate, standalone binary (unins.exe) built from
// cmd/unin, deployed by this installer and invoked by the Control Panel / the
// start-menu "卸载 STARBOX" shortcut. Keeping them as two distinct programs
// avoids the installer ever acting as an uninstaller.
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

//go:embed payload/unins.exe
var uninsExe []byte

//go:embed installer.html
var installerHTML string

const (
	runKey         = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	uninstallKey   = `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\STARBOX`
	createNoWindow = 0x08000000
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
	// Deploy the standalone uninstaller binary.
	if err := writeFile(filepath.Join(dir, "unins.exe"), uninsExe); err != nil {
		return err
	}
	if startMenu {
		sm, _ := shortLinkPaths()
		shortcut(sm, exePath, "", dir, "STARBOX · 你的次元 · 收于一匣")
		// Add an uninstall entry in the same start-menu folder.
		shortcut(filepath.Join(filepath.Dir(sm), "卸载 STARBOX.lnk"), filepath.Join(dir, "unins.exe"), "", dir, "卸载 STARBOX")
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
	kv("UninstallString", "REG_SZ", fmt.Sprintf(`"%s"`, unins))
	kv("DisplayIcon", "REG_SZ", exePath)
	kv("NoModify", "REG_DWORD", "1")
	kv("NoRepair", "REG_DWORD", "1")
	return nil
}

// ---- native helpers ----
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
func setHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(installerHTML))
	})
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"defaultDir": defaultDir()})
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
}

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		msgBox("启动安装器失败："+err.Error(), "STARBOX 安装器")
		return
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	setHandlers(mux)
	go func() {
		if err := http.Serve(ln, mux); err != nil && err != http.ErrServerClosed {
			log.Printf("installer http serve: %v", err)
		}
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	dataPath := filepath.Join(os.TempDir(), "starbox_installer")

	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  "星匣 STARBOX 安装器",
			Width:  920,
			Height: 720,
			IconId: 1, // app icon resource (rsrc)
			Center: true,
		},
	})
	if wv == nil {
		msgBox("无法加载 WebView2 运行时。请先安装 Microsoft Edge WebView2 Runtime 后重试。", "STARBOX 安装器")
		return
	}
	defer wv.Destroy()
	if err := wv.Bind("pickFolder", pickFolder); err != nil {
		log.Printf("bind pickFolder: %v", err)
	}
	wv.SetTitle("星匣 STARBOX 安装器")
	wv.SetSize(920, 720, webview2.HintNone)
	wv.Navigate(url)
	wv.Run()
}
