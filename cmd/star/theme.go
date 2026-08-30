//go:build windows

package main

// theme.go — multi-theme engine for STARBOX.
//
// Built-in themes: "night" (暗夜, navy+cyan), "sakura" (樱夜, dark+pink),
// "day" (白天, light). Switching repaints live and persists to data/theme.json.

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// palette holds every themeable color as COLORREF (0x00BBGGRR).
type palette struct {
	Bg    uintptr
	Side  uintptr
	Acc   uintptr
	Fg    uintptr
	OnAcc uintptr
	Card  uintptr
	Card2 uintptr
	Dim   uintptr
	Red   uintptr
}

// theme is one named look.
type theme struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Colors palette `json:"-"`
}

var (
	themes        = []theme{themeNight, themeSakura, themeDay}
	activeThemeID = "night"
)

var (
	themeNight = theme{
		ID:   "night",
		Name: "暗夜",
		Colors: palette{
			Bg: 0x20100c, Side: 0x2b1610, Acc: 0xeed322, Fg: 0xf7ece7,
			OnAcc: 0x170e0b, Card: 0x4a3c20, Card2: 0x60502e,
			Dim: 0x8f8271, Red: 0x2e201a,
		},
	}
	themeSakura = theme{
		ID:   "sakura",
		Name: "樱夜",
		Colors: palette{
			Bg: 0x1a1420, Side: 0x2a1e30, Acc: 0x9cc0f5, Fg: 0xf0e8f2,
			OnAcc: 0x241430, Card: 0x3a2c44, Card2: 0x52405e,
			Dim: 0x9c8a9c, Red: 0x30202a,
		},
	}
	themeDay = theme{
		ID:   "day",
		Name: "白天",
		Colors: palette{
			Bg: 0xf3ece5, Side: 0xe6e0ee, Acc: 0xa04808, Fg: 0x241a26,
			OnAcc: 0x00e0f4, Card: 0xf0f2f4, Card2: 0xd8e2e8,
			Dim: 0x887870, Red: 0x3818e0,
		},
	}
)

func activeTheme() *theme {
	for i := range themes {
		if themes[i].ID == activeThemeID {
			return &themes[i]
		}
	}
	return &themes[0]
}

// applyTheme copies the palette into the global color vars and refreshes the
// cached brushes. Invalidate everything after calling.
func applyTheme() {
	p := activeTheme().Colors
	colBg = p.Bg
	colSide = p.Side
	colAcc = p.Acc
	colFg = p.Fg
	colOnAcc = p.OnAcc
	colCard = p.Card
	colCard2 = p.Card2
	colDim = p.Dim
	colRed = p.Red
	if brushBg != 0 {
		pDeleteObject.Call(brushBg)
	}
	if brushCard != 0 {
		pDeleteObject.Call(brushCard)
	}
	brushBg, _, _ = pCreateSolidBrush.Call(colBg)
	brushCard, _, _ = pCreateSolidBrush.Call(colCard)
}

func themeFilePath() string { return filepath.Join(curProfDir, "theme.json") }

func loadThemeChoice() {
	b, err := os.ReadFile(themeFilePath())
	if err != nil {
		return
	}
	var v struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(b, &v) == nil && v.ID != "" {
		for _, th := range themes {
			if th.ID == v.ID {
				activeThemeID = v.ID
				return
			}
		}
	}
}

func saveThemeChoice() {
	b, _ := json.MarshalIndent(struct {
		ID string `json:"id"`
	}{activeThemeID}, "", "  ")
	_ = os.MkdirAll(dataDir, 0o755)
	_ = os.WriteFile(themeFilePath(), b, 0o644)
}

// switchTheme flips to the named theme, repaints everything and persists.
func switchTheme(id string) {
	for _, th := range themes {
		if th.ID == id {
			activeThemeID = id
			applyTheme()
			webDataVer++ // page colors follow the theme
			saveThemeChoice()
			if hwndMain != 0 {
				pInvalidateRect.Call(hwndMain, 0, 1)
			}
			return
		}
	}
}
