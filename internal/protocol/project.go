package protocol

const MaxProjects = 64

type ProjectInput struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Engine string `json:"engine"`
}

type GameProject struct {
	ProjectInput
	Workspace string `json:"workspace,omitempty"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

type ProjectRequest struct {
	ProjectInput
	Attempt string `json:"attempt"`
}

type ProjectResult struct {
	Attempt string `json:"attempt"`
	Error   string `json:"error,omitempty"`
}

type HeartbeatReply struct {
	Status  string          `json:"status"`
	Project *ProjectRequest `json:"project,omitempty"`
}
