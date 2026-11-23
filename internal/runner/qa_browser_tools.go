package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Codex starts a per-run stdio MCP client. Its private socket only accepts
// game input and screenshots from the one page the Runner has opened.
func (b *qaBrowser) codexTools(ctx context.Context) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("/tmp", "agentrib-qa-mcp-")
	if err != nil {
		return nil, err
	}
	socket := filepath.Join(dir, "tools.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		_ = os.Remove(dir)
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	closeListener := context.AfterFunc(ctx, func() { _ = listener.Close() })
	b.stopTools = func() {
		cancel()
		closeListener()
		_ = listener.Close()
		<-done
		_ = os.Remove(dir)
	}
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
			b.serveBrowserTools(conn)
			stop()
			_ = conn.Close()
		}
	}()
	const prefix = "mcp_servers.agentrib_game_qa."
	return []string{"-c", prefix + "command=" + strconv.Quote(exe),
		"-c", prefix + "args=[\"-qa-browser-mcp\", " + strconv.Quote(socket) + "]",
		"-c", prefix + `tools.game_browser.approval_mode="approve"`}, nil
}

func ConnectBrowserTools(ctx context.Context, socket string, in io.Reader, out io.Writer) error {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	go func() {
		_, _ = io.Copy(conn, in)
		_ = conn.(*net.UnixConn).CloseWrite()
	}()
	_, err = io.Copy(out, conn)
	return err
}

func (b *qaBrowser) serveBrowserTools(conn net.Conn) {
	input := bufio.NewScanner(conn)
	output := json.NewEncoder(conn)
	for input.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(input.Bytes(), &request) != nil || len(request.ID) == 0 {
			continue
		}
		reply := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "initialize":
			var initial struct{ ProtocolVersion string }
			_ = json.Unmarshal(request.Params, &initial)
			switch initial.ProtocolVersion {
			case "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25":
			default:
				initial.ProtocolVersion = "2025-11-25"
			}
			reply["result"] = map[string]any{"protocolVersion": initial.ProtocolVersion,
				"capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo":   map[string]string{"name": "Agentrib QA browser", "version": "1.0"}}
		case "ping":
			reply["result"] = map[string]any{}
		case "tools/list":
			const tool = `{"tools":[{"name":"game_browser","description":"Observe or interact with the running CI game. Each call returns the current browser screenshot.","inputSchema":{"type":"object","properties":{"action":{"type":"string","enum":["view","click","key"]},"x":{"type":"integer"},"y":{"type":"integer"},"key":{"type":"string","description":"Arrows, Enter, Escape, Space, or a single letter or digit."}},"required":["action"],"additionalProperties":false}}]}`
			reply["result"] = json.RawMessage(tool)
		case "tools/call":
			var call struct {
				Name      string
				Arguments browserAction
			}
			if json.Unmarshal(request.Params, &call) != nil || call.Name != "game_browser" {
				reply["error"] = map[string]any{"code": -32602, "message": "Invalid game browser arguments"}
				break
			}
			image, err := b.interact(call.Arguments)
			if err != nil {
				reply["result"] = map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": err.Error()}}}
			} else {
				reply["result"] = map[string]any{"content": []any{map[string]string{"type": "image", "mimeType": "image/png", "data": image}}}
			}
		default:
			reply["error"] = map[string]any{"code": -32601, "message": "Method not found"}
		}
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if output.Encode(reply) != nil {
			return
		}
	}
}

type browserAction struct {
	Action string
	X, Y   *int
	Key    string
}

func (b *qaBrowser) interact(input browserAction) (string, error) {
	p := b.pipe
	deadline := time.Now().Add(8 * time.Second)
	_ = p.output.SetReadDeadline(deadline)
	_ = p.write.SetWriteDeadline(deadline)
	var current struct {
		Result struct{ Value string }
	}
	if err := p.call(b.session, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true}, &current); err != nil {
		return "", err
	}
	if current.Result.Value != b.url {
		return "", errors.New("QA browser has left the CI game page")
	}
	switch input.Action {
	case "view":
		return b.capturePNG(p)
	case "click":
		if input.X == nil || input.Y == nil || *input.X < 0 || *input.X >= 1280 || *input.Y < 0 || *input.Y >= 800 {
			return "", errors.New("click requires x and y within the screenshot")
		}
		for _, kind := range []string{"mousePressed", "mouseReleased"} {
			args := map[string]any{"type": kind, "x": *input.X, "y": *input.Y, "button": "left", "clickCount": 1}
			if err := p.call(b.session, "Input.dispatchMouseEvent", args, &struct{}{}); err != nil {
				return "", err
			}
		}
	case "key":
		key, code, number, ok := gameKey(input.Key)
		if !ok {
			return "", errors.New("unsupported game key")
		}
		for _, kind := range []string{"rawKeyDown", "keyUp"} {
			args := map[string]any{"type": kind, "key": key, "code": code, "windowsVirtualKeyCode": number}
			if err := p.call(b.session, "Input.dispatchKeyEvent", args, &struct{}{}); err != nil {
				return "", err
			}
		}
	default:
		return "", errors.New("unsupported browser action")
	}
	time.Sleep(150 * time.Millisecond)
	return b.capturePNG(p)
}

func gameKey(key string) (string, string, int, bool) {
	codes := map[string]int{"Enter": 13, "Escape": 27, "Space": 32, "ArrowLeft": 37, "ArrowUp": 38, "ArrowRight": 39, "ArrowDown": 40}
	if number := codes[key]; number != 0 {
		if key == "Space" {
			return " ", key, number, true
		}
		return key, key, number, true
	}
	if len(key) == 1 {
		switch c := key[0]; {
		case c >= '0' && c <= '9':
			return key, "Digit" + key, int(c), true
		case c >= 'a' && c <= 'z':
			letter := c - 'a' + 'A'
			return key, "Key" + string(letter), int(letter), true
		case c >= 'A' && c <= 'Z':
			return key, "Key" + key, int(c), true
		}
	}
	return "", "", 0, false
}
