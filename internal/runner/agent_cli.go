package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

type cliBackend struct {
	agent protocol.Agent
	path  string
}

func backendFor(agent protocol.Agent, path string) Backend {
	switch agent.ID {
	case "codex":
		return Codex{command: path}
	case "qoder-cn":
		return QoderCN{command: path}
	case "minimax-code":
		return MiniMaxCode{path}
	default:
		return cliBackend{agent, path}
	}
}

func (c cliBackend) Probe(ctx context.Context) error {
	if c.agent.ID == "openclaw" || c.agent.ID == "deepseek-harness" {
		args, capability := []string{"agent", "exec", "--help"}, "--message-file"
		if c.agent.ID == "deepseek-harness" {
			args, capability = []string{"--profile", "headless", "--help"}, "task"
		}
		out, err := command(ctx, "", c.path, args...).Output()
		if err != nil {
			return err
		}
		if !bytes.Contains(out, []byte(capability)) {
			return fmt.Errorf("%s CLI lacks the required headless command", c.agent.Name)
		}
		return nil
	}
	return command(ctx, "", c.path, "--version").Run()
}

func (c cliBackend) Execute(ctx context.Context, dir, prompt string, readOnly bool, emit Emit) error {
	if readOnly && !c.agent.ReadOnly {
		return fmt.Errorf("%s does not support read-only Agentrib steps", c.agent.Name)
	}
	var args []string
	var input string
	var claudeEvents bool
	switch c.agent.ID {
	case "claude-code":
		args = []string{"-p", "--verbose", "--output-format", "stream-json", "--no-session-persistence"}
		if readOnly {
			args = append(args, "--tools", "Read,Glob,Grep", "--permission-mode", "dontAsk",
				"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`)
		} else {
			args = append(args, "--permission-mode", "acceptEdits")
		}
		input, claudeEvents = prompt, true
	case "opencode":
		args = []string{"run", "--standalone", "--auto", "--", prompt}
	case "openclaw":
		args, input = []string{"agent", "exec", "--cwd", dir, "--message-file", "-", "--timeout", "0"}, prompt
	case "hermes":
		args = []string{"chat", "--oneshot", "-q", prompt}
	case "pi", "oh-my-pi":
		args, input = []string{"-p", "--no-session"}, prompt
	case "antigravity":
		args = []string{"--mode=accept-edits", "-p", prompt}
	case "codebuddy":
		args = []string{"-p", "--verbose", "--output-format", "stream-json", "--permission-mode", "acceptEdits"}
		input, claudeEvents = prompt, true
	case "grok":
		args = []string{"--no-auto-update", "--always-approve", "-p", prompt}
	case "kimi":
		args, input = []string{"--quiet"}, prompt
	case "qoder":
		args = []string{"-p", "--output-format", "stream-json", "--permission-mode", "accept_edits"}
		input, claudeEvents = prompt, true
	case "deepseek-harness":
		mode := "workspace-write"
		if readOnly {
			mode = "read-only"
		}
		args, input = []string{"--profile", "headless", "--permission-mode", mode, "--approval-policy", "never", "-"}, prompt
	case "zcode":
		args = []string{"--prompt", prompt, "--cwd", dir, "--mode", "yolo", "--no-color"}
	default:
		return fmt.Errorf("no CLI adapter for %s", c.agent.ID)
	}
	cmd := command(ctx, dir, c.path, args...)
	if claudeEvents {
		var completed bool
		var resultErr error
		err := stream(cmd, input, func(line string) {
			var event struct{ Type string }
			if json.Unmarshal([]byte(line), &event) == nil && event.Type == "result" {
				completed = true
			}
			if err := parseQoder(line, emit); err != nil {
				resultErr = err
			}
		}, emit)
		if err == nil && !completed {
			return fmt.Errorf("%s did not return a terminal result", c.agent.Name)
		}
		return errors.Join(err, resultErr)
	}

	var output bool
	err := stream(cmd, input, func(line string) {
		if strings.TrimSpace(line) != "" {
			output = true
			emit("agent", line)
		}
	}, emit)
	if err == nil && !output {
		return fmt.Errorf("%s exited without a response", c.agent.Name)
	}
	return err
}
