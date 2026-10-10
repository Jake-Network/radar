package composition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/workspace"
)

// Team pins the authoritative team declaration to its home repository and blob.
type Team struct {
	File        *workspace.TeamFile `json:"-"`
	Home        string              `json:"home"`
	Path        string              `json:"path"`
	Blob        string              `json:"blob"`
	Base        string              `json:"base"`
	BaseRef     string              `json:"-"`
	Digest      string              `json:"digest"`
	Uncommitted bool                `json:"uncommitted"`
	locator     *workspace.ScopeRepo
}

// LoadTeam reads only committed declarations. With no saved home it accepts
// exactly one declaration among the registered base trees. Replay reads the
// recorded base and verifies its recorded blob, never the current branch.
func LoadTeam(ctx context.Context, scope workspace.Scope, home string, bases map[string]string, replay *Record) (*Team, error) {
	if replay != nil && replay.Team == nil {
		return nil, nil
	}
	if replay != nil {
		home = replay.Team.Home
		if replay.TeamHome != nil && !scope.Has(home) {
			scope.Repos = append(scope.Repos, *replay.TeamHome)
		}
	}
	var found *Team
	for _, repo := range scope.Repos {
		if home != "" && repo.ID != home {
			continue
		}
		if repo.Missing != "" {
			if home == repo.ID {
				return nil, &workspace.Error{Message: repo.ID + ": " + repo.Missing, Next: "radar workspace add <PATH> --id " + repo.ID}
			}
			continue
		}
		ref := bases[repo.ID]
		if ref == "" {
			ref = gitrepo.DefaultBranch(ctx, repo.Path)
		}
		if replay != nil {
			ref = replay.Team.Base
		}
		if ref == "" {
			continue
		}
		sha, err := gitrepo.Resolve(ctx, repo.Path, ref)
		if err != nil {
			return nil, &workspace.Error{Message: fmt.Sprintf("%s: team base %q is not a commit", repo.ID, ref), Next: "radar gate --base " + repo.ID + ":<REF>"}
		}
		entries, err := gitrepo.Entries(ctx, repo.Path, sha)
		if err != nil {
			return nil, err
		}
		var blob string
		for _, entry := range entries {
			if entry.Path == workspace.TeamPath {
				if entry.Type != "blob" || (entry.Mode != "100644" && entry.Mode != "100755") {
					return nil, &workspace.Error{Message: "team file must be a regular file", Next: "repair and commit " + workspace.TeamPath + ", then: radar gate --again"}
				}
				blob = entry.OID
			}
		}
		if blob == "" {
			if replay != nil {
				return nil, &workspace.Error{Message: "not reproducible: team file is unavailable", Next: "radar gate --again"}
			}
			continue
		}
		if replay != nil && blob != replay.Team.Blob {
			return nil, &workspace.Error{Message: "not reproducible: team file changed", Next: "radar gate --again"}
		}
		data, err := gitrepo.ReadFile(ctx, repo.Path, sha, workspace.TeamPath)
		if err != nil {
			return nil, err
		}
		file, err := workspace.ParseTeam(data)
		if err != nil {
			return nil, err
		}
		if found != nil {
			return nil, &workspace.Error{Message: "more than one repository has a team file; choose one home declaration", Next: "keep one committed " + workspace.TeamPath + ", then: radar gate --again"}
		}
		found = &Team{File: file, Home: repo.ID, Path: workspace.TeamPath, Blob: blob, Base: sha, BaseRef: ref, Digest: teamDigest(file), locator: &repo}
		wt, err := gitrepo.InspectWorktrees(ctx, repo.Path)
		if err != nil {
			return nil, err
		}
		for _, w := range wt {
			for _, list := range [][]string{w.Staged, w.Unstaged, w.Untracked} {
				for _, p := range list {
					if p == workspace.TeamPath {
						found.Uncommitted = true
					}
				}
			}
		}
	}
	if replay != nil && found == nil {
		return nil, &workspace.Error{Message: "not reproducible: home repository is unavailable", Next: "radar workspace add <PATH> --id " + home}
	}

	if found != nil && replay == nil && bases[found.Home] == "" {
		for _, repo := range found.File.Repos {
			if repo.ID == found.Home && repo.Base != "" {
				selected, err := gitrepo.Resolve(ctx, found.locator.Path, repo.Base)
				if err != nil {
					return nil, &workspace.Error{Message: "home team base is not a commit", Next: "radar gate --base " + repo.ID + ":<REF>"}
				}
				if selected != found.Base {
					override := map[string]string{}
					for id, ref := range bases {
						override[id] = ref
					}
					override[found.Home] = repo.Base
					declared, err := LoadTeam(ctx, scope, found.Home, override, nil)
					if err == nil && declared == nil {
						// The declarations on this base name the base to read; one
						// without the file would silently drop every declared link.
						return nil, &workspace.Error{Message: fmt.Sprintf("%s: the team file names base %q for its home repository, and that base has no %s", found.Home, repo.Base, workspace.TeamPath), Next: "commit " + workspace.TeamPath + " to " + repo.Base + ", or fix the home repo's base in the team file"}
					}
					return declared, err
				}
			}
		}
	}
	if replay != nil && found != nil && found.Digest != replay.Team.Digest {
		return nil, &workspace.Error{Message: "not reproducible: team digest differs", Next: "radar gate --again"}
	}
	return found, nil
}

func teamDigest(file *workspace.TeamFile) string {
	copy := *file
	copy.Repos = append([]workspace.TeamRepo(nil), file.Repos...)
	copy.Links = append([]workspace.Link(nil), file.Links...)
	sort.Slice(copy.Repos, func(i, j int) bool { return copy.Repos[i].ID < copy.Repos[j].ID })
	sort.Slice(copy.Links, func(i, j int) bool { return copy.Links[i].ID < copy.Links[j].ID })
	data, _ := json.Marshal(copy)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Member reports whether a registered repository holds a team identity,
// independently of checkout paths and of the checked-out branch.
func Member(ctx context.Context) func(workspace.ScopeRepo, string) bool {
	return func(r workspace.ScopeRepo, identity string) bool {
		return gitrepo.HasIdentity(ctx, r.Path, identity)
	}
}
