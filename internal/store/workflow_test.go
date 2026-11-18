package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/jackc/pgx/v5"
)

func TestExecutionHistoryScopesProjectAndHidesArchives(t *testing.T) {
	ctx, db, _ := integrationStore(t)
	runner, err := db.Enroll(ctx, "History runner")
	if err != nil {
		t.Fatal(err)
	}
	machine := runner.Machine.ID
	if err := db.Heartbeat(ctx, machine, protocol.Heartbeat{Executors: []string{"codex"}, Projects: []string{"moon", "smoke"}}); err != nil {
		t.Fatal(err)
	}
	workflow, err := db.SaveWorkflow(ctx, protocol.Workflow{ID: "history", Name: "History", Steps: []protocol.Step{{ID: "produce", Executor: "codex"}}})
	if err != nil {
		t.Fatal(err)
	}
	start := func(id, project string) {
		t.Helper()
		_, err := db.StartExecution(ctx, protocol.ExecutionInput{ID: id, Project: project, WorkflowID: workflow.ID, Version: workflow.Version, MachineID: machine, Brief: "Make a game"})
		if err != nil {
			t.Fatal(err)
		}
	}
	archived, moon, smoke := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
	start(archived, "moon")
	if err := db.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(ctx, `UPDATE executions SET archived=true WHERE id=$1`, archived); err != nil {
		t.Fatal(err)
	}
	start(moon, "moon")
	start(smoke, "smoke")
	for project, id := range map[string]string{"moon": moon, "smoke": smoke} {
		list, err := db.Executions(ctx, project)
		if err != nil || len(list) != 1 || list[0].ID != id {
			t.Fatalf("history for %s: %+v %v", project, list, err)
		}
	}
	if list, err := db.Executions(ctx, "other"); err != nil || len(list) != 0 {
		t.Fatalf("history for unknown project: %+v %v", list, err)
	}
	if list, err := db.Executions(ctx, ""); err != nil || len(list) != 2 {
		t.Fatalf("unfiltered history includes archive: %+v %v", list, err)
	}
	if _, err := db.Execution(ctx, archived); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("archive returned as active history: %v", err)
	}
	if _, err := db.ContinueExecution(ctx, archived); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("archive was allowed to resume: %v", err)
	}
	if run, err := db.Claim(ctx, machine); err != nil || run != nil {
		t.Fatalf("archived run was claimable: %+v %v", run, err)
	}
}

func TestWorkflowGatesAndCommitHandoff(t *testing.T) {
	ctx, db, _ := integrationStore(t)
	enrollment, err := db.Enroll(ctx, "workflow machine")
	if err != nil {
		t.Fatal(err)
	}
	machine := enrollment.Machine.ID
	if err := db.Heartbeat(ctx, machine, protocol.Heartbeat{Executors: []string{"minimax-code", "codex", "godot", "qoder-cn"}, Projects: []string{"moon"}}); err != nil {
		t.Fatal(err)
	}
	w, err := db.SaveWorkflow(ctx, protocol.Workflow{ID: "moon", Name: "Moon", Steps: []protocol.Step{
		{ID: "brief", Name: "Brief", Executor: "approval"},
		{ID: "ui", Name: "UI", Executor: "minimax-code", DependsOn: []string{"brief"}},
		{ID: "build", Name: "Build", Executor: "codex", DependsOn: []string{"ui"}, BaseFrom: "ui"},
		{ID: "test", Name: "Test", Executor: "engine", DependsOn: []string{"build"}, BaseFrom: "build"},
		{ID: "review", Name: "Review", Executor: "qoder-cn", DependsOn: []string{"test"}, BaseFrom: "test", ReadOnly: true},
		{ID: "publish", Name: "Publish", Executor: "approval", DependsOn: []string{"review"}},
	}})
	if err != nil || w.Version != 1 || w.Engine != "godot" {
		t.Fatalf("save workflow: %+v %v", w, err)
	}
	id, _ := newID()
	input := protocol.ExecutionInput{ID: id, WorkflowID: w.ID, Version: w.Version, MachineID: machine, Project: "moon", Brief: "Build a Godot room"}
	planned := w
	planned.ID, planned.Version, planned.Engine = "future", 0, "unity"
	planned, err = db.SaveWorkflow(ctx, planned)
	if err != nil || planned.Engine != "unity" {
		t.Fatalf("save planned engine: %+v %v", planned, err)
	}
	blocked := input
	blocked.WorkflowID, blocked.Version = planned.ID, planned.Version
	if _, err := db.StartExecution(ctx, blocked); !errors.Is(err, ErrEngineUnavailable) {
		t.Fatalf("execution accepted with no engine adapter: %v", err)
	}
	blocked = input
	blocked.Project = "unregistered"
	if _, err := db.StartExecution(ctx, blocked); !errors.Is(err, ErrProjectEngine) {
		t.Fatalf("execution accepted without matching project: %v", err)
	}
	missing := w
	missing.ID, missing.Version = "missing-agent", 0
	missing.Steps = append([]protocol.Step(nil), w.Steps...)
	missing.Steps[1].Executor = "deepseek-harness"
	missing, err = db.SaveWorkflow(ctx, missing)
	if err != nil {
		t.Fatal(err)
	}
	blocked = input
	blocked.WorkflowID, blocked.Version = missing.ID, missing.Version
	if _, err := db.StartExecution(ctx, blocked); !errors.Is(err, ErrAgentUnavailable) {
		t.Fatalf("execution accepted without its selected Agent: %v", err)
	}
	if _, err := db.StartExecution(ctx, input); err != nil {
		t.Fatal(err)
	}
	w.Name = "Different version"
	if _, err := db.SaveWorkflow(ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := db.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if job, err := db.Claim(ctx, machine); err != nil || job != nil {
		t.Fatalf("job available before approval: %+v %v", job, err)
	}
	if err := db.Approve(ctx, id, "publish"); !errors.Is(err, ErrConflict) {
		t.Fatalf("publish before QA: %v", err)
	}
	if err := db.Approve(ctx, id, "brief"); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("e", 40)
	uiSHA := strings.Repeat("a", 40)
	for _, step := range []string{"ui", "build", "test", "review"} {
		if err := db.Schedule(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.Schedule(ctx); err != nil {
			t.Fatal(err)
		}
		job, err := db.Claim(ctx, machine)
		if err != nil || job == nil || job.StepID != step {
			t.Fatalf("claim %s: %+v %v", step, job, err)
		}
		if step == "test" && (job.Spec.Executor != "godot" || job.Spec.Engine != "godot") {
			t.Fatalf("test job did not resolve to the chosen engine: %+v", job.Spec)
		}
		wantBase := map[string]string{"ui": "", "build": uiSHA, "test": sha, "review": sha}
		if job.Spec.Commit != wantBase[step] {
			t.Fatalf("step %s commit: %s", step, job.Spec.Commit)
		}
		if step == "review" && (!strings.Contains(job.Spec.Prompt, "PASS 1/10") || strings.Contains(job.Spec.Prompt, "Export progress")) {
			t.Fatal("QA should receive test evidence without the later export output")
		}
		if err := db.Start(ctx, machine, job.ID, job.Lease); err != nil {
			t.Fatal(err)
		}
		report := protocol.Report{Lease: job.Lease, Result: &protocol.Result{Status: "succeeded", Detail: "complete", Commit: sha}}
		if step == "ui" {
			report.Result.Commit = uiSHA
		}
		if step == "test" {
			report.Events = []protocol.Event{
				{Seq: 1, Kind: "tool", Text: "godot --script res://test.gd"},
				{Seq: 2, Kind: "godot", Text: "PASS 1/10"},
				{Seq: 3, Kind: "tool", Text: "godot --export-release Web /tmp/index.html"},
			}
			for seq := 4; seq <= 30; seq++ {
				report.Events = append(report.Events, protocol.Event{Seq: seq, Kind: "godot", Text: "Export progress"})
			}
		}
		if _, err := db.Report(ctx, machine, job.ID, report); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Approve(ctx, id, "publish"); err != nil {
		t.Fatal(err)
	}
	if err := db.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	e, err := db.Execution(ctx, id)
	if err != nil || e.Status != "succeeded" || e.Workflow.Name != "Moon" || len(e.Runs) != 4 {
		t.Fatalf("finished execution: %+v %v", e, err)
	}
}
