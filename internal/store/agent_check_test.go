package store

import (
	"errors"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func TestAgentChecksUseRunnerWithoutProject(t *testing.T) {
	ctx, db, _ := integrationStore(t)
	enrolled, err := db.Enroll(ctx, "Probe runner")
	if err != nil {
		t.Fatal(err)
	}
	machine := enrolled.Machine.ID
	heartbeat := protocol.Heartbeat{Executors: []string{"minimax-code"}, Projects: []string{}}
	if err := db.Heartbeat(ctx, machine, heartbeat); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CheckAgent(ctx, machine, "minimax-code"); !errors.Is(err, ErrAgentCheckUnavailable) {
		t.Fatalf("unsupported Runner accepted a check: %v", err)
	}
	heartbeat.CanCheckAgents = true
	if err := db.Heartbeat(ctx, machine, heartbeat); err != nil {
		t.Fatal(err)
	}
	first, err := db.CheckAgent(ctx, machine, "minimax-code")
	if err != nil || !first.Spec.AgentCheck || first.Spec.Project != "" {
		t.Fatalf("cannot create isolated check: %+v %v", first, err)
	}
	again, err := db.CheckAgent(ctx, machine, "minimax-code")
	if err != nil || again.ID != first.ID {
		t.Fatalf("duplicate pending check: %+v %v", again, err)
	}
	pending, err := db.Claim(ctx, machine)
	if err != nil || pending == nil || pending.ID != first.ID {
		t.Fatalf("project-free check was not claimed: %+v %v", pending, err)
	}
	if err := db.Start(ctx, machine, pending.ID, pending.Lease); err != nil {
		t.Fatal(err)
	}
	result := protocol.Report{Lease: pending.Lease, Result: &protocol.Result{Status: "succeeded", Detail: "Reached model"}}
	if _, err := db.Report(ctx, machine, pending.ID, result); err != nil {
		t.Fatal(err)
	}
	checks, err := db.AgentChecks(ctx, machine)
	if err != nil || len(checks) != 1 || checks[0].Status != "succeeded" || checks[0].FinishedAt == nil {
		t.Fatalf("missing check result: %+v %v", checks, err)
	}
	next, err := db.CheckAgent(ctx, machine, "minimax-code")
	if err != nil || next.ID == first.ID {
		t.Fatalf("finished check should allow another: %+v %v", next, err)
	}
	if _, err := db.db.Exec(ctx, `UPDATE runs SET created=now()-interval '6 minutes' WHERE id=$1`, next.ID); err != nil {
		t.Fatal(err)
	}
	if job, err := db.Claim(ctx, machine); err != nil || job != nil {
		t.Fatalf("expired check must not call the provider: %+v %v", job, err)
	}
}
