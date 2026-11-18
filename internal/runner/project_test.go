package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

type projectTestBackend struct{}

func (projectTestBackend) Probe(context.Context) error { return nil }

func (projectTestBackend) Execute(_ context.Context, dir, contents string, _ bool, _ Emit) error {
	return os.WriteFile(filepath.Join(dir, "design.md"), []byte(contents), 0600)
}

func TestManagedProjectWorkspaces(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is unavailable")
	}
	ctx := context.Background()
	cfg := Config{State: t.TempDir()}
	first := protocol.ProjectInput{ID: "first-game", Name: "第一款游戏", Engine: "godot"}
	second := protocol.ProjectInput{ID: "second-game", Name: "Second game", Engine: "godot"}
	r := New(cfg)
	for _, input := range []protocol.ProjectInput{first, second, first} {
		if err := r.provisionProject(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	// After restart the Runner rediscovers both repositories without a config edit.
	r = New(cfg)
	var h protocol.Heartbeat
	r.updateProjects(&h)
	if len(h.Projects) != 2 || h.ProjectWorkspaces[first.ID] == h.ProjectWorkspaces[second.ID] {
		t.Fatalf("wrong project inventory: %+v", h)
	}
	r.engines["godot"] = godotEngine{}
	r.backends["codex"] = projectTestBackend{}
	job := protocol.Run{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Spec: protocol.Spec{
		Project: first.ID, Engine: first.Engine, Executor: "codex", Title: "Design", Prompt: "first revision",
	}}
	emit := func(string, string) {}
	result, err := r.work(ctx, job, emit)
	if err != nil || !objectID(result.Commit) {
		t.Fatalf("could not execute in managed worktree: %+v, %v", result, err)
	}
	job.ID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	job.Spec.Project = second.ID
	job.Spec.Commit = result.Commit
	if _, err := r.work(ctx, job, emit); err == nil {
		t.Fatal("a commit from another project was accepted")
	}
	job.Spec.Project = first.ID
	job.Spec.Prompt = "second revision"
	next, err := r.work(ctx, job, emit)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := git(ctx, h.ProjectWorkspaces[first.ID], "rev-parse", next.Commit+"^")
	if err != nil || parent != result.Commit {
		t.Fatalf("maintenance did not start at the previous commit: %q, %v", parent, err)
	}
}
