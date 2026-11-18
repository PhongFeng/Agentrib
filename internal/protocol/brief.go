package protocol

const MaxBriefBytes = 8000

type BriefMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type BriefTurn struct {
	ID        string         `json:"id"`
	MachineID string         `json:"machine_id"`
	Project   string         `json:"project"`
	Executor  string         `json:"executor"`
	Mode      string         `json:"mode"`
	Skills    []string       `json:"skills,omitempty"`
	Messages  []BriefMessage `json:"messages"`
}
