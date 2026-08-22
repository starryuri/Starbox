package tray

import (
	_ "embed"
	"log"
	"os"
	"os/exec"

	"github.com/energye/systray"
)

//go:embed tray_icon.ico
var trayIcon []byte

// Run starts the system tray icon and blocks until the user chooses Quit.
func Run() {
	systray.Run(onReady, nil)
}

func onReady() {
	systray.SetIcon(trayIcon)
	systray.SetTitle("STARBOX")
	systray.SetTooltip("STARBOX · 你的次元，收于一匣")

	// Left-click (and right-click) should show the menu, so the tray icon is
	// responsive after the window is hidden to the tray.
	systray.SetOnClick(func(menu systray.IMenu) { _ = menu.ShowMenu() })
	systray.SetOnRClick(func(menu systray.IMenu) { _ = menu.ShowMenu() })

	mOpen := systray.AddMenuItem("打开应用", "打开 STARBOX 主界面")
	mQuit := systray.AddMenuItem("关闭应用", "退出 STARBOX")

	mOpen.Click(spawnWindow)
	mQuit.Click(func() {
		systray.Quit()
		os.Exit(0)
	})
}

// spawnWindow launches a sibling copy of this exe with -window so the dashboard
// opens in a desktop window pointing at the already-running server.
func spawnWindow() {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("tray: resolve exe: %v", err)
		return
	}
	if err := exec.Command(exe, "-window").Start(); err != nil {
		log.Printf("tray: open window: %v", err)
	}
}
