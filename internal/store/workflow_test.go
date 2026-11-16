package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func TestWorkflowGatesAndCommitHandoff(t *testing.T) {
	ctx, db, _ := integrationStore(t)
	enrollment, err := db.Enroll(ctx, "workflow machine")
	if err != nil {
		t.Fatal(err)
	}
	machine := enrollment.Machine.ID
	if err := db.Heartbeat(ctx, machine, protocol.Heartbeat{Executors: []string{"codex", "godot", "qoder-cn"}, Projects: []string{"moon"}}); err != nil {
		t.Fatal(err)
	}
	w, err := db.SaveWorkflow(ctx, protocol.Workflow{ID: "moon", Name: "Moon", Steps: []protocol.Step{
		{ID: "brief", Name: "Brief", Executor: "approval"},
		{ID: "build", Name: "Build", Executor: "codex", DependsOn: []string{"brief"}},
		{ID: "test", Name: "Test", Executor: "godot", DependsOn: []string{"build"}, BaseFrom: "build"},
		{ID: "review", Name: "Review", Executor: "qoder-cn", DependsOn: []string{"test"}, BaseFrom: "test", ReadOnly: true},
		{ID: "publish", Name: "Publish", Executor: "approval", DependsOn: []string{"review"}},
	}})
	if err != nil || w.Version != 1 {
		t.Fatalf("save workflow: %+v %v", w, err)
	}
	id, _ := newID()
	input := protocol.ExecutionInput{ID: id, WorkflowID: w.ID, Version: w.Version, MachineID: machine, Project: "moon", Brief: "Build a Godot room"}
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
	for _, step := range []string{"build", "test", "review"} {
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
		if step != "build" && job.Spec.Commit != sha {
			t.Fatalf("step %s commit: %s", step, job.Spec.Commit)
		}
		if step == "review" && !strings.Contains(job.Spec.Prompt, "PASS 1/10") {
			t.Fatal("Godot evidence was not handed to QA")
		}
		if err := db.Start(ctx, machine, job.ID, job.Lease); err != nil {
			t.Fatal(err)
		}
		report := protocol.Report{Lease: job.Lease, Result: &protocol.Result{Status: "succeeded", Detail: "complete", Commit: sha}}
		if step == "test" {
			report.Events = []protocol.Event{{Seq: 1, Kind: "godot", Text: "PASS 1/10"}}
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
	if err != nil || e.Status != "succeeded" || e.Workflow.Name != "Moon" || len(e.Runs) != 3 {
		t.Fatalf("finished execution: %+v %v", e, err)
	}
}
