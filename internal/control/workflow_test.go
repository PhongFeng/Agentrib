package control

import (
	"strings"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func TestValidateWorkflow(t *testing.T) {
	if err := validateWorkflow(gameTemplate); err != nil {
		t.Fatal(err)
	}
	ui, build, qa := gameTemplate.Steps[1], gameTemplate.Steps[2], gameTemplate.Steps[4]
	if ui.Executor != "minimax-code" || build.Executor != "codex" || build.BaseFrom != ui.ID || qa.Executor != "qoder-cn" || !qa.ReadOnly || qa.BaseFrom != "tests" {
		t.Fatal("default workflow must hand the MiniMax UI commit to Codex and the Godot-tested commit to read-only Qoder QA")
	}
	planned := gameTemplate
	planned.Engine = "unity"
	if err := validateWorkflow(planned); err != nil {
		t.Fatalf("planned engine could not be saved: %v", err)
	}
	invalid := gameTemplate
	invalid.Steps = append([]protocol.Step(nil), gameTemplate.Steps...)
	invalid.Steps[1].ReadOnly = true
	if err := validateWorkflow(invalid); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only MiniMax step accepted: %v", err)
	}
	invalid.Steps[1].ReadOnly = false
	invalid.Steps[0].DependsOn = []string{"release"}
	if err := validateWorkflow(invalid); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle accepted: %v", err)
	}
	invalid.Steps[0].DependsOn = nil
	invalid.Steps[2].BaseFrom = "brief"
	if err := validateWorkflow(invalid); err == nil || !strings.Contains(err.Error(), "Commit source") {
		t.Fatalf("incorrect commit source accepted: %v", err)
	}
}
