package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/planning"
)

// ConfigurationInput binds the exact decoded invocation bytes. These inputs
// are independent of candidate commits; observations do not lock the filesystem.
type ConfigurationInput struct {
	Kind        string       `json:"kind"`
	Path        string       `json:"path"`
	Digest      string       `json:"digest"`
	FinalDigest string       `json:"final_digest,omitempty"`
	Status      model.Status `json:"status"`
	Explanation string       `json:"explanation"`
	regular     bool
}

type selectedConfiguration struct {
	Policy *gate.Policy
	Plan   *planning.Plan
	Inputs []ConfigurationInput
}

func readConfiguration(path string, limit int64) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, false, err
	}
	return data, info.Mode().IsRegular(), nil
}
func configurationDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (a *app) captureConfiguration(o options) (selectedConfiguration, error) {
	c := selectedConfiguration{Inputs: []ConfigurationInput{}}
	if o.policy != "" && o.strict {
		return c, errors.New("--policy and legacy --require-complete cannot be combined; select one gate model")
	}
	for _, item := range []struct {
		kind, path string
		limit      int64
	}{{"policy", o.policy, gate.MaxBytes}, {"plan", o.plan, planning.MaxBytes}} {
		if item.path == "" {
			continue
		}
		path := item.path
		if !filepath.IsAbs(path) && item.kind == "plan" {
			path = filepath.Join(a.root, path)
		} else if !filepath.IsAbs(path) {
			var err error
			path, err = pathutil.ResolveInside(a.root, path)
			if err != nil {
				return c, err
			}
		}
		data, regular, err := readConfiguration(path, item.limit)
		if err != nil {
			return c, err
		}
		if item.kind == "policy" {
			p, err := gate.Parse(data)
			if err != nil {
				return c, err
			}
			c.Policy = &p
		} else {
			p, err := planning.Parse(data)
			if err != nil {
				return c, err
			}
			c.Plan = &p
		}
		input := ConfigurationInput{Kind: item.kind, Path: path, Digest: configurationDigest(data), Status: model.StatusPassed, Explanation: "Exact captured bytes were decoded and used for this invocation.", regular: regular}
		if !regular {
			input.Status = model.StatusUnknown
			input.Explanation = "Exact stream bytes were captured once, decoded and used; stream stability cannot be checked by rereading."
		}
		c.Inputs = append(c.Inputs, input)
	}
	return c, nil
}
func (c *selectedConfiguration) finish() gate.Check {
	if len(c.Inputs) == 0 {
		return gate.Check{}
	}
	check := gate.Check{ID: "configuration_stability", Status: model.StatusPassed, Evidence: model.VerifiedStatic, Explanation: "Selected plan and policy bytes were stable at invocation boundaries. Source stability is evaluated separately; boundary observations cannot detect transient edits reverted between reads."}
	for i := range c.Inputs {
		input := &c.Inputs[i]
		if !input.regular {
			if check.Status == model.StatusPassed {
				check.Status, check.Evidence = model.StatusUnknown, model.Unknown
				check.Explanation = "Selected stream bytes were captured once; stream stability cannot be independently checked."
			}
			continue
		}
		limit := int64(planning.MaxBytes)
		if input.Kind == "policy" {
			limit = gate.MaxBytes
		}
		info, err := os.Stat(input.Path)
		if err == nil && !info.Mode().IsRegular() {
			err = errors.New("configuration is no longer a regular file")
		}
		var data []byte
		regular := false
		if err == nil {
			data, regular, err = readConfiguration(input.Path, limit)
		}
		if err == nil && regular {
			input.FinalDigest = configurationDigest(data)
		}
		if err != nil || !regular || input.FinalDigest != input.Digest {
			input.Status = model.StatusIncomplete
			input.Explanation = "Selected configuration changed or became unavailable after capture; rerun using stable artifacts."
			if err != nil {
				input.Explanation += " " + err.Error()
			}
			check.Status, check.Evidence = model.StatusIncomplete, model.Unknown
			check.Explanation = "Selected plan or policy changed during analysis; captured digests identify the inputs actually used. Rerun using stable artifacts."
		}
	}
	return check
}
func applyConfiguration(check gate.Check, checks []gate.Check, result gate.Result) ([]gate.Check, gate.Result) {
	if check.ID == "" {
		return checks, result
	}
	if check.Status == model.StatusIncomplete {
		for i := range checks {
			if checks[i].Status == model.StatusPassed {
				checks[i].Status, checks[i].Evidence = model.StatusIncomplete, model.Unknown
				checks[i].Explanation += " Selected configuration changed; this observation cannot satisfy the gate."
			}
		}
	}
	checks = append(checks, check)
	policy := result.Policy
	if check.Status == model.StatusIncomplete {
		policy.Require = append([]string(nil), policy.Require...)
		found := false
		for _, id := range policy.Require {
			if id == check.ID {
				found = true
			}
		}
		if !found {
			policy.Require = append(policy.Require, check.ID)
		}
	}
	return checks, gate.Evaluate(policy, checks)
}
func (c *selectedConfiguration) applyIntegration(r *integration.Report) {
	check := c.finish()
	r.Checks, r.Gate = applyConfiguration(check, r.Checks, r.Gate)
	if check.Status == model.StatusIncomplete && r.Status != model.StatusFailed && r.Status != model.StatusError {
		r.Status = model.StatusIncomplete
	}
	if check.Status == model.StatusIncomplete {
		clearRecord := func(ev *integration.ExecutionEvidence) {
			if ev.PlanRecord == nil {
				return
			}
			ev.PlanRecord = nil
			ev.ID = ""
			data, _ := json.Marshal(*ev)
			ev.ID = model.StableID("integration-evidence-v1", string(data))
		}
		for i := range r.Executions {
			clearRecord(&r.Executions[i])
		}
		if r.Execution != nil {
			clearRecord(r.Execution)
		}
	}
	r.Coverage = gate.CoverageFor(r.Checks, r.Limitations)
}

func configurationUnstable(inputs []ConfigurationInput) bool {
	for _, input := range inputs {
		if input.Status == model.StatusIncomplete {
			return true
		}
	}
	return false
}
