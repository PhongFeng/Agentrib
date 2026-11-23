package runner

import (
	"context"
	"path/filepath"
	"time"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

const blenderMCPName = "agentrib_blender"

func (s mcpServers) prompt() string {
	var prompt string
	if _, ok := s[godotMCPName]; ok {
		prompt += godotMCPPrompt
	}
	if _, ok := s[blenderMCPName]; ok {
		prompt += "\nFor 3D game assets, use agentrib_blender.create_model to export a GLB under assets/models in this worktree. Reference the exported asset from the game when appropriate."
	}
	return prompt
}

func (r *Runner) probeBlender(ctx context.Context) protocol.ToolStatus {
	tool := r.cfg.Commands["blender-mcp"]
	if tool == "" {
		return protocol.ToolStatus{Status: "unconfigured"}
	}
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ready := filepath.IsAbs(tool) && command(check, "", tool, "--check").Run() == nil
	r.blenderReady.Store(ready)
	if !ready {
		return protocol.ToolStatus{Status: "unavailable"}
	}
	status := protocol.ToolStatus{Status: "ready"}
	for _, key := range protocol.WorkspaceMCPAgents {
		if r.backends[key] != nil {
			status.Agents = append(status.Agents, key)
		}
	}
	return status
}

func (r *Runner) pollBlender(ctx context.Context, statuses chan<- protocol.ToolStatus) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status := r.probeBlender(ctx)
			select {
			case statuses <- status:
			case <-ctx.Done():
				return
			}
		}
	}
}
