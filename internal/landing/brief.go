package landing

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/checkpoint"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/testselection"
)

type Hunk struct {
	Left  string `json:"left"`
	Right string `json:"right"`
}
type ConflictFile struct {
	Path     string   `json:"path"`
	Base     string   `json:"base"`
	Left     string   `json:"left"`
	Right    string   `json:"right"`
	Hunks    []Hunk   `json:"hunks"`
	Symbols  []string `json:"symbols"`
	Tests    []string `json:"tests"`
	Complete bool     `json:"complete"`
}
type ConflictBrief struct {
	Base         string         `json:"base"`
	Left         string         `json:"left"`
	Right        string         `json:"right"`
	Files        []ConflictFile `json:"files"`
	Reproduction []string       `json:"reproduction"`
	Evidence     model.Evidence `json:"evidence"`
	Limitations  []string       `json:"limitations"`
}

// Brief carries bounded committed source as requested repair material. Test
// links and symbol associations are inferred; no test coverage is claimed.
func Brief(ctx context.Context, root, base, left, right string) (ConflictBrief, error) {
	b := ConflictBrief{Files: []ConflictFile{}, Evidence: model.Inferred, Limitations: []string{"Symbol and test links are inferred from static file impact, not observed coverage.", "Source excerpts are capped at 64 KiB per version; incomplete excerpts must be inspected in Git."}}
	c, e := integration.BuildCandidate(ctx, root, base, []string{left, right})
	defer c.Close()
	if e != nil {
		return b, e
	}
	b.Base = c.Base
	b.Left = c.Inputs[0]
	b.Right = c.Inputs[1]
	b.Reproduction = []string{"radar", "land", "--base", b.Base, b.Left, b.Right, "--json"}
	snapshots := []model.Snapshot{}
	for _, ref := range c.Inputs {
		s, err := checkpoint.Index(ctx, root, ref)
		if err != nil {
			return b, err
		}
		snapshots = append(snapshots, s)

	}
	if len(c.Conflicts) > 64 {
		return b, errors.New("conflict brief exceeds 64-file budget; inspect individual pairs")
	}
	for _, path := range c.Conflicts {
		f := ConflictFile{Path: path, Complete: true, Hunks: []Hunk{}, Symbols: []string{}, Tests: []string{}}
		for i, ref := range []string{c.Base, c.Inputs[0], c.Inputs[1]} {
			data, err := gitrepo.ReadFile(ctx, root, ref, path)
			if err != nil { // Add/delete conflicts legitimately lack one version.
				files, listErr := gitrepo.Files(ctx, root, ref)
				if listErr != nil {
					return b, listErr
				}
				exists := false
				for _, v := range files {
					if v == path {
						exists = true
					}
				}
				if exists {
					return b, err
				}
			}
			if len(data) > 64<<10 {
				data = data[:64<<10]
				f.Complete = false
			}
			switch i {
			case 0:
				f.Base = string(data)
			case 1:
				f.Left = string(data)
			case 2:
				f.Right = string(data)
			}
		}
		abs, err := pathutil.ResolveInside(c.Dir, path)
		if err != nil {
			return b, err
		}
		data, err := os.ReadFile(abs)
		if err != nil && !os.IsNotExist(err) {
			return b, err
		}
		if len(data) > 256<<10 {
			data = data[:256<<10]
			f.Complete = false
		}
		f.Hunks = parseHunks(string(data))
		symbols := map[string]bool{}
		tests := map[string]bool{}
		for side, s := range snapshots {
			for _, node := range s.Nodes {
				if node.Provenance.Path == path && node.Kind != "file" && node.Kind != "repository" && node.Kind != "import" && touchesHunks(node.Provenance, f, side) {
					symbols[node.Name] = true
				}
			}
		}
		for side, snapshot := range snapshots {
			p, err := testselection.Recommend(ctx, root, c.Inputs[side], snapshot, []string{path}, nil)
			if err != nil {
				return b, err
			}
			for _, cmd := range p.Commands {
				for _, test := range cmd.TestFiles {
					tests[test] = true
				}
			}
		}
		for s := range symbols {
			f.Symbols = append(f.Symbols, s)
		}
		for s := range tests {
			f.Tests = append(f.Tests, s)
		}
		sort.Strings(f.Symbols)
		sort.Strings(f.Tests)
		b.Files = append(b.Files, f)
	}
	return b, nil
}
func parseHunks(text string) []Hunk {
	out := []Hunk{}
	var a, b strings.Builder
	state := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		switch {
		case strings.HasPrefix(line, "<<<<<<< "):
			state = 1
			a.Reset()
			b.Reset()
		case state == 1 && strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r") == "=======":
			state = 2
		case state == 2 && strings.HasPrefix(line, ">>>>>>> "):
			out = append(out, Hunk{a.String(), b.String()})
			state = 0
		case state == 1:
			a.WriteString(line)
		case state == 2:
			b.WriteString(line)
		}
	}
	return out
}

// Hunk text can occur more than once. Keep every intersecting declaration as
// an inferred association rather than inventing a unique semantic location.
func touchesHunks(p model.Provenance, f ConflictFile, side int) bool {
	source := f.Left
	if side == 1 {
		source = f.Right
	}
	for _, h := range f.Hunks {
		fragment := h.Left
		if side == 1 {
			fragment = h.Right
		}
		if fragment == "" {
			continue
		}
		offset := 0
		for offset < len(source) {
			at := strings.Index(source[offset:], fragment)
			if at < 0 {
				break
			}
			at += offset
			line := strings.Count(source[:at], "\n") + 1
			end := line + strings.Count(strings.TrimSuffix(fragment, "\n"), "\n")
			nodeEnd := p.EndLine
			if nodeEnd < p.Line {
				nodeEnd = p.Line
			}
			if p.Line <= end && nodeEnd >= line {
				return true
			}
			offset = at + len(fragment)
		}
	}
	return false
}
