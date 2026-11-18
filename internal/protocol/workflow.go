package protocol

import "time"

type Step struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Role       string   `json:"role"`
	Executor   string   `json:"executor"`
	Prompt     string   `json:"prompt,omitempty"`
	ReadOnly   bool     `json:"read_only,omitempty"`
	DependsOn  []string `json:"depends_on"`
	BaseFrom   string   `json:"base_from,omitempty"`
	MergeFrom  []string `json:"merge_from,omitempty"`
	QAGate     bool     `json:"qa_gate,omitempty"`
	ExportGame bool     `json:"export_game,omitempty"`
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

type Execution struct {
	ExecutionInput
	Status   string    `json:"status"`
	Created  time.Time `json:"created"`
	Workflow Workflow  `json:"workflow"`
	Runs     []Run     `json:"runs"`
	Approved []string  `json:"approved"`
}
