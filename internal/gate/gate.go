// Package gate evaluates explicit verification requirements independently of
// analyzer coverage. It cannot execute commands or authorize human approval.
package gate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Jake-Network/radar/internal/model"
)

type Verdict string

const (
	Pass    Verdict = "pass"
	Fail    Verdict = "fail"
	Blocked Verdict = "blocked"
	Error   Verdict = "error"
)

type Check struct {
	ID          string         `json:"id"`
	Status      model.Status   `json:"status"`
	Evidence    model.Evidence `json:"evidence"`
	Explanation string         `json:"explanation"`
}
type Coverage struct {
	Analyzer    string       `json:"analyzer"`
	Status      model.Status `json:"status"`
	Scope       []string     `json:"analyzed_scope"`
	Excluded    []string     `json:"excluded_scope"`
	Unavailable []string     `json:"unavailable_capabilities"`
	Uncertainty []string     `json:"uncertainty"`
}
type Policy struct {
	Version   int      `json:"version"`
	Name      string   `json:"name,omitempty"`
	Require   []string `json:"require"`
	OnMissing string   `json:"on_missing,omitempty"`
}
type Result struct {
	Verdict     Verdict `json:"verdict"`
	Policy      Policy  `json:"policy"`
	Required    []Check `json:"required_checks"`
	Explanation string  `json:"explanation"`
}

func Load(path string) (Policy, error) {
	var p Policy
	f, e := os.Open(path)
	if e != nil {
		return p, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil {
		return p, e
	}
	if len(b) > 1<<20 {
		return p, fmt.Errorf("policy exceeds 1 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&p); e != nil {
		return p, fmt.Errorf("invalid policy: %w", e)
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return p, fmt.Errorf("policy must contain one JSON object")
	}
	return p, Validate(p)
}
func Validate(p Policy) error {
	if p.Version != 1 || len(p.Require) == 0 {
		return fmt.Errorf("policy requires version 1 and at least one check ID")
	}
	if p.OnMissing != "" && p.OnMissing != "blocked" && p.OnMissing != "fail" {
		return fmt.Errorf("on_missing must be blocked or fail")
	}
	seen := map[string]bool{}
	for _, id := range p.Require {
		if id == "" || seen[id] {
			return fmt.Errorf("required check IDs must be nonempty and unique")
		}
		seen[id] = true
	}
	return nil
}
func Evaluate(p Policy, checks []Check) Result {
	r := Result{Verdict: Pass, Policy: p, Required: []Check{}, Explanation: "All selected policy requirements passed; this does not establish universal software correctness."}
	if e := Validate(p); e != nil {
		r.Verdict = Error
		r.Explanation = e.Error()
		return r
	}
	byID := map[string]Check{}
	for _, c := range checks {
		if _, ok := byID[c.ID]; ok {
			r.Verdict = Error
			r.Explanation = "Ambiguous duplicate check ID: " + c.ID
			return r
		}
		byID[c.ID] = c
	}
	for _, id := range p.Require {
		c, ok := byID[id]
		if !ok {
			c = Check{ID: id, Status: model.StatusUnknown, Evidence: model.Unknown, Explanation: "Required evidence was not supplied."}
		}
		r.Required = append(r.Required, c)
		v := Pass
		switch c.Status {
		case model.StatusPassed:
		case model.StatusFailed:
			v = Fail
		case model.StatusError, model.StatusTimeout:
			v = Error
		default:
			v = Blocked
			if p.OnMissing == "fail" {
				v = Fail
			}
		}
		if rank(v) > rank(r.Verdict) {
			r.Verdict = v
		}
	}
	if r.Verdict != Pass {
		r.Explanation = "Selected policy requirements are not satisfied; inspect required_checks for failed, missing or execution-error evidence."
	}
	return r
}
func rank(v Verdict) int {
	switch v {
	case Error:
		return 3
	case Fail:
		return 2
	case Blocked:
		return 1
	}
	return 0
}
func Exit(r Result) int {
	switch r.Verdict {
	case Pass:
		return 0
	case Error:
		return 2
	default:
		return 1
	}
}

// NoBreaking reports only supported error-severity contract findings. Absence
// of such findings is distinct from complete schema/consumer coverage.
func NoBreaking(findings []model.Finding) Check {
	c := Check{ID: "no_breaking_contracts", Status: model.StatusPassed, Evidence: model.VerifiedStatic, Explanation: "No supported authoritative contract incompatibility was found; unknown and inferred relationships remain visible in coverage."}
	for _, f := range findings {
		if f.Contract != "" && f.Severity == model.SeverityError {
			c.Status = model.StatusFailed
			c.Explanation = "Supported declared contract incompatibility: " + f.Explanation
			break
		}
	}
	return c
}
func CoverageFor(checks []Check, limitations []string) []Coverage {
	out := []Coverage{}
	for _, c := range checks {
		if c.ID == "no_breaking_contracts" {
			continue
		}
		v := Coverage{Analyzer: c.ID, Status: c.Status, Scope: []string{}, Excluded: []string{}, Unavailable: []string{}, Uncertainty: []string{}}
		switch c.Status {
		case model.StatusPassed, model.StatusFailed, model.StatusWarning:
			v.Scope = []string{c.Explanation}
		default:
			v.Uncertainty = []string{c.Explanation}
		}
		switch c.ID {
		case "dependency_impact", "dependency_compatibility":
			v.Unavailable = []string{"compiler-resolved and runtime dependency behavior"}
		case "discovered_contracts":
			v.Excluded = append(v.Excluded, limitations...)
		case "integration_tests", "integration_execution":
			if c.Status == model.StatusUnknown {
				v.Unavailable = []string{"observed test evidence: no successful recognized test execution supplied"}
			}
		}
		out = append(out, v)
	}
	return out
}
