package settings

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
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

// ---------- DPAPI secret protection (used for bound credentials) ----------

// blob mirrors DATA_BLOB (we only ever point it at byte/uint16 buffers).
type blob struct {
	cbData uint32
	pbData uintptr
}

// dpapiProtect encrypts plaintext with CryptProtectData (current user scope)
// and returns "dpapi:<base64>". Returns "" on failure (caller falls back).
func dpapiProtect(plain string) string {
	crypt32 := windows.NewLazySystemDLL("crypt32.dll")
	CryptProtectData := crypt32.NewProc("CryptProtectData")
	plainPtr, err := windows.UTF16PtrFromString(plain)
	if err != nil {
		return ""
	}
	in := blob{cbData: uint32(len(plain) * 2), pbData: uintptr(unsafe.Pointer(plainPtr))}
	var out blob
	r, _, _ := CryptProtectData.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&out)))
	if r == 0 || out.cbData == 0 {
		return ""
	}
	cipher := unsafe.Slice((*byte)(unsafe.Pointer(out.pbData)), out.cbData)
	enc := "dpapi:" + base64.StdEncoding.EncodeToString(cipher)
	windows.LocalFree(windows.Handle(out.pbData))
	return enc
}

// dpapiUnprotect reverses dpapiProtect; returns "" when input is not protected.
func dpapiUnprotect(v string) string {
	if !strings.HasPrefix(v, "dpapi:") {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, "dpapi:"))
	if err != nil || len(raw) == 0 {
		return ""
	}
	crypt32 := windows.NewLazySystemDLL("crypt32.dll")
	CryptUnprotectData := crypt32.NewProc("CryptUnprotectData")
	in := blob{cbData: uint32(len(raw)), pbData: uintptr(unsafe.Pointer(&raw[0]))}
	var out blob
	r, _, _ := CryptUnprotectData.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&out)))
	if r == 0 || out.cbData == 0 {
		return ""
	}
	plain := unsafe.Slice((*uint16)(unsafe.Pointer(out.pbData)), out.cbData/2)
	s := windows.UTF16ToString(plain)
	windows.LocalFree(windows.Handle(out.pbData))
	return s
}

// DPAPIProtect encrypts a secret with Windows DPAPI (user scope). Best effort:
// on failure the plaintext is returned so binding never hard-fails.
func DPAPIProtect(plain string) string {
	if p := dpapiProtect(plain); p != "" {
		return p
	}
	return plain
}

// DPAPIUnprotect reverses DPAPIProtect ("" when not recoverable).
func DPAPIUnprotect(v string) string { return dpapiUnprotect(v) }

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
