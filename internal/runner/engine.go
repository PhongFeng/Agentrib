package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

// Engine adapters validate their project layout and test/export a checked-out revision.
// Build emits test output with kind "test" so downstream QA can use it.
type engineAdapter interface {
	Probe(context.Context) error
	ValidateConfig(Project) error
	ValidateDirectory(checkout, dir string) error
	Build(context.Context, string, string, Project, Emit) ([]protocol.Artifact, error)
}

var engineAdapters = map[string]struct {
	commands []string
	create   func(string) engineAdapter
}{
	protocol.DefaultEngine: {commands: []string{"godot", "godot4"}, create: func(path string) engineAdapter { return godotEngine{path} }},
}

type godotEngine struct{ command string }

func (g godotEngine) Probe(ctx context.Context) error {
	return command(ctx, "", g.command, "--version").Run()
}

func (godotEngine) ValidateConfig(p Project) error {
	if !safeRelative(p.TestScript) || !strings.HasSuffix(p.TestScript, ".gd") {
		return errors.New("Godot requires a project-relative .gd test script")
	}
	return nil
}

func (godotEngine) ValidateDirectory(checkout, dir string) error {
	marker, err := contained(checkout, filepath.Join(dir, "project.godot"))
	if err != nil {
		return errors.New("Git checkout must contain a Godot project.godot")
	}
	info, err := os.Stat(marker)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("project.godot must be a regular file")
	}
	return nil
}
