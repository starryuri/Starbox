package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Settings holds app-level preferences (not per-user).
type Settings struct {
	AutoStart  bool   `json:"auto_start"`  // register in HKCU Run so it launches at boot
	QuitAction string `json:"quit_action"` // "exit" = close window quits app; "tray" = keep in tray
}

const runKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
const runName = "STARBOX"

func file(dir string) string { return filepath.Join(dir, "settings.json") }

// Load reads settings from dir (defaults if missing).
func Load(dir string) Settings {
	s := Settings{AutoStart: false, QuitAction: "tray"}
	b, err := os.ReadFile(file(dir))
	if err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.QuitAction != "exit" && s.QuitAction != "tray" {
		s.QuitAction = "tray"
	}
	return s
}

// Save writes settings to dir.
func Save(dir string, s Settings) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(file(dir), b, 0o644)
}

// SetAutoStart adds (or removes) the STARBOX entry in the HKCU Run registry key.
// It shells out to the built-in Windows reg.exe to avoid extra Go dependencies.
// Note: the app currently takes no command-line arguments. A `-tray` flag was
// previously registered here but never implemented — removed; tray support may
// come back together with real -tray handling.
func SetAutoStart(enable bool, exe string) error {
	if enable {
		cmd := fmt.Sprintf("\"%s\"", exe)
		return exec.Command("reg", "add", runKey, "/v", runName, "/t", "REG_SZ", "/d", cmd, "/f").Run()
	}
	return exec.Command("reg", "delete", runKey, "/v", runName, "/f").Run()
}
