// Package planning validates declared intent against evidence, without inventing a design.
package planning

import (
	"bytes"
	"encoding/json"

	"github.com/Jake-Network/radar/internal/model"
)

const SchemaVersion = "1"

type Requirement struct {
	ID                string `json:"id"`
	Intent            string `json:"intent"`
	SecuritySensitive bool   `json:"security_sensitive,omitempty"`
}
type Decision struct {
	ID           string   `json:"id"`
	Intent       string   `json:"intent"`
	Alternatives []string `json:"alternatives,omitempty"`
	Tradeoffs    []string `json:"tradeoffs,omitempty"`
}
type Constraint struct {
	ID     string `json:"id"`
	Intent string `json:"intent"`
	Rule   *Rule  `json:"rule,omitempty"`
}
type Rule struct {
	CWD        string   `json:"cwd,omitempty"`
	Kind       string   `json:"kind"`
	Command    []string `json:"command,omitempty"`
	EvidenceID string   `json:"evidence_id,omitempty"`
	Path       string   `json:"path,omitempty"`
	Entity     string   `json:"entity,omitempty"`
	Pointer    string   `json:"pointer,omitempty"`
	Property   string   `json:"property,omitempty"`
	// test_run execution environment. Every field is part of the reviewed plan.
	Setup [][]string `json:"setup,omitempty"` // commands run before Command in the snapshot
	Env   []string   `json:"env,omitempty"`   // caller environment variable names passed through
	Link  []string   `json:"link,omitempty"`  // untracked dependency paths linked from the checkout
	JUnit string     `json:"junit,omitempty"` // snapshot-relative JUnit XML report
}

// Assumption statuses. Open assumptions keep verification unknown; accepted
// (risk knowingly taken) and resolved (investigated) require a resolution.
const (
	AssumptionOpen     = "open"
	AssumptionAccepted = "accepted"
	AssumptionResolved = "resolved"
)

// Assumption is an unverified design premise with an explicit lifecycle.
type Assumption struct {
	ID         string `json:"id,omitempty"`
	Text       string `json:"text"`
	Status     string `json:"status,omitempty"`
	Resolution string `json:"resolution,omitempty"`
}

// UnmarshalJSON also accepts the original plain-string form.
func (a *Assumption) UnmarshalJSON(b []byte) error {
	var text string
	if json.Unmarshal(b, &text) == nil {
		*a = Assumption{Text: text}
		return nil
	}
	type plain Assumption
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var v plain
	if err := d.Decode(&v); err != nil {
		return err
	}
	*a = Assumption(v)
	return nil
}

// Open reports whether the assumption still needs investigation.
func (a Assumption) Open() bool { return a.Status == "" || a.Status == AssumptionOpen }

type Criterion struct {
	ID          string `json:"id"`
	Requirement string `json:"requirement"`
	Intent      string `json:"intent"`
	Rule        *Rule  `json:"rule,omitempty"`
}
type ContractDelta struct {
	Contract    string          `json:"contract"`
	Schema      string          `json:"schema,omitempty"`
	Pointer     string          `json:"pointer,omitempty"`
	Expected    json.RawMessage `json:"expected,omitempty"`
	Operation   string          `json:"operation"`
	Description string          `json:"description"`
	Consumers   []string        `json:"consumers,omitempty"`
}
type Task struct {
	ID            string   `json:"id"`
	Intent        string   `json:"intent"`
	Requirements  []string `json:"requirements"`
	Components    []string `json:"components,omitempty"`
	Contracts     []string `json:"contracts,omitempty"`
	DependsOn     []string `json:"depends_on"`
	Acceptance    []string `json:"acceptance"`
	Consequential bool     `json:"consequential,omitempty"`
}
type Delta struct {
	Operation string      `json:"operation"`
	Node      *model.Node `json:"node,omitempty"`
	Edge      *model.Edge `json:"edge,omitempty"`
}

// Approval records a local review declaration bound to plan contents and a checkpoint.
// Radar cannot authenticate the reviewer. A recorded approval never authorizes Git operations.
type Approval struct {
	Reviewer   string `json:"reviewer"`
	ReviewedAt string `json:"reviewed_at"`
	Checkpoint string `json:"checkpoint"`
	PlanDigest string `json:"plan_digest"`
}
type Plan struct {
	SchemaVersion  string             `json:"schema_version"`
	FeatureID      string             `json:"feature_id"`
	Intent         string             `json:"intent"`
	BaseRevision   string             `json:"base_revision"`
	Requirements   []Requirement      `json:"requirements"`
	Decisions      []Decision         `json:"decisions"`
	Constraints    []Constraint       `json:"constraints"`
	ContractDeltas []ContractDelta    `json:"contract_deltas"`
	Tasks          []Task             `json:"tasks"`
	Acceptance     []Criterion        `json:"acceptance"`
	GraphDeltas    []Delta            `json:"graph_deltas"`
	Assumptions    []Assumption       `json:"assumptions"`
	Evidence       []model.Provenance `json:"evidence"`
	Incomplete     []string           `json:"incomplete"`
	Approval       *Approval          `json:"approval,omitempty"`
	Context        *Context           `json:"context,omitempty"`
}
type Check struct {
	ID          string            `json:"id"`
	Status      model.Status      `json:"status"`
	Explanation string            `json:"explanation"`
	Evidence    model.Evidence    `json:"evidence"`
	Location    *model.Provenance `json:"location,omitempty"`
}
type TaskStatus struct {
	Checks      []string     `json:"checks,omitempty"`
	BlockedBy   []string     `json:"blocked_by,omitempty"`
	ID          string       `json:"id"`
	Status      model.Status `json:"status"`
	Explanation string       `json:"explanation"`
}
type AgentFeedback struct {
	TaskID   string       `json:"task_id"`
	Status   model.Status `json:"status"`
	Message  string       `json:"message"`
	CheckIDs []string     `json:"check_ids"`
}
type ContextMatch struct {
	Entity      string           `json:"entity"`
	Score       int              `json:"score"`
	Explanation string           `json:"explanation"`
	Evidence    model.Provenance `json:"evidence"`
}
type Context struct {
	Relationships []model.Edge   `json:"relationships"`
	Query         string         `json:"query"`
	Matches       []ContextMatch `json:"matches"`
	Unknown       []string       `json:"unknown"`
	PlanningDepth string         `json:"planning_depth"`
	Reason        string         `json:"reason"`
}
type Report struct {
	Revision      string          `json:"revision,omitempty"`
	BaseRevision  string          `json:"base_revision,omitempty"`
	PlanDigest    string          `json:"plan_digest,omitempty"`
	Tasks         []TaskStatus    `json:"tasks,omitempty"`
	Feedback      []AgentFeedback `json:"feedback,omitempty"`
	Status        model.Status    `json:"status"`
	Authoritative bool            `json:"authoritative"`
	Findings      []model.Finding `json:"findings"`
	Checks        []Check         `json:"checks"`
	NextSteps     []string        `json:"next_steps,omitempty"`
}
type TaskPacket struct {
	ContractDeltas []ContractDelta    `json:"contract_deltas"`
	GraphDeltas    []Delta            `json:"graph_deltas"`
	PlanDigest     string             `json:"plan_digest"`
	FeatureID      string             `json:"feature_id"`
	Decisions      []Decision         `json:"decisions"`
	Approval       *Approval          `json:"approval,omitempty"`
	Task           Task               `json:"task"`
	Requirements   []Requirement      `json:"requirements"`
	Acceptance     []Criterion        `json:"acceptance"`
	Constraints    []Constraint       `json:"constraints"`
	BaseRevision   string             `json:"base_revision"`
	Evidence       []model.Provenance `json:"evidence"`
}
type Schedule struct {
	PlanDigest     string       `json:"plan_digest"`
	Instructions   []TaskPacket `json:"instructions"`
	Order          []string     `json:"order"`
	ParallelGroups [][]string   `json:"parallel_groups"`
}
