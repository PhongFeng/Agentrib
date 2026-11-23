package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const godotTools = `{"tools":[
 {"name":"get_godot_version","description":"Read the Runner's Godot version.","inputSchema":{"type":"object","additionalProperties":false}},
 {"name":"run_project","description":"Run the assigned Godot workspace in debug mode. Optionally choose a project-relative .tscn scene.","inputSchema":{"type":"object","properties":{"scene":{"type":"string"}},"additionalProperties":false}},
 {"name":"get_debug_output","description":"Read output from the Godot session started by this tool.","inputSchema":{"type":"object","additionalProperties":false}},
 {"name":"stop_project","description":"Stop the Godot session started by this tool.","inputSchema":{"type":"object","additionalProperties":false}}
]}`

const godotMCPName = "agentrib_godot"
const godotMCPPrompt = "\nUse the agentrib_godot tools to run the current project or a specific scene, read its runtime output, and stop it when done."

type godotMCPServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func godotMCPForWorkspace(tool, godot, dir string) (godotMCPServer, error) {
	exe, err := os.Executable()
	if err != nil {
		return godotMCPServer{}, err
	}
	return godotMCPServer{Command: exe, Args: []string{"-godot-mcp", tool, "-godot-engine", godot, "-godot-project", dir}}, nil
}

func (s godotMCPServer) codexOptions() []string {
	args, _ := json.Marshal(s.Args)
	const key = "mcp_servers." + godotMCPName + "."
	return []string{"-c", key + "command=" + strconv.Quote(s.Command), "-c", key + "args=" + string(args),
		"-c", key + `default_tools_approval_mode="approve"`}
}

func (s godotMCPServer) qoderConfig() string {
	data, _ := json.Marshal(map[string]map[string]godotMCPServer{"mcpServers": {godotMCPName: s}})
	return string(data)
}

func (s godotMCPServer) miniMaxConfig(dir string) (func() error, error) {
	path := filepath.Join(dir, ".mcp.json")
	document := map[string]json.RawMessage{}
	servers := map[string]json.RawMessage{}
	info, err := os.Lstat(path)
	var original []byte
	if err == nil {
		if !info.Mode().IsRegular() || info.Size() > 512<<10 {
			return nil, errors.New("unsupported project .mcp.json file")
		}
		original, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if json.Unmarshal(original, &document) != nil || document == nil {
			return nil, errors.New("project .mcp.json is not a JSON object")
		}
		if raw := document["mcpServers"]; raw != nil && (json.Unmarshal(raw, &servers) != nil || servers == nil) {
			return nil, errors.New("project .mcp.json has an invalid mcpServers object")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	servers[godotMCPName], _ = json.Marshal(s)
	document["mcpServers"], _ = json.Marshal(servers)
	data, _ := json.Marshal(document)
	if err := writeMCPConfig(path, data, 0600); err != nil {
		return nil, err
	}
	return func() error {
		if info != nil {
			return writeMCPConfig(path, original, info.Mode().Perm())
		}
		if err := os.Remove(path); !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}, nil
}

func writeMCPConfig(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".agentrib-mcp-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Chmod(mode), file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

type godotMCPClient struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Scanner
	id     int
}

func startGodotMCP(ctx context.Context, tool, godot, dir string) (*godotMCPClient, error) {
	if !filepath.IsAbs(tool) || !filepath.IsAbs(godot) {
		return nil, errors.New("Godot and its MCP launcher require absolute paths")
	}
	cmd := command(ctx, dir, tool)
	cmd.Env = slices.DeleteFunc(cmd.Env, func(value string) bool {
		return strings.HasPrefix(value, "GODOT_PATH=")
	})
	cmd.Env = append(cmd.Env, "GODOT_PATH="+godot)
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	client := &godotMCPClient{cmd: cmd, input: input, output: scanner}
	params := json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"Agentrib","version":"1"}}`)
	if _, err := client.call("initialize", params); err != nil {
		client.close()
		return nil, err
	}
	if err := json.NewEncoder(input).Encode(map[string]string{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		client.close()
		return nil, err
	}
	return client, nil
}

func (c *godotMCPClient) close() {
	_ = c.input.Close()
	_ = c.cmd.Cancel()
	_ = c.cmd.Wait()
}

func (c *godotMCPClient) call(method string, params any) (json.RawMessage, error) {
	c.id++
	request := map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method}
	if params != nil {
		request["params"] = params
	}
	if err := json.NewEncoder(c.input).Encode(request); err != nil {
		return nil, err
	}
	for c.output.Scan() {
		var response struct {
			ID     int
			Result json.RawMessage
			Error  json.RawMessage
		}
		if err := json.Unmarshal(c.output.Bytes(), &response); err != nil {
			return nil, errors.New("Godot MCP returned an invalid message")
		}
		if response.ID != c.id {
			continue
		}
		if len(response.Result) == 0 || len(response.Error) != 0 {
			return nil, fmt.Errorf("Godot MCP rejected %s", method)
		}
		return response.Result, nil
	}
	if err := c.output.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("Godot MCP closed the connection")
}

func probeGodotMCP(ctx context.Context, tool, godot string) error {
	client, err := startGodotMCP(ctx, tool, godot, "")
	if err != nil {
		return err
	}
	defer client.close()
	raw, err := client.call("tools/call", map[string]string{"name": "get_godot_version"})
	if err != nil {
		return err
	}
	var version struct {
		IsError bool
		Content []struct{ Text string }
	}
	if json.Unmarshal(raw, &version) != nil || version.IsError || len(version.Content) == 0 || strings.TrimSpace(version.Content[0].Text) == "" {
		return errors.New("Godot MCP did not report the engine version")
	}
	return nil
}

func godotToolArguments(project, name string, input json.RawMessage) (map[string]string, error) {
	args := map[string]string{}
	switch name {
	case "get_godot_version", "get_debug_output", "stop_project":
		return args, nil
	case "run_project":
		var options struct{ Scene string }
		if len(input) != 0 && json.Unmarshal(input, &options) != nil {
			return nil, errors.New("invalid Godot scene arguments")
		}
		args["projectPath"] = project
		scene := strings.TrimPrefix(options.Scene, "res://")
		if scene != "" {
			if !filepath.IsLocal(scene) || !safeRelative(scene) || filepath.Ext(scene) != ".tscn" {
				return nil, errors.New("scene must be a .tscn path inside this project")
			}
			full, err := contained(project, filepath.Join(project, scene))
			if err != nil {
				return nil, errors.New("scene is unavailable in this project")
			}
			if info, err := os.Stat(full); err != nil || !info.Mode().IsRegular() {
				return nil, errors.New("scene must be a regular project file")
			}
			relative, err := filepath.Rel(project, full)
			if err != nil {
				return nil, err
			}
			args["scene"] = "res://" + filepath.ToSlash(relative)
		}
		return args, nil
	default:
		return nil, errors.New("unsupported Godot tool")
	}
}

func ServeGodotMCP(ctx context.Context, tool, godot, project string, in io.Reader, out io.Writer) error {
	if !filepath.IsAbs(project) {
		return errors.New("Godot workspace requires an absolute path")
	}
	project, err := filepath.EvalSymlinks(project)
	if err != nil {
		return err
	}
	if _, err := contained(project, filepath.Join(project, "project.godot")); err != nil {
		return errors.New("Godot workspace has no local project.godot")
	}
	client, err := startGodotMCP(ctx, tool, godot, project)
	if err != nil {
		return err
	}
	defer client.close()
	scanner := bufio.NewScanner(in)
	encoder := json.NewEncoder(out)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage
			Method string
			Params json.RawMessage
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || len(request.ID) == 0 {
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "initialize":
			var initial struct{ ProtocolVersion string }
			_ = json.Unmarshal(request.Params, &initial)
			response["result"] = map[string]any{"protocolVersion": initial.ProtocolVersion,
				"capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo":   map[string]string{"name": "Agentrib Godot MCP", "version": "1"}}
		case "ping":
			response["result"] = map[string]any{}
		case "tools/list":
			response["result"] = json.RawMessage(godotTools)
		case "tools/call":
			var call struct {
				Name      string
				Arguments json.RawMessage
			}
			_ = json.Unmarshal(request.Params, &call)
			args, err := godotToolArguments(project, call.Name, call.Arguments)
			var result any
			if err == nil {
				result, err = client.call("tools/call", map[string]any{"name": call.Name, "arguments": args})
			}
			if err != nil {
				result = map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": err.Error()}}}
			}
			response["result"] = result
		default:
			response["error"] = map[string]any{"code": -32601, "message": "Method not found"}
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}
