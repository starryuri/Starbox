//go:build windows

// Starbox setup — a native Windows GUI installer / uninstaller.
//   setup.exe                -> GUI install (pick dir / start menu / desktop)
//   setup.exe -uninstall     -> GUI uninstall
package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/walk/declarative"
)

//go:embed payload/starbox.exe
var payloadExe []byte

//go:embed payload/WebView2Loader.dll
var payloadDLL []byte

//go:embed payload/config.json
var payloadCfg []byte

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

func isUninstall() bool {
	for _, a := range os.Args[1:] {
		if a == "-uninstall" {
			return true
		}
	}
	return false
}

func main() {
	if isUninstall() {
		if walk.MsgBox(nil, "卸载 STARBOX", "确定要卸载 STARBOX 吗？将删除安装目录、快捷方式与卸载注册表项。", walk.MsgBoxYesNo) == walk.DlgCmdYes {
			uninstall("")
			walk.MsgBox(nil, "STARBOX", "已卸载 STARBOX 并清理相关文件。", walk.MsgBoxOK)
		}
		return
	}

	var mw *walk.MainWindow
	var pathEdit *walk.LineEdit
	var cbStart, cbDesktop *walk.CheckBox
	var status *walk.Label
	var installBtn *walk.PushButton

	declarative.MainWindow{
		AssignTo: &mw,
		Title:    "星匣 STARBOX 安装程序",
		Size:     declarative.Size{Width: 460, Height: 330},
		MinSize:  declarative.Size{Width: 460, Height: 330},
		Layout:   declarative.VBox{},
		Children: []declarative.Widget{
			declarative.GroupBox{
				Title:  "安装位置",
				Layout: declarative.HBox{},
				Children: []declarative.Widget{
					declarative.LineEdit{AssignTo: &pathEdit, Text: defaultDir()},
					declarative.PushButton{Text: "浏览…", OnClicked: func() {
						dlg := walk.FileDialog{InitialDirPath: pathEdit.Text(), Title: "选择安装位置"}
						if ok, err := dlg.ShowBrowseFolder(mw); err == nil && ok && dlg.FilePath != "" {
							pathEdit.SetText(dlg.FilePath)
						}
					}},
				},
			},
			declarative.GroupBox{
				Title:  "选项",
				Layout: declarative.VBox{},
				Children: []declarative.Widget{
					declarative.CheckBox{AssignTo: &cbStart, Text: "添加开始菜单快捷方式", Checked: true},
					declarative.CheckBox{AssignTo: &cbDesktop, Text: "添加桌面快捷方式", Checked: true},
				},
			},
			declarative.Label{AssignTo: &status, Text: "默认安装到 " + defaultDir()},
			declarative.Composite{
				Layout: declarative.HBox{},
				Children: []declarative.Widget{
					declarative.HSpacer{},
					declarative.PushButton{Text: "安装", AssignTo: &installBtn, OnClicked: func() {
						dir := pathEdit.Text()
						if strings.TrimSpace(dir) == "" {
							dir = defaultDir()
						}
						installBtn.SetEnabled(false)
						status.SetText("正在安装…")
						go func() {
							err := install(dir, cbStart.Checked(), cbDesktop.Checked())
							if err != nil {
								status.SetText("安装失败：" + err.Error())
							} else {
								status.SetText("✔ 安装完成！可从开始菜单/桌面启动。")
							}
							installBtn.SetEnabled(true)
						}()
					}},
					declarative.PushButton{Text: "取消", OnClicked: func() { mw.Close() }},
				},
			},
		},
	}.Create()
	mw.Run()
}
