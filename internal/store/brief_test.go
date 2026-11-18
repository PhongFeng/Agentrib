package store

import (
	"errors"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func TestProductAgentRunUsesCompatibleRunner(t *testing.T) {
	ctx, db, _ := integrationStore(t)
	enrollment, err := db.Enroll(ctx, "test runner")
	if err != nil {
		t.Fatal(err)
	}
	machine := enrollment.Machine.ID
	heartbeat := protocol.Heartbeat{Executors: []string{"codex"}, Projects: []string{"game"}}
	reportCapabilities := func(enabled bool) {
		t.Helper()
		heartbeat.BriefChat = enabled
		if err := db.Heartbeat(ctx, machine, heartbeat); err != nil {
			t.Fatal(err)
		}
	}
	reportCapabilities(false)
	id, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	spec := protocol.Spec{MachineID: machine, Project: "game", Executor: "codex", Title: "Brief", Prompt: "History", BriefMode: "draft", ReadOnly: true}
	if _, err := db.CreateBriefRun(ctx, id, spec); !errors.Is(err, ErrBriefUnavailable) {
		t.Fatalf("old Runner accepted product dialogue: %v", err)
	}
	reportCapabilities(true)
	if run, err := db.CreateBriefRun(ctx, id, spec); err != nil || run.Spec.Engine != "godot" {
		t.Fatalf("could not queue product dialogue: %+v, %v", run, err)
	}
	reportCapabilities(false)
	if run, err := db.Claim(ctx, machine); err != nil || run != nil {
		t.Fatalf("old Runner could claim product dialogue: %+v, %v", run, err)
	}
	reportCapabilities(true)
	run, err := db.Claim(ctx, machine)
	if err != nil || run == nil || run.ID != id {
		t.Fatalf("product dialogue was not claimable: %+v, %v", run, err)
	}
	if err := db.Start(ctx, machine, id, run.Lease); err != nil {
		t.Fatal(err)
	}
	result := protocol.Result{Status: "succeeded", Detail: "Execution completed", Reply: "游戏简报"}
	if _, err := db.Report(ctx, machine, id, protocol.Report{Lease: run.Lease, Result: &result}); err != nil {
		t.Fatal(err)
	}
	finished, err := db.Run(ctx, id)
	if err != nil || finished.Result == nil || finished.Result.Reply != result.Reply {
		t.Fatalf("product reply did not round-trip: %+v, %v", finished, err)
	}
}
