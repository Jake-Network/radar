// Package termui decides whether human-readable output may use terminal
// styling (ANSI color, live progress). It is presentation only: callers never
// style machine-readable output, and the decision is local to one process.
package termui

import (
	"fmt"
	"io"
	"os"

	"github.com/mattn/go-isatty"
)

// Mode is the --color setting.
type Mode string

const (
	Auto   Mode = "auto"
	Always Mode = "always"
	Never  Mode = "never"
)

// ParseMode accepts auto, always or never.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case Auto, Always, Never:
		return m, nil
	}
	return "", fmt.Errorf("--color must be auto, always or never, not %q", s)
}

// IsTerminal reports whether w is an interactive terminal. Pipes, files,
// buffers and /dev/null are not.
func IsTerminal(w any) bool {
	f, ok := w.(*os.File)
	if !ok || f == nil {
		return false
	}
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// Color reports whether output to w may carry ANSI styling. An explicit
// always or never wins; in auto mode NO_COLOR disables color, FORCE_COLOR or
// CLICOLOR_FORCE enables it, TERM=dumb disables it, and otherwise only a
// terminal that accepts escape sequences gets color.
func Color(w io.Writer, mode Mode, getenv func(string) string) bool {
	switch mode {
	case Always:
		enable(w)
		return true
	case Never:
		return false
	}
	if getenv("NO_COLOR") != "" {
		return false
	}
	for _, name := range []string{"FORCE_COLOR", "CLICOLOR_FORCE"} {
		if v := getenv(name); v != "" && v != "0" && v != "false" {
			enable(w)
			return true
		}
	}
	if getenv("TERM") == "dumb" {
		return false
	}
	return IsTerminal(w) && enable(w)
}

// Live reports whether w can show transient progress lines that are
// rewritten in place: a real terminal, not a pipe or a CI log.
func Live(w io.Writer, getenv func(string) string) bool {
	return IsTerminal(w) && getenv("TERM") != "dumb" && enable(w)
}

// enable turns on escape-sequence processing where the console needs it
// (Windows); it reports whether escape sequences will be interpreted.
func enable(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || f == nil {
		return true
	}
	return enableVirtualTerminal(f)
}
