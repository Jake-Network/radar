package cli

import (
	"encoding/json"
	"io"
	"os"

	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/runners"
)

type runnerSetupProposal struct {
	Source string     `json:"source"`
	Setup  [][]string `json:"setup,omitempty"`
	Note   string     `json:"note"`
}

// supersetSetupProposal reads bounded data, never scripts. Shell command strings
// stay opaque: only an explicit argv matrix can be copied as runner setup.
func supersetSetupProposal(root string) *runnerSetupProposal {
	p, err := pathutil.ResolveInside(root, ".superset/config.json")
	if err != nil {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil
	}
	var config struct {
		Setup   json.RawMessage `json:"setup"`
		Scripts struct {
			Setup json.RawMessage `json:"setup"`
		} `json:"scripts"`
	}
	if json.Unmarshal(data, &config) != nil {
		return &runnerSetupProposal{Source: ".superset/config.json", Note: "Malformed configuration; no setup proposed or executed."}
	}
	raw := config.Setup
	if len(raw) == 0 {
		raw = config.Scripts.Setup
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	proposal := &runnerSetupProposal{Source: ".superset/config.json", Note: "Review setup and declare argv in .radar/runners.json; nothing was executed."}
	var argv [][]string
	if json.Unmarshal(raw, &argv) != nil || len(argv) == 0 || len(argv) > 16 {
		return proposal
	}
	for _, command := range argv {
		if runners.ValidateArgv(command) != nil {
			return proposal
		}
	}
	proposal.Setup = argv
	return proposal
}
