package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func TestWorkflowUsesCommittedProjectVersion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is unavailable")
	}
	ctx := context.Background()
	r := New(Config{State: t.TempDir()})
	input := protocol.ProjectInput{ID: "game", Name: "Game", Engine: "godot"}
	if err := r.provisionProject(ctx, input); err != nil {
		t.Fatal(err)
	}
	project, ok := r.managedProject(input.ID)
	if !ok {
		t.Fatal("managed project is unavailable")
	}
	repo := project.Repository
	runGit := func(args ...string) string {
		t.Helper()
		value, err := git(ctx, repo, args...)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	start := runGit("rev-parse", "HEAD")
	committed := runGit("show", "HEAD:"+starterTest)
	write(starterTest, "staged change\n")
	runGit("add", "--", starterTest)
	write(starterTest, "unstaged change\n")
	write("local-only.txt", "untracked change\n")
	dirty := runGit("status", "--porcelain")
	r.engines["godot"] = godotEngine{}
	r.backends["codex"] = projectTestBackend{}
	job := protocol.Run{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExecutionID: "11111111111111111111111111111111",
		Spec: protocol.Spec{Project: input.ID, Engine: input.Engine, Executor: "codex", Title: "Development", Prompt: "Write design"},
	}
	execute := func() string {
		t.Helper()
		result, err := r.work(ctx, job, func(string, string) {})
		if err != nil {
			t.Fatal(err)
		}
		return result.Commit
	}
	first := execute()
	if got := runGit("rev-parse", first+"^"); got != start {
		t.Fatalf("first step started at %q, want %q", got, start)
	}
	if got := runGit("show", first+":"+starterTest); got != committed {
		t.Fatalf("worktree included a local change: %q", got)
	}
	if _, err := git(ctx, repo, "cat-file", "-e", first+":local-only.txt"); err == nil {
		t.Fatal("worktree included an untracked file")
	}
	if got := runGit("status", "--porcelain"); got != dirty {
		t.Fatalf("original Git status changed: %q, want %q", got, dirty)
	}
	if got := runGit("show", ":"+starterTest); got != "staged change" {
		t.Fatalf("original index changed: %q", got)
	}
	if data, err := os.ReadFile(filepath.Join(repo, starterTest)); err != nil || string(data) != "unstaged change\n" {
		t.Fatalf("original working file changed: %q, %v", data, err)
	}

	runGit("-c", "user.name=Agentrib", "-c", "user.email=agentrib@localhost", "-c", "commit.gpgsign=false",
		"commit", "-m", "Commit local change")
	latest := runGit("rev-parse", "HEAD")
	job.ID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if got := runGit("rev-parse", execute()+"^"); got != start {
		t.Fatalf("later step changed starting commit to %q", got)
	}
	job.ID = "cccccccccccccccccccccccccccccccc"
	job.Spec.Commit = first
	job.Spec.Prompt = "Continue from upstream"
	if got := runGit("rev-parse", execute()+"^"); got != first {
		t.Fatalf("explicit upstream commit was ignored: %q", got)
	}
	job.ID = "dddddddddddddddddddddddddddddddd"
	job.ExecutionID = "22222222222222222222222222222222"
	job.Spec.Commit = ""
	if got := runGit("rev-parse", execute()+"^"); got != latest {
		t.Fatalf("new workflow did not use current project HEAD: %q", got)
	}
}
