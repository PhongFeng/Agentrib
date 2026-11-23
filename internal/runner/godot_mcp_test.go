package runner

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMiniMaxGodotMCPLocalConfigIsRestored(t *testing.T) {
	for _, original := range []string{"", "{\n  \"mcpServers\": {\"other\": {\"command\": \"tool\"}}\n}\n"} {
		project := t.TempDir()
		config := filepath.Join(project, ".mcp.json")
		if original != "" {
			if err := os.WriteFile(config, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
		}
		server := mcpServers{godotMCPName: {Command: "agentrib-runner", Args: []string{"-godot-project", project}}, blenderMCPName: {Command: "blender-tool"}}
		cleanup, err := server.miniMaxConfig(project)
		if err != nil {
			t.Fatal(err)
		}
		during, err := os.ReadFile(config)
		if err != nil || !bytes.Contains(during, []byte(godotMCPName)) || !bytes.Contains(during, []byte(blenderMCPName)) ||
			original != "" && !bytes.Contains(during, []byte(`"other"`)) {
			t.Fatalf("temporary MCP config: %q, error: %v", during, err)
		}
		if err := cleanup(); err != nil {
			t.Fatal(err)
		}
		restored, err := os.ReadFile(config)
		if original == "" && !os.IsNotExist(err) || original != "" && (err != nil || string(restored) != original) {
			t.Fatalf("original MCP config changed: %q, error: %v", restored, err)
		}
	}
}

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
