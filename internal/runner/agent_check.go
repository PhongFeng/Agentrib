package runner

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

const (
	agentCheckDuration = 45 * time.Second
	agentCheckReply    = "AGENTRIB_CHECK_OK"
)

func (r *Runner) checkAgent(ctx context.Context, executor string) error {
	agent, known := protocol.FindAgent(executor)
	backend := r.backends[executor]
	if !known || backend == nil {
		return errors.New("Agent CLI 尚未就绪")
	}
	dir, err := os.MkdirTemp("", "agentrib-agent-check-")
	if err != nil {
		return errors.New("无法建立测试工作区")
	}
	defer os.RemoveAll(dir)
	if _, ok := backend.(Codex); ok {
		if _, err := git(ctx, dir, "init", "-q"); err != nil {
			return errors.New("无法初始化测试工作区")
		}
	}
	var mu sync.Mutex
	var matched bool
	var diagnosis string
	emit := func(kind, text string) {
		mu.Lock()
		defer mu.Unlock()
		switch kind {
		case "agent", "result":
			for _, line := range strings.Split(text, "\n") {
				matched = matched || strings.Trim(strings.TrimSpace(line), "`") == agentCheckReply
			}
		case "error", "diagnostic":
			if known := agentCheckDiagnosis(text); known != "" {
				diagnosis = known
			}
		}
	}
	err = backend.Execute(ctx, dir, "Availability test. Reply with exactly "+agentCheckReply+". Do not use tools or access files.", agent.ReadOnly, emit)
	if err == nil && matched {
		return nil
	}
	// Keep provider output local. It can contain credentials or account data.
	if err != nil && diagnosis == "" {
		diagnosis = agentCheckDiagnosis(err.Error())
	}
	if diagnosis != "" {
		return errors.New(diagnosis)
	}
	if err != nil {
		return errors.New("Agent 调用失败")
	}
	return errors.New("Agent 没有返回预期的测试响应")
}

func agentCheckDiagnosis(message string) string {
	text := strings.ToLower(message)
	for _, group := range []struct {
		reason string
		hints  []string
	}{
		{"需要登录或验证凭据", []string{"unauthorized", "invalid api key", "not logged in", "login required", "登录失效", "未登录"}},
		{"额度不足或请求受限", []string{"insufficient", "quota exceeded", "rate limit", "too many requests", "额度不足", "余额不足"}},
		{"无法连接模型服务", []string{"connection refused", "timed out", "network error", "failed to connect", "service unavailable"}},
	} {
		for _, hint := range group.hints {
			if strings.Contains(text, hint) {
				return group.reason
			}
		}
	}
	return ""
}
