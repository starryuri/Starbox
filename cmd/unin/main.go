//go:build windows

// Starbox uninstaller — a standalone GUI uninstaller (unins.exe). It is a
// distinct binary from the installer: it always runs the uninstall flow and is
// deployed into the install dir by setup.exe, then invoked from the Control
// Panel / the start-menu "卸载 STARBOX" shortcut.
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
	"syscall"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

//go:embed uninstaller.html
var unHTML string

const (
	uninstallKey   = `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\STARBOX`
	createNoWindow = 0x08000000
)

func runNoWindow(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return cmd.Run()
}

func reg(args ...string) error {
	return runNoWindow("reg", args...)
}

func shortLinkPaths() (sm, desktop string) {
	sm = filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "STARBOX.lnk")
	desktop = filepath.Join(os.Getenv("USERPROFILE"), "Desktop", "STARBOX.lnk")
	return
}

// uninstall removes shortcuts, registry, and files (self scheduled for reboot).
func uninstall(dir string) {
	if dir == "" {
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

var (
	user32  = windows.NewLazyDLL("user32.dll")
	bMsgBox = user32.NewProc("MessageBoxW")
)

func msgBoxOK(text, title string) {
	t, _ := windows.UTF16PtrFromString(title)
	c, _ := windows.UTF16PtrFromString(text)
	bMsgBox.Call(0, uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(t)), 0x40) // MB_ICONINFORMATION
}

func dirOfSelf() string {
	if self, err := os.Executable(); err == nil {
		return filepath.Dir(self)
	}
	return ""
}

func setHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(unHTML))
	})
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"dir": dirOfSelf()})
	})
	mux.HandleFunc("/uninstall", func(w http.ResponseWriter, r *http.Request) {
		uninstall("")
		_, _ = w.Write([]byte("ok"))
	})
}

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		msgBoxOK("启动卸载器失败："+err.Error(), "STARBOX")
		return
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	setHandlers(mux)
	go func() {
		if err := http.Serve(ln, mux); err != nil && err != http.ErrServerClosed {
			log.Printf("uninstaller http serve: %v", err)
		}
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	dataPath := filepath.Join(os.TempDir(), "starbox_uninstaller")

	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  "星匣 STARBOX 卸载",
			Width:  720,
			Height: 600,
			IconId: 1, // app icon resource (rsrc)
			Center: true,
		},
	})
	if wv == nil {
		// WebView2 missing — fall back to a native confirm dialog.
		msgBoxOK("无法加载 WebView2 运行时。", "STARBOX")
		return
	}
	defer wv.Destroy()
	// `window.close()` does not close a WebView2 window that the app itself created,
	// so expose a bound function that destroys the window (endpoint of Run()).
	if err := wv.Bind("closeApp", func() { go wv.Destroy() }); err != nil {
		log.Printf("bind closeApp: %v", err)
	}
	wv.SetTitle("星匣 STARBOX 卸载")
	wv.SetSize(720, 600, webview2.HintNone)
	wv.Navigate(url)
	wv.Run()
}
