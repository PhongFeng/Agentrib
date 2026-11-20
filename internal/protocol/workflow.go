package protocol

import (
	"strings"
	"time"
)

type Step struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Role          string   `json:"role"`
	Executor      string   `json:"executor"`
	Prompt        string   `json:"prompt,omitempty"`
	ReadOnly      bool     `json:"read_only,omitempty"`
	DependsOn     []string `json:"depends_on"`
	BaseFrom      string   `json:"base_from,omitempty"`
	MergeFrom     []string `json:"merge_from,omitempty"`
	QAGate        bool     `json:"qa_gate,omitempty"`
	ExportGame    bool     `json:"export_game,omitempty"`
	CIRepairAgent string   `json:"ci_repair_agent,omitempty"`
	Preflight     bool     `json:"preflight,omitempty"`
	QARework      bool     `json:"qa_rework,omitempty"`
}

type Bug struct {
	ID       string `json:"id,omitempty"`
	Owner    string `json:"owner"`
	Title    string `json:"title"`
	Steps    string `json:"steps"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

func ValidBugs(bugs []Bug) bool {
	if len(bugs) == 0 || len(bugs) > 8 {
		return false
	}
	for _, bug := range bugs {
		switch bug.Owner {
		case "art", "audio", "develop", "environment", "producer":
		default:
			return false
		}
		if (bug.ID != "" && !ticketID.MatchString(bug.ID)) || len(bug.Title) > 240 {
			return false
		}
		for _, field := range []string{bug.Title, bug.Steps, bug.Expected, bug.Actual} {
			if strings.TrimSpace(field) == "" || len(field) > 900 || strings.ContainsRune(field, 0) {
				return false
			}
		}
	}
	return true
}

type Workflow struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Name    string `json:"name"`
	Engine  string `json:"engine,omitempty"`
	Steps   []Step `json:"steps"`
}

type ExecutionInput struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflow_id"`
	Version    int    `json:"version"`
	MachineID  string `json:"machine_id"`
	Project    string `json:"project"`
	Brief      string `json:"brief"`
	Commit     string `json:"commit,omitempty"`
}

type Assignment struct {
	Owner   string   `json:"owner"`
	Helpers []string `json:"helpers,omitempty"`
}

type QADecision struct {
	ResumeAfter      string                `json:"resume_after,omitempty"`
	Dispatch         string                `json:"dispatch,omitempty"`
	Recheck          bool                  `json:"recheck,omitempty"`
	Assignments      map[string]Assignment `json:"assignments,omitempty"`
	Note             string                `json:"note,omitempty"`
	EnvironmentReady bool                  `json:"environment_ready,omitempty"`
}

type QAReview struct {
	Assignments map[string]Assignment `json:"assignments,omitempty"`
	RunID       string                `json:"run_id"`
	Phase       string                `json:"phase"`
	Reason      string                `json:"reason"`
	Roles       []string              `json:"roles,omitempty"`
}

type Execution struct {
	QAPhase   string                `json:"qa_phase,omitempty"`
	Decisions map[string]QADecision `json:"qa_decisions,omitempty"`
	Reviews   map[string]QAReview   `json:"qa_reviews,omitempty"`
	Tickets   map[string][]Ticket   `json:"qa_tickets,omitempty"`
	ExecutionInput
	Status   string    `json:"status"`
	Created  time.Time `json:"created"`
	Workflow Workflow  `json:"workflow"`
	Runs     []Run     `json:"runs"`
	Approved []string  `json:"approved"`
}
