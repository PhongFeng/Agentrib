package protocol

import "time"

type Machine struct {
	ID                 string                 `json:"id"`
	Name               string                 `json:"name"`
	Executors          []string               `json:"executors"`
	AgentStatuses      map[string]AgentStatus `json:"agent_statuses,omitempty"`
	Projects           []string               `json:"projects"`
	ProjectEngines     map[string]string      `json:"project_engines,omitempty"`
	CreateProjects     bool                   `json:"create_projects,omitempty"`
	BriefChat          bool                   `json:"brief_chat,omitempty"`
	ProductionWorkflow bool                   `json:"production_workflow,omitempty"`
	WorkflowReview     bool                   `json:"workflow_review,omitempty"`
	Seen               time.Time              `json:"seen"`
}

type Enrollment struct {
	Machine Machine `json:"machine"`
	Token   string  `json:"token"`
}

type Heartbeat struct {
	Executors          []string               `json:"executors"`
	AgentStatuses      map[string]AgentStatus `json:"agent_statuses,omitempty"`
	Projects           []string               `json:"projects"`
	ProjectEngines     map[string]string      `json:"project_engines,omitempty"`
	ProjectWorkspaces  map[string]string      `json:"project_workspaces,omitempty"`
	CreateProjects     bool                   `json:"create_projects,omitempty"`
	BriefChat          bool                   `json:"brief_chat,omitempty"`
	ProductionWorkflow bool                   `json:"production_workflow,omitempty"`
	WorkflowReview     bool                   `json:"workflow_review,omitempty"`
	ProjectResult      *ProjectResult         `json:"project_result,omitempty"`
}

type Spec struct {
	MachineID    string   `json:"machine_id"`
	Project      string   `json:"project"`
	Engine       string   `json:"engine,omitempty"`
	Executor     string   `json:"executor"`
	Title        string   `json:"title"`
	Prompt       string   `json:"prompt"`
	Commit       string   `json:"commit,omitempty"`
	ReadOnly     bool     `json:"read_only,omitempty"`
	BriefMode    string   `json:"brief_mode,omitempty"`
	MergeCommits []string `json:"merge_commits,omitempty"`
	QAGate       bool     `json:"qa_gate,omitempty"`
	ExportGame   bool     `json:"export_game,omitempty"`
	QADispatch   string   `json:"qa_dispatch,omitempty"`
	RepairOf     string   `json:"repair_of,omitempty"`
	Preflight    bool     `json:"preflight,omitempty"`
	QARework     bool     `json:"qa_rework,omitempty"`
	RecheckOf    string   `json:"recheck_of,omitempty"`
}

type Run struct {
	ID          string    `json:"id"`
	Spec        Spec      `json:"spec"`
	Status      string    `json:"status"`
	Lease       string    `json:"lease,omitempty"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
	Result      *Result   `json:"result,omitempty"`
	ExecutionID string    `json:"execution_id,omitempty"`
	StepID      string    `json:"step_id,omitempty"`
	Attempt     int       `json:"attempt,omitempty"`
}

type Event struct {
	Seq  int    `json:"seq"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type Artifact struct {
	Name   string `json:"name"`
	Digest string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type Result struct {
	Status    string     `json:"status"`
	Detail    string     `json:"detail"`
	Commit    string     `json:"commit,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
	Reply     string     `json:"reply,omitempty"`
	Verified  []string   `json:"verified,omitempty"`
	Bugs      []Bug      `json:"bugs,omitempty"`
}

type Report struct {
	Lease  string  `json:"lease"`
	Events []Event `json:"events,omitempty"`
	Result *Result `json:"result,omitempty"`
}

type RunReply struct {
	Status string `json:"status"`
}
