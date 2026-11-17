package control

import (
	"strings"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func TestValidateWorkflow(t *testing.T) {
	ui, build, qa := gameTemplate.Steps[1], gameTemplate.Steps[2], gameTemplate.Steps[4]
	if ui.Executor != "" || build.Executor != "" || qa.Executor != "" || build.BaseFrom != ui.ID || qa.BaseFrom != "tests" {
		t.Fatal("game template must leave Agent assignments open and preserve commit handoffs")
	}
	assigned := gameTemplate
	assigned.Steps = append([]protocol.Step(nil), gameTemplate.Steps...)
	assigned.Steps[1].Executor, assigned.Steps[2].Executor, assigned.Steps[4].Executor = "deepseek-harness", "codex", "qoder-cn"
	if err := validateWorkflow(assigned); err != nil {
		t.Fatal(err)
	}
	planned := assigned
	planned.Engine = "unity"
	if err := validateWorkflow(planned); err != nil {
		t.Fatalf("planned engine could not be saved: %v", err)
	}
	invalid := assigned
	invalid.Steps = append([]protocol.Step(nil), assigned.Steps...)
	invalid.Steps[1].Executor = "minimax-code"
	invalid.Steps[1].ReadOnly = true
	if err := validateWorkflow(invalid); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only step accepted for a CLI without read-only support: %v", err)
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
