package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func (r *Runner) finalAgentReply(ctx context.Context, backend Backend, dir, prompt string, readOnly bool, replyLimit int, emit Emit) (string, error) {
	var answer string
	var lastMessage string
	var output strings.Builder
	log := func(kind, text string) {
		switch kind {
		case "result":
			answer = text
		case "agent":
			lastMessage = text
			if readOnly && output.Len() <= replyLimit {
				if output.Len() > 0 {
					output.WriteByte('\n')
				}
				output.WriteString(text[:min(len(text), replyLimit+1-output.Len())])
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
		if err := codex.execute(ctx, dir, prompt, readOnly, log, "--output-last-message", path); err != nil {
			return "", err
		}
		file, err := os.Open(path)
		if err != nil {
			return "", errors.New("Codex did not return a final reply")
		}
		defer file.Close()
		body, err := io.ReadAll(io.LimitReader(file, int64(replyLimit)+1))
		if err != nil {
			return "", err
		}
		answer = string(body)
	} else {
		if err := backend.Execute(ctx, dir, prompt, readOnly, log); err != nil {
			return "", err
		}
		if strings.TrimSpace(answer) == "" {
			if readOnly {
				answer = output.String()
			} else {
				answer = lastMessage
			}
		}
	}
	if len(answer) > replyLimit {
		return "", fmt.Errorf("Agent 回复过长（上限 %d 字节）", replyLimit)
	}
	if r.cfg.token != "" {
		answer = strings.ReplaceAll(answer, r.cfg.token, "[redacted]")
	}
	answer = strings.TrimSpace(strings.ReplaceAll(strings.ToValidUTF8(answer, "�"), "\x00", ""))
	if len(answer) > replyLimit {
		return "", fmt.Errorf("Agent 回复过长（上限 %d 字节）", replyLimit)
	}
	if answer == "" {
		return "", errors.New("Agent 没有返回内容")
	}
	return answer, nil
}
