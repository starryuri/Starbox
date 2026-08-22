//go:build !windows

package desktop

import "fmt"

// Open is a no-op stub on non-Windows platforms.
func Open(url string) {
	fmt.Println("desktop window is only supported on Windows:", url)
}
