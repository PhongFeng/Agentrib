package runner

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func (r *Runner) finalAgentReply(ctx context.Context, backend Backend, dir, prompt string, emit Emit) (string, error) {
	var answer string
	var output strings.Builder
	log := func(kind, text string) {
		switch kind {
		case "result":
			answer = text
		case "agent":
			if output.Len() <= protocol.MaxBriefBytes {
				output.WriteString(text[:min(len(text), protocol.MaxBriefBytes+1)])
				output.WriteByte('\n')
			}
		default:
			emit(kind, text)
		}
	}
	if codex, ok := backend.(Codex); ok {
		tmp, err := os.MkdirTemp("", "agentrib-brief-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(tmp)
		path := filepath.Join(tmp, "reply.txt")
		if err := codex.execute(ctx, dir, prompt, true, log, "--output-last-message", path); err != nil {
			return "", err
		}
		file, err := os.Open(path)
		if err != nil {
			return "", errors.New("Codex did not return a final reply")
		}
		defer file.Close()
		body, err := io.ReadAll(io.LimitReader(file, protocol.MaxBriefBytes+1))
		if err != nil {
			return "", err
		}
		answer = string(body)
	} else {
		if err := backend.Execute(ctx, dir, prompt, true, log); err != nil {
			return "", err
		}
		if strings.TrimSpace(answer) == "" {
			answer = output.String()
		}
	}
	if len(answer) > protocol.MaxBriefBytes {
		return "", errors.New("Agent 回复过长")
	}
	if r.cfg.token != "" {
		answer = strings.ReplaceAll(answer, r.cfg.token, "[redacted]")
	}
	answer = strings.TrimSpace(strings.ReplaceAll(strings.ToValidUTF8(answer, "�"), "\x00", ""))
	if len(answer) > protocol.MaxBriefBytes {
		return "", errors.New("Agent 回复过长")
	}
	if answer == "" {
		return "", errors.New("Agent 没有返回内容")
	}
	return answer, nil
}
