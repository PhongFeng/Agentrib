package store

import (
	"errors"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func TestGameProjectLifecycle(t *testing.T) {
	ctx, db, _ := integrationStore(t)
	enrollment, err := db.Enroll(ctx, "test runner")
	if err != nil {
		t.Fatal(err)
	}
	machine := enrollment.Machine.ID
	h := protocol.Heartbeat{Executors: []string{"codex"}, Projects: []string{"existing"}}
	if err := db.Heartbeat(ctx, machine, h); err != nil {
		t.Fatal(err)
	}
	input := protocol.ProjectInput{ID: "new-game", Name: "新游戏", Engine: "godot"}
	if err := db.RequestProject(ctx, machine, input); !errors.Is(err, ErrProjectCreation) {
		t.Fatalf("legacy Runner accepted creation: %v", err)
	}
	h.CreateProjects = true
	h.AgentStatuses = map[string]protocol.AgentStatus{
		"codex": {Account: "available", Quotas: []protocol.AgentQuota{{Remaining: 75, Unit: "percent", WindowMinutes: 300}}},
	}
	heartbeat := func() protocol.HeartbeatReply {
		t.Helper()
		reply, err := db.ProjectHeartbeat(ctx, machine, h)
		if err != nil {
			t.Fatal(err)
		}
		return reply
	}
	assertStatus := func(status, workspace string) {
		t.Helper()
		projects, err := db.Projects(ctx, machine)
		if err != nil || len(projects) != 2 || projects[0].ID != "existing" || projects[0].Status != "ready" ||
			projects[1].ProjectInput != input || projects[1].Status != status || projects[1].Workspace != workspace {
			t.Fatalf("expected legacy project and %s managed workspace, got %+v, %v", status, projects, err)
		}
	}
	heartbeat()
	machines, err := db.Machines(ctx)
	if err != nil || len(machines) != 1 {
		t.Fatalf("could not list enrolled machine: %+v, %v", machines, err)
	}
	status := machines[0].AgentStatuses["codex"]
	if status.Account != "available" || len(status.Quotas) != 1 || status.Quotas[0] != h.AgentStatuses["codex"].Quotas[0] {
		t.Fatalf("Agent quota did not round-trip: %+v", status)
	}
	existing := input
	existing.ID = "existing"
	if err := db.RequestProject(ctx, machine, existing); !errors.Is(err, ErrProjectExists) {
		t.Fatalf("existing project was overwritten: %v", err)
	}
	if err := db.RequestProject(ctx, machine, input); err != nil {
		t.Fatal(err)
	}
	assertStatus("pending", "")
	first := heartbeat().Project
	if first == nil || first.ProjectInput != input {
		t.Fatalf("wrong project request: %+v", first)
	}
	h.ProjectResult = &protocol.ProjectResult{Attempt: first.Attempt, Error: "initialization failed"}
	if reply := heartbeat(); reply.Project != nil {
		t.Fatalf("failed request was sent again: %+v", reply)
	}
	assertStatus("failed", "")
	if err := db.RequestProject(ctx, machine, input); err != nil {
		t.Fatal(err)
	}
	retry := heartbeat().Project
	if retry == nil || retry.Attempt == first.Attempt {
		t.Fatalf("retry did not start a new attempt: %+v", retry)
	}
	h.ProjectResult = &protocol.ProjectResult{Attempt: retry.Attempt}
	if reply := heartbeat(); reply.Project == nil {
		t.Fatal("request completed without an advertised workspace")
	}
	h.Projects = append(h.Projects, input.ID)
	h.ProjectEngines = map[string]string{input.ID: input.Engine}
	h.ProjectWorkspaces = map[string]string{input.ID: "/runner/projects/new-game"}
	if reply := heartbeat(); reply.Project != nil {
		t.Fatalf("confirmed project was sent again: %+v", reply)
	}
	assertStatus("ready", h.ProjectWorkspaces[input.ID])
	h.Projects = h.Projects[:1]
	h.ProjectEngines, h.ProjectWorkspaces, h.ProjectResult = nil, nil, nil
	if reply := heartbeat(); reply.Project != nil {
		t.Fatal("missing workspace was silently recreated")
	}
	assertStatus("unavailable", "")
}
