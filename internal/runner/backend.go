package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

type Emit func(kind, text string)

type Backend interface {
	Probe(context.Context) error
	Execute(context.Context, string, string, bool, Emit) error
}

type Codex struct{ command string }
type QoderCN struct{ command string }
type MiniMaxCode struct{ command string }

func (c Codex) Probe(ctx context.Context) error {
	return command(ctx, "", c.command, "login", "status").Run()
}
func (q QoderCN) Probe(ctx context.Context) error {
	return command(ctx, "", q.command, "--version").Run()
}

func (m MiniMaxCode) Probe(ctx context.Context) error {
	return command(ctx, "", m.command, "--version").Run()
}

func (c Codex) Execute(ctx context.Context, directory, prompt string, readOnly bool, emit Emit) error {
	mode := "workspace-write"
	if readOnly {
		mode = "read-only"
	}
	cmd := command(ctx, directory, c.command, "exec", "--json", "--ephemeral", "--color", "never",
		"-c", `approval_policy="never"`, "--sandbox", mode, "-")
	return stream(cmd, prompt, func(line string) { parseCodex(line, emit) }, emit)
}

func (q QoderCN) Execute(ctx context.Context, directory, prompt string, readOnly bool, emit Emit) error {
	args := []string{"-p", "--output-format", "stream-json"}
	if readOnly {
		args = append(args, "--tools=Read,Glob,Grep", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`)
	} else {
		args = append(args, "--permission-mode", "accept_edits")
	}
	cmd := command(ctx, directory, q.command, args...)
	var resultErr error
	err := stream(cmd, prompt, func(line string) {
		if err := parseQoder(line, emit); err != nil {
			resultErr = err
		}
	}, emit)
	return errors.Join(err, resultErr)
}

func (m MiniMaxCode) Execute(ctx context.Context, directory, prompt string, readOnly bool, emit Emit) error {
	if readOnly {
		return errors.New("MiniMax Code does not support read-only Agentrib steps")
	}
	input, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		return err
	}
	cmd := command(ctx, directory, m.command, "exec", "--permission", "smart",
		"--input", "-", "--input-format", "json", "--output-format", "stream-json")
	var status string
	err = stream(cmd, string(input), func(line string) {
		if completed := parseMiniMax(line, emit); completed != "" {
			status = completed
		}
	}, emit)
	if status == "" && err == nil {
		return errors.New("MiniMax Code exited without a terminal result")
	}
	if status != "" && status != "succeeded" {
		return errors.Join(err, fmt.Errorf("MiniMax Code reported %s", status))
	}
	return err
}

func parseMiniMax(line string, emit Emit) string {
	var event struct {
		Type string
		Item struct {
			Type, Content string
			ToolCall      struct{ Name string }
		}
		Result struct {
			Status string
			Error  struct{ Message string }
		}
	}
	if json.Unmarshal([]byte(line), &event) != nil {
		return ""
	}
	switch event.Type {
	case "item.completed":
		switch event.Item.Type {
		case "agent_message":
			emit("agent", event.Item.Content)
		case "tool_call":
			emit("tool", event.Item.ToolCall.Name)
		}
	case "exec.completed":
		if event.Result.Error.Message != "" {
			emit("error", event.Result.Error.Message)
		}
		return event.Result.Status
	}
	return ""
}

func command(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 4 * time.Second
	cmd.Env = slices.DeleteFunc(cmd.Environ(), func(v string) bool {
		return strings.HasPrefix(strings.ToUpper(v), "AGENTRIB_")
	})
	isolate(cmd)
	return cmd
}

func stream(cmd *exec.Cmd, prompt string, stdout func(string), emit Emit) error {
	out := &lineWriter{writeLine: stdout}
	stderr := &lineWriter{writeLine: func(line string) { emit("diagnostic", line) }}
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout, cmd.Stderr = out, stderr
	err := cmd.Run()
	out.flush()
	stderr.flush()
	return err
}

type lineWriter struct {
	buffer    []byte
	truncated bool
	writeLine func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	size := len(p)
	for len(p) > 0 {
		line, remain, newline := bytes.Cut(p, []byte{'\n'})
		available := (2 << 20) - len(w.buffer)
		w.buffer = append(w.buffer, line[:min(available, len(line))]...)
		w.truncated = w.truncated || len(line) > available
		if newline {
			w.flush()
		}
		p = remain
	}
	return size, nil
}

func (w *lineWriter) flush() {
	if len(w.buffer) != 0 && !w.truncated {
		w.writeLine(strings.TrimSuffix(string(w.buffer), "\r"))
	}
	w.buffer = w.buffer[:0]
	w.truncated = false
}

func parseCodex(line string, emit Emit) {
	var data struct {
		Type    string          `json:"type"`
		Message string          `json:"message"`
		Item    json.RawMessage `json:"item"`
		Error   struct{ Message string }
	}
	if json.Unmarshal([]byte(line), &data) != nil {
		return
	}
	switch data.Type {
	case "error":
		emit("error", data.Message)
	case "turn.failed":
		emit("error", data.Error.Message)
	case "item.completed":
		var item struct {
			Type, Text, Command string
			ExitCode            *int `json:"exit_code"`
			Changes             []struct{ Kind, Path string }
		}
		if json.Unmarshal(data.Item, &item) != nil {
			return
		}
		switch item.Type {
		case "agent_message":
			emit("agent", item.Text)
		case "command_execution":
			emit("tool", item.Command)
			if item.ExitCode != nil {
				emit("tool", fmt.Sprintf("Exit code %d", *item.ExitCode))
			}
		case "file_change":
			for _, change := range item.Changes {
				emit("tool", change.Kind+" "+change.Path)
			}
		}
	}
}

func parseQoder(line string, emit Emit) error {
	var data struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
		Message struct {
			Content []struct{ Type, Text, Name string }
		}
	}
	if json.Unmarshal([]byte(line), &data) != nil {
		return nil
	}
	if data.Type == "assistant" {
		for _, item := range data.Message.Content {
			if item.Type == "text" {
				emit("agent", item.Text)
			} else if item.Type == "tool_use" {
				emit("tool", item.Name)
			}
		}
	}
	if data.Type == "result" {
		if data.IsError || strings.HasPrefix(data.Subtype, "error") {
			emit("error", data.Result)
			return errors.New("Qoder CN reported a failed result")
		}
		emit("result", data.Result)
	}
	return nil
}

func findCommand(explicit string, names ...string) (string, error) {
	if explicit != "" {
		return exec.LookPath(explicit)
	}
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

var _ io.Writer = (*lineWriter)(nil)
