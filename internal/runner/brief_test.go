package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

type briefTestBackend struct{ write bool }

func (briefTestBackend) Probe(context.Context) error { return nil }

func (b briefTestBackend) Execute(_ context.Context, dir, prompt string, readOnly bool, emit Emit) error {
	if !readOnly || !strings.Contains(prompt, "核心玩法") {
		return errors.New("brief did not receive a read-only product prompt")
	}
	if b.write {
		return os.WriteFile(filepath.Join(dir, "unwanted.txt"), []byte("unexpected"), 0600)
	}
	emit("agent", "分析过程")
	emit("result", "游戏简报正文")
	return nil
}

func TestProductAgentTurnUsesReadOnlyProject(t *testing.T) {
	ctx := context.Background()
	r := New(Config{State: t.TempDir()})
	project := protocol.ProjectInput{ID: "game", Name: "Game", Engine: "godot"}
	if err := r.provisionProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	r.engines["godot"] = godotEngine{}
	run := protocol.Run{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Spec: protocol.Spec{
		Project: project.ID, Engine: project.Engine, Executor: "qoder-cn", Prompt: "核心玩法", ReadOnly: true, BriefMode: "draft",
	}}
	r.backends["qoder-cn"] = briefTestBackend{}
	emit := func(string, string) {}
	result, err := r.work(ctx, run, emit)
	if err != nil || result.Reply != "游戏简报正文" || result.Commit != "" {
		t.Fatalf("unexpected product reply %+v: %v", result, err)
	}
	r.backends["qoder-cn"] = briefTestBackend{write: true}
	if _, err := r.work(ctx, run, emit); err == nil {
		t.Fatal("product Agent was allowed to change its worktree")
	}
}
