package protocol

import "time"

type Machine struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Executors      []string          `json:"executors"`
	Projects       []string          `json:"projects"`
	ProjectEngines map[string]string `json:"project_engines,omitempty"`
	Seen           time.Time         `json:"seen"`
}

type Enrollment struct {
	Machine Machine `json:"machine"`
	Token   string  `json:"token"`
}

type Heartbeat struct {
	Executors      []string          `json:"executors"`
	Projects       []string          `json:"projects"`
	ProjectEngines map[string]string `json:"project_engines,omitempty"`
}

type Spec struct {
	MachineID string `json:"machine_id"`
	Project   string `json:"project"`
	Engine    string `json:"engine,omitempty"`
	Executor  string `json:"executor"`
	Title     string `json:"title"`
	Prompt    string `json:"prompt"`
	Commit    string `json:"commit,omitempty"`
	ReadOnly  bool   `json:"read_only,omitempty"`
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
}

type Report struct {
	Lease  string  `json:"lease"`
	Events []Event `json:"events,omitempty"`
	Result *Result `json:"result,omitempty"`
}

type RunReply struct {
	Status string `json:"status"`
}
