package protocol

type Agent struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Commands []string `json:"commands"`
	ReadOnly bool     `json:"read_only"`
}

var Agents = []Agent{
	{ID: "claude-code", Name: "Claude Code", Commands: []string{"claude"}, ReadOnly: true},
	{ID: "codex", Name: "OpenAI Codex", Commands: []string{"codex"}, ReadOnly: true},
	{ID: "opencode", Name: "OpenCode", Commands: []string{"opencode"}},
	{ID: "openclaw", Name: "OpenClaw", Commands: []string{"openclaw"}},
	{ID: "hermes", Name: "Hermes", Commands: []string{"hermes"}},
	{ID: "pi", Name: "Pi", Commands: []string{"pi"}},
	{ID: "antigravity", Name: "Antigravity", Commands: []string{"agy"}},
	{ID: "codebuddy", Name: "CodeBuddy", Commands: []string{"codebuddy", "cbc"}},
	{ID: "grok", Name: "Grok", Commands: []string{"grok"}},
	{ID: "kimi", Name: "Kimi", Commands: []string{"kimi"}},
	{ID: "qoder", Name: "Qoder CLI", Commands: []string{"qodercli", "qoder"}},
	{ID: "qoder-cn", Name: "Qoder CN", Commands: []string{"qoderclicn", "qodercn"}, ReadOnly: true},
	{ID: "deepseek-harness", Name: "DeepSeek Harness", Commands: []string{"dsh"}, ReadOnly: true},
	{ID: "oh-my-pi", Name: "Oh-My-Pi", Commands: []string{"omp"}},
	{ID: "minimax-code", Name: "MiniMax Code", Commands: []string{"mcode"}},
	{ID: "zcode", Name: "ZCode", Commands: []string{"zcode"}},
}

func FindAgent(id string) (Agent, bool) {
	for _, agent := range Agents {
		if agent.ID == id {
			return agent, true
		}
	}
	return Agent{}, false
}
