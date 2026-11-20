package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/jackc/pgx/v5"
)

func TestContinueExecutionPreservesSuccessfulInputs(t *testing.T) {
	ctx, db, url := integrationStore(t)
	enrolled, err := db.Enroll(ctx, "Resume runner")
	if err != nil {
		t.Fatal(err)
	}
	machine := enrolled.Machine.ID
	if err := db.Heartbeat(ctx, machine, protocol.Heartbeat{Executors: []string{"codex", "godot"}, Projects: []string{"moon"}, ProductionWorkflow: true}); err != nil {
		t.Fatal(err)
	}
	w, err := db.SaveWorkflow(ctx, protocol.Workflow{ID: "resume", Name: "Game production", Steps: []protocol.Step{
		{ID: "art", Executor: "codex"},
		{ID: "audio", Executor: "codex"},
		{ID: "develop", Executor: "codex", DependsOn: []string{"art", "audio"}, BaseFrom: "art", MergeFrom: []string{"audio"}},
		{ID: "ci", Executor: "engine", DependsOn: []string{"develop"}, BaseFrom: "develop", ExportGame: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	eid := strings.Repeat("1", 32)
	if _, err := db.StartExecution(ctx, protocol.ExecutionInput{ID: eid, WorkflowID: w.ID, Version: w.Version, MachineID: machine, Project: "moon", Brief: "Make a game"}); err != nil {
		t.Fatal(err)
	}
	claim := func(step string) *protocol.Run {
		t.Helper()
		if err := db.Schedule(ctx); err != nil {
			t.Fatal(err)
		}
		run, err := db.Claim(ctx, machine)
		if err != nil || run == nil || (step != "" && run.StepID != step) {
			t.Fatalf("claim %s: %+v %v", step, run, err)
		}
		if err := db.Start(ctx, machine, run.ID, run.Lease); err != nil {
			t.Fatal(err)
		}
		return run
	}
	finish := func(run *protocol.Run, result protocol.Result, events ...protocol.Event) {
		t.Helper()
		if _, err := db.Report(ctx, machine, run.ID, protocol.Report{Lease: run.Lease, Result: &result, Events: events}); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		run := claim("")
		revision := map[string]string{"art": "a", "audio": "b"}[run.StepID]
		finish(run, protocol.Result{Status: "succeeded", Detail: "complete", Commit: strings.Repeat(revision, 40)})
	}
	failed := claim("develop")
	timeout := protocol.Result{Status: "failed", Detail: "Execution timed out"}
	finish(failed, timeout, protocol.Event{Seq: 1, Kind: "error", Text: timeout.Detail})
	if err := db.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	resumed, err := db.ContinueExecution(ctx, eid)
	if err != nil || resumed.Status != "running" || resumed.ID != eid || len(resumed.Runs) != 4 {
		t.Fatalf("resume didn't keep the execution ID and history: %+v %v", resumed, err)
	}
	if _, err := db.ContinueExecution(ctx, eid); !errors.Is(err, ErrConflict) {
		t.Fatalf("second resume should not create a duplicate: %v", err)
	}
	again := claim("develop")
	if again.ID == failed.ID || again.Attempt != 2 || !reflect.DeepEqual(again.Spec, failed.Spec) {
		t.Fatalf("retry didn't use a fresh run and the same inputs: %+v", again)
	}
	commit := strings.Repeat("c", 40)
	finish(again, protocol.Result{Status: "succeeded", Detail: "complete", Commit: commit})
	ci := claim("ci")
	if ci.Spec.Commit != commit {
		t.Fatalf("CI didn't get the successful retry's Git commit: %+v", ci.Spec)
	}
	finish(ci, protocol.Result{Status: "succeeded", Detail: "complete", Commit: commit})
	if err := db.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	final, err := reopened.Execution(ctx, eid)
	if err != nil || final.Status != "succeeded" || len(final.Runs) != 5 {
		t.Fatalf("retry did not lead to a completed execution: %+v %v", final, err)
	}
	old, err := reopened.Events(ctx, failed.ID, 0)
	if err != nil || len(old) != 1 || old[0].Text != timeout.Detail {
		t.Fatalf("failed attempt's log was lost: %+v %v", old, err)
	}
}

func TestProductionWorkflowJoinsInputsAndNotifies(t *testing.T) {
	ctx, db, _ := integrationStore(t)
	enrolled, err := db.Enroll(ctx, "Production runner")
	if err != nil {
		t.Fatal(err)
	}
	machine := enrolled.Machine.ID
	if err := db.Heartbeat(ctx, machine, protocol.Heartbeat{Executors: []string{"codex", "godot"}, Projects: []string{"moon"}, ProductionWorkflow: true}); err != nil {
		t.Fatal(err)
	}
	w, err := db.SaveWorkflow(ctx, protocol.Workflow{ID: "production", Name: "Game production", Steps: []protocol.Step{
		{ID: "producer", Executor: "codex"},
		{ID: "art", Executor: "codex", DependsOn: []string{"producer"}, BaseFrom: "producer"},
		{ID: "audio", Executor: "codex", DependsOn: []string{"producer"}, BaseFrom: "producer"},
		{ID: "develop", Executor: "codex", DependsOn: []string{"art", "audio"}, BaseFrom: "art", MergeFrom: []string{"audio"}},
		{ID: "ci", Executor: "engine", DependsOn: []string{"develop"}, BaseFrom: "develop", ExportGame: true},
		{ID: "qa", Executor: "codex", DependsOn: []string{"ci"}, BaseFrom: "ci", ReadOnly: true, QAGate: true},
		{ID: "release", Executor: "notify", DependsOn: []string{"qa"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	input := protocol.ExecutionInput{ID: strings.Repeat("1", 32), WorkflowID: w.ID, Version: w.Version, MachineID: machine, Project: "moon", Brief: "Make a game"}
	if _, err := db.StartExecution(ctx, input); err != nil {
		t.Fatal(err)
	}
	completedAssets := make(map[string]bool)
	commits := map[string]string{"producer": "a", "art": "b", "audio": "c", "develop": "d", "ci": "d", "qa": "d"}
	for _, expected := range []string{"producer", "asset", "asset", "develop", "ci", "qa"} {
		if err := db.Schedule(ctx); err != nil {
			t.Fatal(err)
		}
		run, err := db.Claim(ctx, machine)
		if err != nil || run == nil {
			t.Fatalf("claim %s: %+v %v", expected, run, err)
		}
		step := run.StepID
		if expected == "asset" {
			if (step != "art" && step != "audio") || completedAssets[step] {
				t.Fatalf("development did not wait for both assets: %+v", run.Spec)
			}
			completedAssets[step] = true
		} else if step != expected {
			t.Fatalf("claimed %s, expected %s", step, expected)
		}
		switch step {
		case "art", "audio":
			if run.Spec.Commit != strings.Repeat("a", 40) {
				t.Fatalf("resource step missed producer commit: %+v", run.Spec)
			}
		case "develop":
			if run.Spec.Commit != strings.Repeat("b", 40) || !slices.Equal(run.Spec.MergeCommits, []string{strings.Repeat("c", 40)}) {
				t.Fatalf("developer did not receive both resource commits: %+v", run.Spec)
			}
		case "ci":
			if !run.Spec.ExportGame || run.Spec.Executor != "godot" {
				t.Fatalf("tests did not require a playable export: %+v", run.Spec)
			}
		case "qa":
			if !run.Spec.QAGate || !run.Spec.ReadOnly {
				t.Fatalf("QA did not gate delivery: %+v", run.Spec)
			}
		}
		if err := db.Start(ctx, machine, run.ID, run.Lease); err != nil {
			t.Fatal(err)
		}
		result := protocol.Result{Status: "succeeded", Detail: "complete", Commit: strings.Repeat(commits[step], 40)}
		if _, err := db.Report(ctx, machine, run.ID, protocol.Report{Lease: run.Lease, Result: &result}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := db.Schedule(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.db.Exec(ctx, `UPDATE workflows SET archived=true`); err != nil {
		t.Fatal(err)
	}
	execution, err := db.Execution(ctx, input.ID)
	if err != nil || execution.Status != "succeeded" || !slices.Contains(execution.Approved, "release") || len(execution.Runs) != 6 {
		t.Fatalf("delivery or history missing: %+v %v", execution, err)
	}
	list, err := db.Workflows(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("archive still in workflow editor: %+v %v", list, err)
	}
	input.ID = strings.Repeat("2", 32)
	if _, err := db.StartExecution(ctx, input); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("archived workflow accepted new execution: %v", err)
	}
}

func TestCIRepairChecksNewCommitBeforeQA(t *testing.T) {
	ctx, db, _ := integrationStore(t)
	machine, err := db.Enroll(ctx, "CI repair")
	if err != nil {
		t.Fatal(err)
	}
	mid := machine.Machine.ID
	if err := db.Heartbeat(ctx, mid, protocol.Heartbeat{Executors: []string{"godot", "qoder-cn", "minimax-code"}, Projects: []string{"moon"}, ProductionWorkflow: true, WorkflowReview: true}); err != nil {
		t.Fatal(err)
	}
	workflow, err := db.SaveWorkflow(ctx, protocol.Workflow{ID: "repair", Name: "Repair", Steps: []protocol.Step{
		{ID: "develop", Executor: "qoder-cn", Prompt: "Implement feature", Preflight: true},
		{ID: "ci", Executor: "engine", DependsOn: []string{"develop"}, BaseFrom: "develop", ExportGame: true, CIRepairAgent: "minimax-code"},
		{ID: "qa", Executor: "qoder-cn", DependsOn: []string{"ci"}, BaseFrom: "ci", ReadOnly: true, QAGate: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	eid := strings.Repeat("1", 32)
	if _, err := db.StartExecution(ctx, protocol.ExecutionInput{ID: eid, WorkflowID: workflow.ID, Version: workflow.Version, MachineID: mid, Project: "moon", Brief: "Make a game"}); err != nil {
		t.Fatal(err)
	}
	tick := func() {
		t.Helper()
		if err := db.Schedule(ctx); err != nil {
			t.Fatal(err)
		}
	}
	claim := func(step string) *protocol.Run {
		t.Helper()
		tick()
		job, err := db.Claim(ctx, mid)
		if err != nil || job == nil || job.StepID != step {
			t.Fatalf("expected %s: %+v %v", step, job, err)
		}
		if err := db.Start(ctx, mid, job.ID, job.Lease); err != nil {
			t.Fatal(err)
		}
		return job
	}
	report := func(job *protocol.Run, status, commit string, events ...protocol.Event) {
		t.Helper()
		result := &protocol.Result{Status: status, Detail: "fixture", Commit: commit}
		if _, err := db.Report(ctx, mid, job.ID, protocol.Report{Lease: job.Lease, Result: result, Events: events}); err != nil {
			t.Fatal(err)
		}
	}
	bad, fixed := strings.Repeat("a", 40), strings.Repeat("b", 40)
	develop := claim("develop")
	if !develop.Spec.Preflight {
		t.Fatal("development missing engine preflight")
	}
	report(develop, "succeeded", bad)
	ci := claim("ci")
	report(ci, "failed", bad, protocol.Event{Seq: 1, Kind: "tool", Text: "godot --export-release Web index.html"},
		protocol.Event{Seq: 2, Kind: "godot", Text: "No export template installed"})
	tick()
	if execution, err := db.Execution(ctx, eid); err != nil || execution.Status != "failed" || len(execution.Runs) != 2 {
		t.Fatalf("an environment error unexpectedly scheduled a repair: %+v %v", execution, err)
	}
	if _, err := db.ContinueExecution(ctx, eid); err != nil {
		t.Fatal(err)
	}
	ci = claim("ci")
	report(ci, "failed", bad, protocol.Event{Seq: 1, Kind: "tool", Text: "godot --export-release Web index.html"},
		protocol.Event{Seq: 2, Kind: "godot", Text: "The Web export preset references an invalid resource"})
	tick()
	tick()
	repair := claim("ci:repair")
	if repair.Spec.RepairOf != ci.ID || repair.Spec.Executor != "minimax-code" || repair.Spec.Commit != bad ||
		!strings.Contains(repair.Spec.Prompt, "invalid resource") {
		t.Fatalf("repair did not get the CI input and diagnostic: %+v", repair.Spec)
	}
	report(repair, "succeeded", fixed)
	rebuild := claim("ci")
	if rebuild.Attempt != ci.Attempt+1 || rebuild.Spec.Commit != fixed || !rebuild.Spec.ExportGame {
		t.Fatalf("CI did not rebuild the fixed commit: %+v", rebuild)
	}
	report(rebuild, "succeeded", fixed)
	if qa := claim("qa"); qa.Spec.Commit != fixed {
		t.Fatalf("QA did not get the verified commit: %+v", qa.Spec)
	}
}

func TestQAReworkRoutesAudioAndVerifiesRebuiltRevision(t *testing.T) {
	for _, scenario := range []string{"direct", "mixed_environment", "product_agent", "product_operator", "round_limit", "no_changes", "legacy_environment", "interrupted_rework", "interrupted_triage"} {
		t.Run(scenario, func(t *testing.T) { testQARework(t, scenario) })
	}
}

func testQARework(t *testing.T, scenario string) {
	ctx, db, _ := integrationStore(t)
	enrollment, err := db.Enroll(ctx, "QA rework")
	if err != nil {
		t.Fatal(err)
	}
	mid := enrollment.Machine.ID
	if err := db.Heartbeat(ctx, mid, protocol.Heartbeat{Executors: []string{"godot", "qoder-cn", "codex", "minimax-code"}, Projects: []string{"moon"}, ProductionWorkflow: true, WorkflowReview: true}); err != nil {
		t.Fatal(err)
	}
	w, err := db.SaveWorkflow(ctx, protocol.Workflow{ID: "rework", Name: "Rework", Steps: []protocol.Step{
		{ID: "producer", Executor: "codex"},
		{ID: "audio", Executor: "qoder-cn", DependsOn: []string{"producer"}},
		{ID: "develop", Executor: "codex", DependsOn: []string{"audio"}, BaseFrom: "audio", Preflight: true},
		{ID: "ci", Executor: "engine", DependsOn: []string{"develop"}, BaseFrom: "develop", ExportGame: true},
		{ID: "qa", Executor: "minimax-code", DependsOn: []string{"ci"}, BaseFrom: "ci", QAGate: true, QARework: true},
		{ID: "release", Executor: "notify", DependsOn: []string{"qa"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	eid := strings.Repeat("1", 32)
	if _, err := db.StartExecution(ctx, protocol.ExecutionInput{ID: eid, WorkflowID: w.ID, Version: w.Version, MachineID: mid, Project: "moon", Brief: "Make a game"}); err != nil {
		t.Fatal(err)
	}
	tick := func() {
		t.Helper()
		if err := db.Schedule(ctx); err != nil {
			t.Fatal(err)
		}
	}
	claim := func(step, commit, executor string) *protocol.Run {
		t.Helper()
		tick()
		job, err := db.Claim(ctx, mid)
		if err != nil || job == nil || job.StepID != step || job.Spec.Commit != commit || job.Spec.Executor != executor {
			t.Fatalf("wanted %s on %s by %s: %+v %v", step, commit, executor, job, err)
		}
		if err := db.Start(ctx, mid, job.ID, job.Lease); err != nil {
			t.Fatal(err)
		}
		return job
	}
	report := func(job *protocol.Run, status, commit string, bugs ...protocol.Bug) {
		t.Helper()
		result := &protocol.Result{Status: status, Detail: "fixture", Commit: commit, Bugs: bugs}
		if _, err := db.Report(ctx, mid, job.ID, protocol.Report{Lease: job.Lease, Result: result}); err != nil {
			t.Fatal(err)
		}
	}
	audio, build, fix, integrated := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
	report(claim("producer", "", "codex"), "succeeded", build)
	report(claim("audio", "", "qoder-cn"), "succeeded", audio)
	report(claim("develop", audio, "codex"), "succeeded", build)
	report(claim("ci", build, "godot"), "succeeded", build)
	qa := claim("qa", build, "minimax-code")
	bug := protocol.Bug{Owner: "audio", Title: "Music missing", Steps: "Enter a level", Expected: "Music loops", Actual: "Silence"}
	ticketID := "QA-" + qa.ID[:7] + "-1"
	var findings []protocol.Bug
	operatorTriage := scenario == "product_operator" || scenario == "interrupted_triage"
	mixedEnvironment := scenario == "mixed_environment" || scenario == "interrupted_rework"
	if scenario == "product_agent" || operatorTriage {
		bug.Owner = "producer"
	}
	findings = append(findings, bug)
	if mixedEnvironment {
		env := protocol.Bug{Owner: "environment", Title: "Browser unavailable", Steps: "Open game", Expected: "Browser launches", Actual: "Missing tool"}
		findings = append(findings, env)
	}
	if scenario == "legacy_environment" {
		bug.Owner = "environment"
		report(qa, "failed", build, bug)
		if _, err := db.db.Exec(ctx, `UPDATE executions SET status='failed' WHERE id=$1`, eid); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ContinueExecution(ctx, eid); err != nil {
			t.Fatal(err)
		}
		tick()
		e, err := db.Execution(ctx, eid)
		if err != nil || e.Status != "waiting_qa" || e.QAPhase != "environment" {
			t.Fatalf("legacy QA was not resumed: %+v %v", e, err)
		}
		if err := db.DecideQA(ctx, eid, qa.ID, "environment_ready", protocol.QADecision{}); err != nil {
			t.Fatal(err)
		}
		recheck := claim("qa", build, "minimax-code")
		report(recheck, "failed", build, bug)
		tick()
		if err := db.DecideQA(ctx, eid, recheck.ID, "stop", protocol.QADecision{}); err != nil {
			t.Fatal(err)
		}
		tick()
		e, err = db.Execution(ctx, eid)
		if err != nil || e.Status != "canceled" || len(e.Approved) != 0 {
			t.Fatalf("termination bypassed QA: %+v %v", e, err)
		}
		return
	}
	report(qa, "failed", build, findings...)
	if scenario == "interrupted_rework" || scenario == "interrupted_triage" {
		// Preserve the historical rejection while simulating an older release
		// which repeated QA on the same revision and then lost its lease.
		if _, err := insertRun(ctx, db.db, strings.Repeat("2", 32), qa.Spec, eid, "qa", 2); err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.Exec(ctx, `UPDATE runs SET status='lost' WHERE execution_id=$1 AND step_id='qa' AND attempt=2`, eid); err != nil {
			t.Fatal(err)
		}
		tick()
		e, err := db.Execution(ctx, eid)
		if err != nil || e.Status != "failed" || e.Reviews["qa"].RunID != qa.ID {
			t.Fatalf("missing original rejection on continuation: %+v %v", e, err)
		}
		if _, err := db.ContinueExecution(ctx, eid); err != nil {
			t.Fatal(err)
		}
	}
	if scenario == "product_agent" || operatorTriage {
		triage := claim("qa:triage", build, "codex")
		plan := map[string]protocol.Assignment{ticketID: {Owner: "develop", Helpers: []string{"audio"}}}
		payload, _ := json.Marshal(plan)
		if operatorTriage {
			payload = []byte("{}")
		}
		if _, err := db.Report(ctx, mid, triage.ID, protocol.Report{Lease: triage.Lease, Result: &protocol.Result{Status: "succeeded", Commit: build, Detail: "fixture", Reply: string(payload)}}); err != nil {
			t.Fatal(err)
		}
		if operatorTriage {
			tick()
			e, err := db.Execution(ctx, eid)
			if err != nil || e.Status != "waiting_qa" || e.QAPhase != "product_review" {
				t.Fatalf("expected product decision: %+v %v", e, err)
			}
			if err := db.DecideQA(ctx, eid, qa.ID, "assign", protocol.QADecision{}); !errors.Is(err, ErrConflict) {
				t.Fatalf("accepted incomplete plan: %v", err)
			}
			d := protocol.QADecision{Assignments: plan, Note: "Check audio binding"}
			if err := db.DecideQA(ctx, eid, qa.ID, "assign", d); err != nil {
				t.Fatal(err)
			}
			if err := db.DecideQA(ctx, eid, qa.ID, "assign", d); !errors.Is(err, ErrConflict) {
				t.Fatalf("accepted replay: %v", err)
			}
		}
	}
	asset := claim("qa:fix:audio", build, "qoder-cn")
	if asset.Spec.RepairOf != qa.ID || !strings.Contains(asset.Spec.Prompt, bug.Title) {
		t.Fatalf("missing ticket handoff: %+v", asset.Spec)
	}
	if scenario == "no_changes" {
		report(asset, "succeeded", build)
		report(claim("qa:fix:develop", build, "codex"), "succeeded", build)
		tick()
		e, err := db.Execution(ctx, eid)
		if err != nil || e.QAPhase != "product_review" {
			t.Fatalf("expected no-change review: %+v %v", e, err)
		}
		d := protocol.QADecision{Assignments: map[string]protocol.Assignment{ticketID: {Owner: "audio"}}, Note: "Replace missing clip"}
		if err := db.DecideQA(ctx, eid, qa.ID, "assign", d); err != nil {
			t.Fatal(err)
		}
		asset = claim("qa:fix:audio", build, "qoder-cn")
		if asset.Attempt != 2 || !strings.Contains(asset.Spec.Prompt, d.Note) {
			t.Fatal("did not re-dispatch the clarified task")
		}
	}
	report(asset, "succeeded", fix)
	develop := claim("qa:fix:develop", fix, "codex")
	if !develop.Spec.Preflight {
		t.Fatal("rework bypassed development check")
	}
	report(develop, "succeeded", integrated)
	rebuild := claim("ci", integrated, "godot")
	if rebuild.Spec.RecheckOf != qa.ID || !rebuild.Spec.ExportGame {
		t.Fatalf("missing CI recheck: %+v", rebuild.Spec)
	}
	report(rebuild, "succeeded", integrated)
	if mixedEnvironment {
		tick()
		tick()
		e, err := db.Execution(ctx, eid)
		if err != nil || e.QAPhase != "environment" || e.Status != "waiting_qa" || len(e.Approved) != 0 {
			t.Fatalf("missing environment gate: %+v %v", e, err)
		}
		if job, err := db.Claim(ctx, mid); err != nil || job != nil {
			t.Fatalf("unexpected pending work: %+v %v", job, err)
		}
		if err := db.DecideQA(ctx, eid, qa.ID, "environment_ready", protocol.QADecision{}); err != nil {
			t.Fatal(err)
		}
	}
	review := claim("qa", integrated, "minimax-code")
	if scenario == "interrupted_rework" {
		if review.Attempt != 3 {
			t.Fatalf("expected review after repairing, got attempt %d", review.Attempt)
		}
		report(review, "failed", integrated)
		tick()
		if _, err := db.ContinueExecution(ctx, eid); err != nil {
			t.Fatal(err)
		}
		review = claim("qa", integrated, "minimax-code")
		if review.Attempt != 4 {
			t.Fatalf("expected retry of rebuilt revision, got attempt %d", review.Attempt)
		}
	}
	if !strings.Contains(review.Spec.Prompt, ticketID) {
		t.Fatal("lost the ticket ID before recheck")
	}
	if e, err := db.Execution(ctx, eid); err != nil || e.Status != "running" || len(e.Approved) != 0 {
		t.Fatalf("released before QA passed: %+v %v", e, err)
	}
	if scenario == "round_limit" {
		bug.ID, bug.Owner, bug.Title = ticketID, "develop", "Audio still not connected"
		report(review, "failed", integrated, bug)
		next := strings.Repeat("e", 40)
		report(claim("qa:fix:develop", integrated, "codex"), "succeeded", next)
		report(claim("ci", next, "godot"), "succeeded", next)
		last := claim("qa", next, "minimax-code")
		report(last, "failed", next, bug)
		tick()
		e, err := db.Execution(ctx, eid)
		if err != nil || e.Status != "waiting_qa" || e.QAPhase != "product_review" || len(e.Tickets["qa"]) != 1 || e.Tickets["qa"][0].ID != ticketID {
			t.Fatalf("expected product escalation: %+v %v", e, err)
		}
		if err := db.DecideQA(ctx, eid, qa.ID, "stop", protocol.QADecision{}); !errors.Is(err, ErrConflict) {
			t.Fatalf("accepted stale termination: %v", err)
		}
		if err := db.DecideQA(ctx, eid, last.ID, "recheck", protocol.QADecision{Note: "Verify against original spec"}); err != nil {
			t.Fatal(err)
		}
		review, integrated = claim("qa", next, "minimax-code"), next
	}
	report(review, "succeeded", integrated)
	tick()
	tick()
	if e, err := db.Execution(ctx, eid); err != nil || e.Status != "succeeded" || !slices.Contains(e.Approved, "release") {
		t.Fatalf("release did not follow recheck: %+v %v", e, err)
	} else {
		for _, ticket := range e.Tickets["qa"] {
			if scenario == "product_agent" || operatorTriage {
				if ticket.Owner != "develop" || !slices.Contains(ticket.Helpers, "audio") {
					t.Fatalf("lost assigned roles: %+v", ticket)
				}
			}
			if !ticket.Closed {
				t.Fatalf("QA pass left ticket open: %+v", ticket)
			}
		}
	}
}
