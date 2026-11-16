package control

import (
	"strings"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func TestValidateWorkflow(t *testing.T) {
	if err := validateWorkflow(godotTemplate); err != nil {
		t.Fatal(err)
	}
	invalid := godotTemplate
	invalid.Steps = append([]protocol.Step(nil), godotTemplate.Steps...)
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
