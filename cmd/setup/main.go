//go:build windows

// Starbox setup — a self-contained installer + uninstaller.
//   setup.exe                  -> install STARBOX to %LOCALAPPDATA%\STARBOX
//   setup.exe -dir <path>      -> install to <path>
//   setup.exe -uninstall       -> remove STARBOX
package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

//go:embed payload/starbox.exe
var payloadExe []byte

//go:embed payload/WebView2Loader.dll
var payloadDLL []byte

//go:embed payload/config.json
var payloadCfg []byte

var (
	user32     = syscall.NewLazyDLL("user32.dll")
	messageBox = user32.NewProc("MessageBoxW")
	kernel32   = syscall.NewLazyDLL("kernel32.dll")
	moveFileEx = kernel32.NewProc("MoveFileExW")
)

func msg(title, text string) {
	ti, _ := syscall.UTF16PtrFromString(title)
	tt, _ := syscall.UTF16PtrFromString(text)
	messageBox.Call(0, uintptr(unsafe.Pointer(ti)), uintptr(unsafe.Pointer(tt)), 0x40) // MB_ICONINFORMATION
}

func defaultDir() string {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "STARBOX")
	}
	return filepath.Join(os.Getenv("USERPROFILE"), "STARBOX")
}

const uninstallKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\STARBOX`

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func shortcut(lnk, target, args, workdir, desc string) {
	ps := fmt.Sprintf(`$ws=New-Object -ComObject WScript.Shell;$s=$ws.CreateShortcut('%s');$s.TargetPath='%s';$s.Arguments='%s';$s.WorkingDirectory='%s';$s.Description='%s';$s.Save()`, lnk, target, args, workdir, desc)
	_ = exec.Command("powershell", "-NoProfile", "-Command", ps).Run()
}

func shortLinkPaths() (startMenu string, desktop string) {
	sm := os.Getenv("APPDATA")
	dd := os.Getenv("USERPROFILE")
	startMenu = filepath.Join(sm, "Microsoft", "Windows", "Start Menu", "Programs", "STARBOX.lnk")
	desktop = filepath.Join(dd, "Desktop", "STARBOX.lnk")
	return
}

func install(dir string) error {
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
	// uninstaller = a copy of this exe
	if self, err := os.Executable(); err == nil {
		if b, err := os.ReadFile(self); err == nil {
			_ = writeFile(filepath.Join(dir, "unins.exe"), b)
		}
	}
	// shortcuts
	sm, dd := shortLinkPaths()
	shortcut(sm, exePath, "", dir, "STARBOX · 你的次元 · 收于一匣")
	shortcut(dd, exePath, "", dir, "STARBOX · 你的次元 · 收于一匣")
	// uninstall registry entry
	unins := filepath.Join(dir, "unins.exe")
	args := []string{"add", uninstallKey, "/v", "DisplayName", "/t", "REG_SZ", "/d", "STARBOX", "/f"}
	_ = exec.Command("reg", args...).Run()
	args = []string{"add", uninstallKey, "/v", "DisplayVersion", "/t", "REG_SZ", "/d", "1.0.0", "/f"}
	_ = exec.Command("reg", args...).Run()
	args = []string{"add", uninstallKey, "/v", "Publisher", "/t", "REG_SZ", "/d", "starryuri", "/f"}
	_ = exec.Command("reg", args...).Run()
	args = []string{"add", uninstallKey, "/v", "InstallLocation", "/t", "REG_SZ", "/d", dir, "/f"}
	_ = exec.Command("reg", args...).Run()
	args = []string{"add", uninstallKey, "/v", "UninstallString", "/t", "REG_SZ", "/d", fmt.Sprintf(`"%s" -uninstall`, unins), "/f"}
	_ = exec.Command("reg", args...).Run()
	args = []string{"add", uninstallKey, "/v", "DisplayIcon", "/t", "REG_SZ", "/d", exePath, "/f"}
	_ = exec.Command("reg", args...).Run()
	args = []string{"add", uninstallKey, "/v", "NoModify", "/t", "REG_DWORD", "/d", "1", "/f"}
	_ = exec.Command("reg", args...).Run()
	args = []string{"add", uninstallKey, "/v", "NoRepair", "/t", "REG_DWORD", "/d", "1", "/f"}
	_ = exec.Command("reg", args...).Run()
	return nil
}

func uninstall(dir string) {
	if dir == "" {
		dir = defaultDir()
	}
	sm, dd := shortLinkPaths()
	_ = os.Remove(sm)
	_ = os.Remove(dd)
	_ = exec.Command("reg", "delete", uninstallKey, "/f").Run()

	// Remove everything except the running uninstaller, then schedule the exe
	// for deletion on reboot (Windows locks a running exe against deletion).
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
		p, _ := syscall.UTF16PtrFromString(self)
		moveFileEx.Call(uintptr(unsafe.Pointer(p)), 0, 0x4) // MOVEFILE_DELAY_UNTIL_REBOOT
	}
}

func main() {
	args := os.Args[1:]
	dir := ""
	un := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-dir":
			if i+1 < len(args) {
				dir = args[i+1]
				i++
			}
		case "-uninstall":
			un = true
		}
	}
	if un {
		uninstall(dir)
		msg("STARBOX", "已卸载 STARBOX 并清理相关文件。")
		return
	}
	if err := install(dir); err != nil {
		msg("STARBOX", "安装失败：\n"+err.Error())
		return
	}
	msg("STARBOX", "安装完成！\n可从「开始菜单 / 桌面」的 STARBOX 快捷方式启动。\n（卸载：控制面板 → 应用，或运行安装目录下的 unins.exe）")
}
