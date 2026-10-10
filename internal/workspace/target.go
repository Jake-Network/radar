package workspace

import (
	"github.com/Jake-Network/radar/internal/jsonptr"
	"github.com/Jake-Network/radar/internal/pathutil"
	"strings"
)

// Producer identifies a schema in a workspace repository.
type Producer struct{ Repo, Path, Pointer string }

// ParseProducer parses repo:path#pointer without performing Git or file I/O.
func ParseProducer(value string) (Producer, error) {
	repo, rest, ok := strings.Cut(value, ":")
	if !ok {
		return Producer{}, errorf("use <REPO>:<PATH>#<POINTER>", "producer %q needs a repo ID and path", value)
	}
	if err := ValidID(repo); err != nil {
		return Producer{}, errorf("use a valid repo ID", "producer: %s", err)
	}
	p, pointer, _ := strings.Cut(rest, "#")
	clean, err := pathutil.RepoRelative(p)
	if err != nil || clean == "." || strings.Contains(p, ":") {
		return Producer{}, errorf("use a repository-relative schema file", "producer %q has an invalid schema path", value)
	}
	if err := jsonptr.Validate(pointer); err != nil {
		return Producer{}, errorf("use an RFC 6901 JSON pointer", "producer %q: %s", value, err)
	}
	return Producer{Repo: repo, Path: clean, Pointer: pointer}, nil
}

// Target is one positional gate argument: REF names a ref of the current
// repository and REPO:REF a ref of that workspace repository. Git ref names
// cannot contain ':', so the first ':' always separates the repo ID.
type Target struct {
	Repo      string `json:"repo"`
	Ref       string `json:"ref"`
	Qualified bool   `json:"-"`
}

// ParseTarget splits arg at its first ':'.
func ParseTarget(arg string) Target {
	if repo, ref, ok := strings.Cut(arg, ":"); ok {
		return Target{Repo: repo, Ref: ref, Qualified: true}
	}
	return Target{Ref: arg}
}

func (t Target) String() string {
	if t.Qualified {
		return t.Repo + ":" + t.Ref
	}
	return t.Ref
}

// ParseTargets applies the single-repository rule to every positional
// argument: an argument that names a valid target is kept whole (commas are
// legal in ref names); otherwise it is a comma-separated list of targets.
// valid reports whether a target names a known repository and an existing ref.
func ParseTargets(args []string, valid func(Target) bool) []Target {
	out := []Target{}
	for _, arg := range args {
		t := ParseTarget(arg)
		if valid(t) || !strings.Contains(arg, ",") {
			out = append(out, t)
			continue
		}
		for _, piece := range strings.Split(arg, ",") {
			if piece = strings.TrimSpace(piece); piece != "" {
				out = append(out, ParseTarget(piece))
			}
		}
	}
	return out
}
