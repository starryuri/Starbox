//go:build windows

package main

import (
	"fmt"
	"time"
)

// uiStatus surfaces one-line user-visible state (errors, notices) without
// modal dialogs: it draws a status strip under the page title for a few
// seconds. Thread-safe: Set posts a refresh to the UI thread.

var (
	statusText  string
	statusIsErr bool
	statusUntil time.Time
	statusTimer int64 // atomic handle for expiry ticks
)

// SetStatus shows a neutral message for ~4 seconds.
func SetStatus(format string, args ...interface{}) {
	setStatusMsg(fmt.Sprintf(format, args...), false)
}

// SetError shows a red error message for ~6 seconds.
func SetError(format string, args ...interface{}) {
	setStatusMsg(fmt.Sprintf(format, args...), true)
}

func setStatusMsg(msg string, isErr bool) {
	statusText = msg
	statusIsErr = isErr
	statusUntil = time.Now().Add(4 * time.Second)
	if hwndMain != 0 {
		pPostMessage.Call(hwndMain, uintptr(wmStatusTick), 0, 0)
	}
}

// statusVisible reports whether the strip should paint right now.
func statusVisible() bool {
	return statusText != "" && time.Now().Before(statusUntil)
}

// paintStatusStrip draws the one-line status strip below the page title.
func paintStatusStrip(dc uintptr, x, y, w int) {
	if !statusVisible() {
		return
	}
	col := uintptr(colAcc)
	if statusIsErr {
		col = uintptr(0x000000E0) // soft red
	}
	fillRectColor(dc, x, y, w, 28, 0x00000000)
	drawTextRect(dc, x+8, y, w-16, 28, statusText, fontBody, col, 0x0025) // DT_SINGLELINE|DT_VCENTER
}
