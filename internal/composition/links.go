package composition

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/contracts"
	"github.com/Jake-Network/radar/internal/discovery"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/workspace"
)

// FileSides caches only declarations and documents required for link checks.
// A nil data slice without an error means the path is absent from the tree.
type FileSides struct {
	Base, Candidate       []byte
	BaseErr, CandidateErr error
}

type Link struct {
	contracts.LinkResult
	Producer     string   `json:"producer"`
	Consumer     string   `json:"consumer"`
	ProducerPath string   `json:"producer_path"`
	Pointer      string   `json:"pointer"`
	Direction    string   `json:"direction"`
	Warnings     []string `json:"warnings,omitempty"`
}

type SuggestedLink struct {
	Producer  string              `json:"producer"`
	Consumer  string              `json:"consumer"`
	Candidate discovery.Candidate `json:"candidate"`
}

func cacheFiles(ctx context.Context, res *Result, c *integration.Candidate, team *Team) {
	if c.Commit == "" {
		return
	}
	// Discover only committed candidate sources. It reads data and never executes
	// application code. Keeping reports in memory permits matching after Close.
	if report, err := discovery.Discover(ctx, c.Dir, c.Commit); err == nil {
		res.Discovery = &report
	}
	if team == nil {
		return
	}
	if res.ID == team.Home {
		if data, err := gitrepo.ReadFile(ctx, c.Dir, c.Commit, workspace.TeamPath); err == nil {
			if candidate, err := workspace.ParseTeam(data); err == nil {
				copy := *team
				copy.File = unionTeam(team.File, candidate)
				team = &copy
			}
		}
	}
	paths := map[string]bool{workspace.ConsumesPath: true}
	if res.ID == team.Home {
		paths[workspace.TeamPath] = true
	}
	for _, link := range team.File.Links {
		producer, _ := workspace.ParseProducer(link.Producer)
		if producer.Repo == res.ID {
			paths[producer.Path] = true
		}
	}
	res.Files = map[string]FileSides{}
	base, be := gitrepo.Entries(ctx, res.Path, res.Base)
	candidate, ce := gitrepo.Entries(ctx, c.Dir, c.Commit)
	read := func(root, ref, path string, entries []gitrepo.TreeEntry, listErr error) ([]byte, error) {
		if listErr != nil {
			return nil, listErr
		}
		for _, entry := range entries {
			if entry.Path == path {
				if entry.Mode != "100644" && entry.Mode != "100755" {
					return nil, fmt.Errorf("%s is not a regular file", path)
				}
				return gitrepo.ReadFile(ctx, root, ref, path)
			}
		}
		return nil, nil
	}
	for p := range paths {
		b, berr := read(res.Path, res.Base, p, base, be)
		v, verr := read(c.Dir, c.Commit, p, candidate, ce)
		res.Files[p] = FileSides{b, v, berr, verr}
	}
	for _, data := range [][]byte{res.Files[workspace.ConsumesPath].Base, res.Files[workspace.ConsumesPath].Candidate} {
		if file, err := workspace.ParseConsumes(data); err == nil {
			for _, consume := range file.Consumes {
				if consume.Source != "" {
					b, berr := read(res.Path, res.Base, consume.Source, base, be)
					v, verr := read(c.Dir, c.Commit, consume.Source, candidate, ce)
					res.Files[consume.Source] = FileSides{b, v, berr, verr}
				}
			}
		}
	}
}

// Suggestions proposes literal endpoint+method matches only for pairs where
// at least one repository has committed candidate changes. They are never
// authoritative links or verdict input.
func Suggestions(results []Result) []SuggestedLink {
	out := []SuggestedLink{}
	for _, p := range results {
		if p.Discovery == nil {
			continue
		}
		for _, c := range results {
			if c.ID == p.ID || c.Discovery == nil || (len(p.Branches) == 0 && len(c.Branches) == 0) {
				continue
			}
			for _, v := range discovery.MatchAcross(*p.Discovery, *c.Discovery) {
				ext := strings.ToLower(filepath.Ext(v.SchemaPath))
				if len(v.Fields) == 0 || (ext != ".json" && ext != ".yaml" && ext != ".yml") {
					continue
				}
				out = append(out, SuggestedLink{p.ID, c.ID, v})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Candidate.Fields) != len(out[j].Candidate.Fields) {
			return len(out[i].Candidate.Fields) > len(out[j].Candidate.Fields)
		}
		a, b := out[i], out[j]
		return a.Producer+"\x00"+a.Consumer+"\x00"+a.Candidate.ID < b.Producer+"\x00"+b.Consumer+"\x00"+b.Candidate.ID
	})
	return out
}

// CheckLinks uses base declarations even when the candidate removes them.
func CheckLinks(results []Result, team *Team) []Link {
	out := []Link{}
	if team == nil {
		return out
	}
	repos := map[string]Result{}
	for _, r := range results {
		repos[r.ID] = r
	}
	candidateTeam := team.File
	var teamErr error
	if home, ok := repos[team.Home]; ok {
		if data := home.Files[workspace.TeamPath]; data.CandidateErr != nil {
			teamErr = data.CandidateErr
		} else if data.Candidate == nil {
			candidateTeam = &workspace.TeamFile{Version: 1}
		} else {
			candidateTeam, teamErr = workspace.ParseTeam(data.Candidate)
		}
	} else {
		teamErr = fmt.Errorf("%s is outside the selected scope", team.Home)
	}
	declarations := team.File.Links
	if candidateTeam != nil {
		declarations = unionTeam(team.File, candidateTeam).Links
	}
	for _, decl := range declarations {
		p, _ := workspace.ParseProducer(decl.Producer)
		input := contracts.LinkInput{ID: decl.ID, Direction: decl.Direction, Pointer: p.Pointer, ConsumerBaseState: "absent", ConsumerCandidateState: "absent"}
		input.LinkBaseState = "absent"
		for _, base := range team.File.Links {
			if base.ID == decl.ID {
				input.LinkBaseState = "present"
				break
			}
		}
		var reason string
		for _, id := range []string{p.Repo, decl.Consumer} {
			r, ok := repos[id]
			if !ok {
				reason = id + " is outside the selected scope"
				break
			}
			if r.Report == nil {
				reason = id + " has no candidate"
				break
			}
			if len(r.Report.Conflicts) > 0 {
				reason = id + " has no candidate (conflict)"
				break
			}
		}
		if teamErr != nil {
			reason = "candidate team file is invalid: " + teamErr.Error()
		}
		if reason != "" {
			link := Link{LinkResult: contracts.LinkResult{ID: decl.ID, Status: model.StatusIncomplete}, Producer: p.Repo, Consumer: decl.Consumer, ProducerPath: p.Path, Pointer: p.Pointer, Direction: decl.Direction}
			for i, side := range [][2]string{{"base", "base"}, {"candidate", "base"}, {"base", "candidate"}, {"candidate", "candidate"}} {
				link.Cells[i] = contracts.CellResult{Producer: side[0], Consumer: side[1], Status: model.StatusIncomplete, Findings: []model.Finding{}, Reason: reason}
			}
			out = append(out, link)
			continue
		}
		data := repos[p.Repo].Files[p.Path]
		input.ProducerBaseErr = data.BaseErr
		input.ProducerCandidateErr = data.CandidateErr
		if data.Base != nil && input.ProducerBaseErr == nil {
			input.ProducerBase, input.ProducerBaseErr = contracts.DecodeDocumentAt(p.Path, data.Base)
		}
		if data.Candidate != nil && input.ProducerCandidateErr == nil {
			input.ProducerCandidate, input.ProducerCandidateErr = contracts.DecodeDocumentAt(p.Path, data.Candidate)
		}
		consume := repos[decl.Consumer].Files[workspace.ConsumesPath]
		parse := func(data []byte, err error) ([]string, string) {
			if err != nil {
				return nil, "invalid"
			}
			if data == nil {
				return nil, "absent"
			}
			file, err := workspace.ParseConsumes(data)
			if err != nil {
				return nil, "invalid"
			}
			for _, c := range file.Consumes {
				if c.Contract == decl.ID {
					return c.Fields, "present"
				}
			}
			return nil, "absent"
		}
		input.ConsumerBase, input.ConsumerBaseState = parse(consume.Base, consume.BaseErr)
		input.ConsumerCandidate, input.ConsumerCandidateState = parse(consume.Candidate, consume.CandidateErr)
		input.LinkCandidateState = "absent"
		for _, v := range candidateTeam.Links {
			if v.ID == decl.ID {
				input.LinkCandidateState = "present"
				if !reflect.DeepEqual(v, decl) {
					input.LinkCandidateState = "invalid"
				}
				break
			}
		}
		for _, v := range candidateTeam.Retired {
			existing := false
			for _, base := range team.File.Retired {
				if reflect.DeepEqual(v, base) {
					existing = true
				}
			}
			if !existing {
				input.Retirements = append(input.Retirements, v)
			}
		}
		link := Link{LinkResult: contracts.CheckLink(input), Producer: p.Repo, Consumer: decl.Consumer, ProducerPath: p.Path, Pointer: p.Pointer, Direction: decl.Direction}
		if file, err := workspace.ParseConsumes(consume.Candidate); err == nil {
			for _, entry := range file.Consumes {
				if entry.Contract == decl.ID && entry.Source != "" {
					if source := repos[decl.Consumer].Files[entry.Source]; source.Candidate == nil {
						link.Warnings = append(link.Warnings, decl.Consumer+":"+entry.Source+" source file is absent")
					}
				}
			}
		}
		out = append(out, link)
	}
	return out
}

func unionTeam(base, candidate *workspace.TeamFile) *workspace.TeamFile {
	copy := *base
	copy.Links = append([]workspace.Link(nil), base.Links...)
	ids := map[string]bool{}
	for _, v := range copy.Links {
		ids[v.ID] = true
	}
	for _, v := range candidate.Links {
		if !ids[v.ID] {
			copy.Links = append(copy.Links, v)
		}
	}
	return &copy
}
