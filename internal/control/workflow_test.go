package control

import (
	"strings"
	"testing"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func TestValidateWorkflow(t *testing.T) {
	producer, art, audio, develop, ci, qa, release := gameTemplate.Steps[0], gameTemplate.Steps[1], gameTemplate.Steps[2], gameTemplate.Steps[3], gameTemplate.Steps[4], gameTemplate.Steps[5], gameTemplate.Steps[6]
	if producer.ID != "producer" || art.BaseFrom != producer.ID || audio.BaseFrom != producer.ID ||
		develop.ID != "develop" || develop.BaseFrom != art.ID || len(develop.MergeFrom) != 1 || develop.MergeFrom[0] != audio.ID ||
		ci.ID != "ci" || ci.BaseFrom != develop.ID || !ci.ExportGame || qa.BaseFrom != ci.ID || !qa.QAGate || release.Executor != "notify" {
		t.Fatal("game template must leave Agent assignments open and preserve commit handoffs")
	}
	assigned := gameTemplate
	assigned.Steps = append([]protocol.Step(nil), gameTemplate.Steps...)
	for _, index := range []int{0, 1, 2, 3, 5} {
		if assigned.Steps[index].Executor != "" {
			t.Fatal("game template preselected an Agent")
		}
		assigned.Steps[index].Executor = "codex"
	}
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
	invalid.Steps[3].MergeFrom = []string{"art"}
	if err := validateWorkflow(invalid); err == nil || !strings.Contains(err.Error(), "Commit source") {
		t.Fatalf("incorrect commit source accepted: %v", err)
	}
}
