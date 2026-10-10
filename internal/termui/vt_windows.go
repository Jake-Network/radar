//go:build windows

package termui

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVirtualTerminal asks the Windows console to interpret ANSI escape
// sequences. Files and pipes are not consoles and need no setup.
func enableVirtualTerminal(f *os.File) bool {
	h := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return true
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
