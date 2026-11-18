package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

// Engine adapters validate their project layout and test/export a checked-out revision.
// Build emits test output with kind "test" so downstream QA can use it.
type engineAdapter interface {
	Probe(context.Context) error
	ValidateConfig(Project) error
	ValidateDirectory(checkout, dir string) error
	Check(context.Context, string, Emit) error
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

func (g godotEngine) Check(ctx context.Context, dir string, emit Emit) error {
	check, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := g.run(check, dir, emit, "--import"); err != nil {
		return fmt.Errorf("Godot import: %w", err)
	}
	if err := g.run(check, dir, emit, "--quit-after", "2"); err != nil {
		return fmt.Errorf("Godot main scene: %w", err)
	}
	return nil
}

func (g godotEngine) run(ctx context.Context, dir string, emit Emit, args ...string) error {
	emit("tool", "godot "+strings.Join(args, " "))
	cmd := command(ctx, dir, g.command, append([]string{"--headless", "--path", dir}, args...)...)
	kind := "godot"
	if args[0] == "--script" {
		kind = "test"
	}
	var scriptError bool
	lines := &lineWriter{writeLine: func(s string) {
		emit(kind, s)
		scriptError = scriptError || strings.Contains(s, "SCRIPT ERROR:") || strings.Contains(s, "Parse Error:")
	}}
	cmd.Stdout, cmd.Stderr = lines, lines
	err := cmd.Run()
	lines.flush()
	if err == nil && scriptError {
		return errors.New("Godot reported a script or import error")
	}
	return err
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
