package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

type qaTestBackend string

func (qaTestBackend) Probe(context.Context) error { return nil }

func (answer qaTestBackend) Execute(_ context.Context, _ string, _ string, _ bool, emit Emit) error {
	emit("agent", string(answer))
	return nil
}

func TestProductionWorktreeIncludesBothInputsAndChecksQA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is unavailable")
	}
	ctx := context.Background()
	r := New(Config{State: t.TempDir()})
	if err := r.provisionProject(ctx, protocol.ProjectInput{ID: "game", Name: "Game", Engine: "godot"}); err != nil {
		t.Fatal(err)
	}
	project, _ := r.managedProject("game")
	repo := project.Repository
	runGit := func(args ...string) string {
		t.Helper()
		output, err := git(ctx, repo, args...)
		if err != nil {
			t.Fatal(err)
		}
		return output
	}
	root := runGit("rev-parse", "HEAD")
	asset := func(name string) string {
		t.Helper()
		runGit("switch", "--detach", root)
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		runGit("add", "--", name)
		runGit("-c", "user.name=Agentrib", "-c", "user.email=agentrib@localhost", "-c", "commit.gpgsign=false", "commit", "-m", name)
		return runGit("rev-parse", "HEAD")
	}
	art := asset("art.txt")
	audio := asset("audio.txt")
	r.engines["godot"] = godotEngine{}
	r.backends["codex"] = projectTestBackend{}
	job := protocol.Run{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Spec: protocol.Spec{
		Project: "game", Engine: "godot", Executor: "codex", Commit: art, MergeCommits: []string{audio},
		Title: "Build", Prompt: "Integrate the assets",
	}}
	emit := func(string, string) {}
	result, err := r.work(ctx, job, emit)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"art.txt", "audio.txt"} {
		if got := runGit("show", result.Commit+":"+file); got != file {
			t.Fatalf("%s missing from integrated commit: %q", file, got)
		}
	}
	if got := runGit("rev-parse", "HEAD"); got != audio {
		t.Fatalf("source workspace moved from %s to %s", audio, got)
	}
	job.Spec.Commit, job.Spec.MergeCommits, job.Spec.ReadOnly, job.Spec.QAGate = result.Commit, nil, true, true
	r.backends["codex"] = qaTestBackend("QA: FAIL\nMissing evidence")
	if _, err := r.work(ctx, job, emit); err == nil {
		t.Fatal("failing QA verdict accepted")
	}
	r.backends["codex"] = qaTestBackend("QA: PASS\nVerified")
	if _, err := r.work(ctx, job, emit); err != nil {
		t.Fatalf("passing QA verdict rejected: %v", err)
	}

	job.Spec.QARework, job.Spec.ReadOnly = true, false
	evidence := strings.Repeat("证据", 140)
	var report struct {
		Bugs []protocol.Bug `json:"bugs"`
	}
	for i := 0; i < 8; i++ {
		report.Bugs = append(report.Bugs, protocol.Bug{Owner: "develop", Title: fmt.Sprintf("缺陷 %d", i+1),
			Steps: evidence, Expected: evidence, Actual: evidence})
	}
	body, err := json.Marshal(report)
	if err != nil || len(body) <= protocol.MaxBriefBytes {
		t.Fatalf("invalid oversized QA fixture: %d bytes, %v", len(body), err)
	}
	r.backends["codex"] = qaTestBackend("QA: FAIL\n" + string(body))
	result, err = r.work(ctx, job, emit)
	if !errors.Is(err, errQARejected) || !reflect.DeepEqual(result.Bugs, report.Bugs) {
		t.Fatalf("expected all 8 intact defects, got %d and %v", len(result.Bugs), err)
	}
}
