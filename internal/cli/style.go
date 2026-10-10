package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
)

// palette styles human output. Color carries no information of its own:
// every styled word keeps its text and mark (✓ ✗ ? ! ·), so plain output
// (pipes, files, --json, MCP, NO_COLOR) reads the same without it. Unknown,
// incomplete and not-run states are never styled as success.
type palette struct{ on bool }

func (p palette) paint(code, s string) string {
	if !p.on || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p palette) bold(s string) string   { return p.paint("1", s) }
func (p palette) dim(s string) string    { return p.paint("2", s) }
func (p palette) green(s string) string  { return p.paint("32", s) }
func (p palette) red(s string) string    { return p.paint("31", s) }
func (p palette) yellow(s string) string { return p.paint("33", s) }
func (p palette) cyan(s string) string   { return p.paint("36", s) }

// command highlights something to type.
func (p palette) command(s string) string { return p.paint("1;36", s) }

// styledWriter carries the palette to renderers that receive only a writer.
type styledWriter struct {
	io.Writer
	p palette
}

func paletteOf(w io.Writer) palette {
	if s, ok := w.(styledWriter); ok {
		return s.p
	}
	return palette{}
}

func styled(w io.Writer, on bool) io.Writer {
	if !on {
		return w
	}
	return styledWriter{Writer: w, p: palette{on: true}}
}

// verdictBadge colors the verdict word; static and bounded passes keep their
// qualifier unstyled-bold so they never read as an unconditional success.
func (p palette) verdict(v gate.Verdict, text string) string {
	if !p.on {
		return text
	}
	word, rest, _ := strings.Cut(text, " ")
	if rest != "" {
		rest = " " + p.bold(rest)
	}
	switch {
	case v == gate.Pass && strings.Contains(rest, "static"):
		// Static checks only: no tests ran, so no solid success badge.
		return p.paint("1;32", word) + rest
	case v == gate.Pass:
		return p.paint("1;30;42", " "+word+" ") + rest
	case v == gate.Fail:
		return p.paint("1;97;41", " "+word+" ") + rest
	case v == gate.Blocked:
		return p.paint("1;30;43", " "+text+" ")
	}
	return p.paint("1;97;45", " "+text+" ")
}

// mark colors a status mark by its meaning.
func (p palette) mark(m string) string {
	switch m {
	case "✓":
		return p.green(m)
	case "✗":
		return p.red(m)
	case "?", "!":
		return p.yellow(m)
	case "·":
		return p.dim(m)
	}
	return m
}

// status colors a status word (PASS, FAIL, WARN, …) used by the advanced
// commands' tables.
func (p palette) status(s model.Status, text string) string {
	switch s {
	case model.StatusPassed:
		return p.green(text)
	case model.StatusFailed, model.StatusError, model.StatusTimeout:
		return p.red(text)
	case model.StatusWarning, model.StatusBlocked, model.StatusIncomplete, model.StatusUnknown:
		return p.yellow(text)
	}
	return text
}

func (p palette) severity(s model.Severity, text string) string {
	switch s {
	case model.SeverityError:
		return p.red(text)
	case model.SeverityWarning:
		return p.yellow(text)
	}
	return p.dim(text)
}

// next renders "Next: …", highlighting the command to run.
func (p palette) next(text string) string {
	if i := strings.Index(text, "radar "); i >= 0 {
		return text[:i] + p.command(text[i:])
	}
	return text
}

// logo is Radar's terminal mark: a radar scope whose sweep has found a
// blip. Shown by the interactive menu and by help on a terminal.
var logo = []string{
	"   ╭───────────╮  ",
	" ╭─╯ ·      ●  ╰─╮",
	" │     ·  ╱      │",
	" ╰───────◉───────╯",
}

// printLogo writes the mark with a title beside its second row and a
// tagline beside its third.
func printLogo(w io.Writer, p palette, title, tagline string) {
	for i, line := range logo {
		if p.on {
			var b strings.Builder
			for _, r := range line {
				s := string(r)
				switch r {
				case '●':
					s = p.paint("1;31", s)
				case '◉':
					s = p.paint("1;32", s)
				case '╱', '·':
					s = p.green(s)
				case ' ':
				default:
					s = p.dim(s)
				}
				b.WriteString(s)
			}
			line = b.String()
		}
		switch i {
		case 1:
			line += "   " + p.bold(title)
		case 2:
			line += "   " + p.dim(tagline)
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
}
