package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/contracts"
	"github.com/Jake-Network/radar/internal/pathutil"
)

const TeamPath = ".radar/workspace.json"
const ConsumesPath = ".radar/consumes.json"

// TeamFile is the committed declaration of repository membership and links.
type TeamFile struct {
	Version int                    `json:"version"`
	Repos   []TeamRepo             `json:"repos"`
	Links   []Link                 `json:"links"`
	Retired []contracts.Retirement `json:"retired,omitempty"`
}
type TeamRepo struct {
	ID       string `json:"id"`
	Identity string `json:"identity"`
	Base     string `json:"base,omitempty"`
}
type Link struct {
	ID        string `json:"id"`
	Direction string `json:"direction,omitempty"`
	Producer  string `json:"producer"`
	Consumer  string `json:"consumer"`
}
type ConsumesFile struct {
	Version  int       `json:"version"`
	Consumes []Consume `json:"consumes"`
}
type Consume struct {
	Contract string   `json:"contract"`
	Fields   []string `json:"fields"`
	Source   string   `json:"source,omitempty"`
}

func decodeTeamJSON(data []byte, dst any, name string) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		next := "repair and commit " + name
		if strings.Contains(err.Error(), "unknown field") {
			next = "update Radar to support this file format, or remove unsupported fields"
		}
		return errorf(next, "invalid %s: %s", name, err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errorf("repair and commit "+name, "invalid %s: expected one JSON object", name)
	}
	return nil
}

// ParseTeam validates a team declaration without reading any repository files.
func ParseTeam(data []byte) (*TeamFile, error) {
	var team TeamFile
	if err := decodeTeamJSON(data, &team, TeamPath); err != nil {
		return nil, err
	}
	bad := func(format string, args ...any) (*TeamFile, error) {
		return nil, errorf("repair and commit "+TeamPath, format, args...)
	}
	if team.Version != 1 {
		return nil, errorf("update Radar to support this team format", "unsupported team file version %d", team.Version)
	}
	if len(team.Repos) == 0 {
		return bad("team file needs at least one repo")
	}
	ids := map[string]bool{}
	for _, r := range team.Repos {
		if err := ValidID(r.ID); err != nil {
			return bad("team repo: %s", err)
		}
		if ids[r.ID] {
			return bad("duplicate team repo %q", r.ID)
		}
		if !validIdentity(r.Identity) {
			return bad("team repo %q needs a git root-commit identity", r.ID)
		}
		if strings.ContainsAny(r.Base, "\x00\r\n") || strings.HasPrefix(r.Base, "-") {
			return bad("team repo %q has an invalid base ref", r.ID)
		}
		ids[r.ID] = true
	}
	links := map[string]bool{}
	for _, l := range team.Links {
		if err := ValidID(l.ID); err != nil {
			return bad("team link: %s", err)
		}
		if links[l.ID] {
			return bad("duplicate team link %q", l.ID)
		}
		links[l.ID] = true
		p, err := ParseProducer(l.Producer)
		if err != nil {
			return nil, err
		}
		if !ids[p.Repo] || !ids[l.Consumer] {
			return bad("link %q producer and consumer must name team repos", l.ID)
		}
		if p.Repo == l.Consumer {
			return bad("link %q must connect different repositories", l.ID)
		}
		if l.Direction != "" && l.Direction != "request" && l.Direction != "response" {
			return bad("link %q direction must be request or response", l.ID)
		}
	}
	retired := map[string]bool{}
	for _, r := range team.Retired {
		if err := ValidID(r.ID); err != nil {
			return bad("retirement: %s", err)
		}
		key := r.ID + "\x00" + strings.Join(r.Fields, "\x00")
		if retired[key] || strings.TrimSpace(r.Reason) == "" {
			return bad("retirements require unique entries and nonempty reasons")
		}
		if err := validateFields(r.Fields, false); err != nil {
			return bad("retirement %q: %s", r.ID, err)
		}
		retired[key] = true
	}
	return &team, nil
}

func validIdentity(id string) bool {
	if !strings.HasPrefix(id, "git:") {
		return false
	}
	seen := map[string]bool{}
	for _, sha := range strings.Split(strings.TrimPrefix(id, "git:"), "+") {
		if (len(sha) != 40 && len(sha) != 64) || seen[sha] {
			return false
		}
		for _, c := range sha {
			if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
				return false
			}
		}
		seen[sha] = true
	}
	return true
}

func validateFields(fields []string, required bool) error {
	if required && len(fields) == 0 {
		return fmt.Errorf("fields must not be empty")
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if f == "" || strings.TrimSpace(f) != f || strings.ContainsAny(f, "\x00\r\n") || seen[f] {
			return fmt.Errorf("fields require unique nonempty paths")
		}
		for _, part := range strings.Split(f, ".") {
			if part == "" || strings.TrimSuffix(part, "[]") == "" || strings.ContainsAny(strings.TrimSuffix(part, "[]"), "[]") {
				return fmt.Errorf("field %q must use dot paths and [] array notation", f)
			}
		}
		seen[f] = true
	}
	return nil
}

// ParseConsumes validates consumer expectations; malformed entries stay errors.
func ParseConsumes(data []byte) (*ConsumesFile, error) {
	var file ConsumesFile
	if err := decodeTeamJSON(data, &file, ConsumesPath); err != nil {
		return nil, err
	}
	bad := func(format string, args ...any) (*ConsumesFile, error) {
		return nil, errorf("repair and commit "+ConsumesPath, format, args...)
	}
	if file.Version != 1 {
		return nil, errorf("update Radar to support this consumes format", "unsupported consumes file version %d", file.Version)
	}
	ids := map[string]bool{}
	for _, c := range file.Consumes {
		if err := ValidID(c.Contract); err != nil {
			return bad("consume contract: %s", err)
		}
		if ids[c.Contract] {
			return bad("duplicate consume contract %q", c.Contract)
		}
		ids[c.Contract] = true
		if err := validateFields(c.Fields, true); err != nil {
			return bad("consume %q: %s", c.Contract, err)
		}
		if c.Source != "" {
			if _, err := pathutil.RepoRelative(c.Source); err != nil {
				return bad("consume %q source: %s", c.Contract, err)
			}
		}
	}
	return &file, nil
}

// ResolveTeamScope applies team membership before --only, retaining explicit
// --with additions. member reports whether a registered repository is the
// one a team entry identifies. A team repo with no usable registered location
// is kept as Missing, so it is reported (or excluded by --only) instead of
// stopping the run; a registered repository with another identity is an error.
func ResolveTeamScope(scope Scope, team *TeamFile, member func(ScopeRepo, string) bool) (Scope, error) {
	if team == nil {
		return applyTeamOnly(scope)
	}
	out := scope
	out.Source = SourceTeamFile
	out.Repos = nil
	out.All = nil
	out.Excluded = nil
	available := map[string]ScopeRepo{}
	for _, r := range scope.Repos {
		available[r.ID] = r
	}
	teamIDs := map[string]bool{}
	for _, r := range team.Repos {
		teamIDs[r.ID] = true
		sr, ok := available[r.ID]
		if !ok {
			sr = ScopeRepo{ID: r.ID, Origin: SourceTeamFile, Missing: "team repo is not registered on this machine"}
		}
		if sr.Missing == "" && !member(sr, r.Identity) {
			return Scope{}, errorf("radar workspace add <PATH> --id "+r.ID, "team repo %q identity differs from its registered repository", r.ID)
		}
		sr.TeamBase = r.Base
		out.Repos = append(out.Repos, sr)
	}
	for _, r := range scope.Repos {
		if !teamIDs[r.ID] {
			if r.Origin == SourceWith {
				out.Repos = append(out.Repos, r)
			} else {
				out.Excluded = append(out.Excluded, r)
			}
		}
	}
	sort.Slice(out.Repos, func(i, j int) bool { return out.Repos[i].ID < out.Repos[j].ID })
	for _, r := range out.Repos {
		out.All = append(out.All, r.ID)
	}
	return applyTeamOnly(out)
}

func applyTeamOnly(out Scope) (Scope, error) {
	if len(out.Only) > 0 {
		keep := map[string]bool{}
		only := []string{}
		for _, id := range out.Only {
			if !containsID(out.All, id) {
				return Scope{}, out.unknown(id, "--only")
			}
			if !keep[id] {
				keep[id] = true
				only = append(only, id)
			}
		}
		out.Only = only
		selected := []ScopeRepo{}
		for _, r := range out.Repos {
			if keep[r.ID] {
				selected = append(selected, r)
			}
		}
		out.Repos = selected
	}
	return out, nil
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
