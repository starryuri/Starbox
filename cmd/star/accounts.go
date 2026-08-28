//go:build windows

package main

// accounts.go — lightweight local multi-identity system.
//
// Each identity gets an isolated data directory (data/profiles/<id>/) holding
// its own collections (anime/favs/notif/...), settings.json, theme.json and
// detail_layout.json — full personalization per user. Covers stay global
// (data/covers) as a shared cache. On first run of this version the existing
// top-level *.json files migrate into a default identity automatically.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"butler/internal/kb"
)

type profile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var (
	profiles      []profile
	currentProfID string
	curProfDir    string
)

func profilesRoot() string        { return filepath.Join(dataDir, "profiles") }
func profilesIndexPath() string   { return filepath.Join(profilesRoot(), "index.json") }
func currentProfilePath() string  { return filepath.Join(dataDir, "current_profile.json") }
func profileDir(id string) string { return filepath.Join(profilesRoot(), id) }

// initProfiles migrates legacy data (once), loads the index and resolves the
// current identity. Call before st is created.
func initProfiles() {
	if _, err := os.Stat(profilesRoot()); os.IsNotExist(err) {
		migrateToProfiles()
	}
	profiles = nil
	if b, err := os.ReadFile(profilesIndexPath()); err == nil {
		_ = json.Unmarshal(b, &profiles)
	}
	if len(profiles) == 0 {
		id := newProfileID()
		_ = os.MkdirAll(profileDir(id), 0o755)
		profiles = []profile{{ID: id, Name: "默认"}}
		saveProfilesIndex()
	}
	currentProfID = ""
	if b, err := os.ReadFile(currentProfilePath()); err == nil {
		var v struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(b, &v) == nil {
			for _, p := range profiles {
				if p.ID == v.ID {
					currentProfID = v.ID
				}
			}
		}
	}
	if currentProfID == "" {
		currentProfID = profiles[0].ID
	}
	curProfDir = profileDir(currentProfID)
	_ = os.MkdirAll(curProfDir, 0o755)
}

// migrateToProfiles moves every top-level *.json (collections, settings,
// theme, detail layout) into a fresh default identity directory.
func migrateToProfiles() {
	id := newProfileID()
	dst := profileDir(id)
	_ = os.MkdirAll(dst, 0o755)
	if entries, err := os.ReadDir(dataDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if name := e.Name(); strings.HasSuffix(name, ".json") {
				_ = os.Rename(filepath.Join(dataDir, name), filepath.Join(dst, name))
			}
		}
	}
	profiles = []profile{{ID: id, Name: "默认"}}
	_ = os.MkdirAll(profilesRoot(), 0o755)
	saveProfilesIndex()
	saveCurrentProfileMeta()
}

func saveProfilesIndex() {
	b, _ := json.MarshalIndent(profiles, "", "  ")
	_ = os.MkdirAll(profilesRoot(), 0o755)
	_ = os.WriteFile(profilesIndexPath(), b, 0o644)
}

func saveCurrentProfileMeta() {
	b, _ := json.MarshalIndent(struct {
		ID string `json:"id"`
	}{currentProfID}, "", "  ")
	_ = os.WriteFile(currentProfilePath(), b, 0o644)
}

func newProfileID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func currentProfileName() string {
	for _, p := range profiles {
		if p.ID == currentProfID {
			return p.Name
		}
	}
	return "?"
}

func createProfile(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("名字不能为空")
	}
	for _, p := range profiles {
		if p.Name == name {
			return "", errors.New("同名身份已存在")
		}
	}
	id := newProfileID()
	if err := os.MkdirAll(profileDir(id), 0o755); err != nil {
		return "", err
	}
	profiles = append(profiles, profile{ID: id, Name: name})
	saveProfilesIndex()
	return id, nil
}

func deleteProfile(id string) error {
	if len(profiles) <= 1 {
		return errors.New("至少保留一个身份")
	}
	if id == currentProfID {
		return errors.New("不能删除当前身份，请先切换到其他身份")
	}
	for i, p := range profiles {
		if p.ID == id {
			profiles = append(profiles[:i], profiles[i+1:]...)
			saveProfilesIndex()
			_ = os.RemoveAll(profileDir(id))
			return nil
		}
	}
	return errors.New("身份不存在")
}

// cycleProfile switches to the next/previous identity in the list.
func cycleProfile(delta int) {
	idx := 0
	for i, p := range profiles {
		if p.ID == currentProfID {
			idx = i
			break
		}
	}
	n := (idx + delta + len(profiles)) % len(profiles)
	switchProfileTo(profiles[n].ID)
}

// switchProfileTo repoints the store at the identity's data directory and
// reloads every UI surface (theme, detail layout, settings, page data).
func switchProfileTo(id string) {
	if id == currentProfID || id == "" {
		return
	}
	for _, p := range profiles {
		if p.ID != id {
			continue
		}
		currentProfID = id
		curProfDir = profileDir(id)
		saveCurrentProfileMeta()
		st = kb.New(curProfDir)
		loadThemeChoice()
		applyTheme()
		loadDetailLayout()
		renderPage()
		SetStatus("已切换到身份「%s」", p.Name)
		return
	}
}
