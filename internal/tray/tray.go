package tray

import (
	_ "embed"
	"log"
	"os"
	"os/exec"

	"github.com/energye/systray"
)

//go:embed tray_icon.png
var trayIcon []byte

// Run starts the system tray icon and blocks until the user chooses Quit.
func Run() {
	systray.Run(onReady, nil)
}

func onReady() {
	systray.SetIcon(trayIcon)
	systray.SetTitle("STARBOX")
	systray.SetTooltip("STARBOX · 你的次元，收于一匣")

	mOpen := systray.AddMenuItem("打开面板", "打开仪表盘窗口")
	mRefresh := systray.AddMenuItem("刷新面板", "重新打开仪表盘窗口")
	mQuit := systray.AddMenuItem("退出", "退出 STARBOX")

	mOpen.Click(spawnWindow)
	mRefresh.Click(spawnWindow)
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
