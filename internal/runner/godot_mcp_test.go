package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGodotMCPBindsProjectAndScene(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "level.tscn"), []byte("[gd_scene format=3]"), 0600); err != nil {
		t.Fatal(err)
	}
	args, err := godotToolArguments(project, "run_project", json.RawMessage(`{"projectPath":"outside","scene":"res://level.tscn"}`))
	if err != nil || args["projectPath"] != project || args["scene"] != "res://level.tscn" {
		t.Fatalf("tool arguments = %v, error = %v", args, err)
	}
	for _, scene := range []string{"../play.tscn", "--editor", "missing.tscn"} {
		payload, err := json.Marshal(map[string]string{"scene": scene})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := godotToolArguments(project, "run_project", payload); err == nil {
			t.Fatalf("allowed invalid scene %q", scene)
		}
	}
	if _, err := godotToolArguments(project, "launch_editor", nil); err == nil {
		t.Fatal("unexpected tool exposed")
	}
}
