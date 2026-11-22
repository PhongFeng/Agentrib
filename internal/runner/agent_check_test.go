package runner

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

type checkBackend func(context.Context, string, string, bool, Emit) error

func (checkBackend) Probe(context.Context) error { return nil }
func (f checkBackend) Execute(ctx context.Context, dir, prompt string, readOnly bool, emit Emit) error {
	return f(ctx, dir, prompt, readOnly, emit)
}

func TestCheckAgentNeedsRealReplyAndHidesProviderOutput(t *testing.T) {
	r := New(Config{State: t.TempDir()})
	const executor = "minimax-code"
	spec := protocol.Run{Spec: protocol.Spec{AgentCheck: true, Executor: executor, Prompt: "untrusted job prompt"}}
	r.backends[executor] = checkBackend(func(_ context.Context, dir, prompt string, _ bool, emit Emit) error {
		files, err := os.ReadDir(dir)
		if err != nil || len(files) != 0 || strings.Contains(prompt, spec.Spec.Prompt) {
			t.Fatalf("check should use an empty directory and fixed prompt: %v %v", files, err)
		}
		emit("agent", agentCheckReply)
		return nil
	})
	if _, err := r.work(context.Background(), spec, discardCheckLog); err != nil {
		t.Fatal(err)
	}
	r.backends[executor] = checkBackend(func(_ context.Context, _, _ string, _ bool, emit Emit) error {
		emit("diagnostic", "unauthorized: example-api-key-should-stay-local")
		return errors.New("exit status 1")
	})
	if _, err := r.work(context.Background(), spec, discardCheckLog); err == nil || err.Error() != "需要登录或验证凭据" {
		t.Fatalf("expected a sanitized authentication error, got %v", err)
	}
	r.backends[executor] = checkBackend(func(_ context.Context, _, _ string, _ bool, emit Emit) error {
		emit("diagnostic", agentCheckReply)
		return nil
	})
	if _, err := r.work(context.Background(), spec, discardCheckLog); err == nil {
		t.Fatal("diagnostics alone must not pass the check")
	}
}

func discardCheckLog(string, string) {}
