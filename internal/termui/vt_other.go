//go:build !windows

package termui

import "os"

// Unix terminals interpret escape sequences without setup.
func enableVirtualTerminal(*os.File) bool { return true }
