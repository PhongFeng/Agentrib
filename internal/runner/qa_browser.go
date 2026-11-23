package runner

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/PhongFeng/Agentrib/internal/webgame"
)

type qaBrowser struct {
	url        string
	screenshot string
	profile    string
	process    *exec.Cmd
	server     *http.Server
	bundle     *webgame.Bundle
	pipe       *browserPipe
	session    string
	stopTools  func()
}

func (b *qaBrowser) Close() {
	if b.pipe != nil {
		_ = b.pipe.output.Close()
		_ = b.pipe.write.Close()
	}
	if b.stopTools != nil {
		b.stopTools()
	}
	if b.process != nil {
		_ = b.process.Cancel()
		_ = b.process.Wait()
	}
	_ = b.server.Close()
	_ = b.bundle.Close()
	_ = os.RemoveAll(b.profile)
}

func (b *qaBrowser) evidence() string {
	return "\n\nRunner 已在隔离的 Chrome 测试会话中打开这次 CI 的原始网页包，加载层已退出，游戏画布已就绪。" +
		" 在本轮 QA 中可打开 " + b.url + " 继续浏览器复验。启动截图位于 " + b.screenshot +
		"。请使用已准备好的 URL，避免另起 HTTP 服务或重新导出。" +
		"启动证据只覆盖网页启动，不代表已验证操作、画面细节或音效。"
}

func (r *Runner) prepareQABrowser(ctx context.Context, build protocol.WebBuild) (*qaBrowser, error) {
	if !runID.MatchString(build.RunID) {
		return nil, errors.New("invalid CI build ID")
	}
	if runtime.GOOS == "windows" {
		return nil, errors.New("QA browser automation requires a macOS or Linux Runner")
	}
	chrome, err := findCommand(r.cfg.Commands["chrome"], "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "google-chrome", "chromium")
	if err != nil {
		return nil, errors.New("Chrome is unavailable; install it or configure commands.chrome")
	}
	filename := filepath.Join(r.cfg.State, "artifacts", build.RunID, protocol.WebBuildArchive)
	artifact, err := identify(filename)
	if err != nil {
		return nil, fmt.Errorf("read original CI ZIP: %w", err)
	}
	if artifact.Digest != build.Digest {
		return nil, errors.New("CI ZIP checksum does not match")
	}
	// Keep the archive open while requests are being served. Its contents are never extracted.
	bundle, err := webgame.Open(filename)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		_ = bundle.Close()
		return nil, err
	}
	profile, err := os.MkdirTemp(r.cfg.State, "qa-browser-")
	if err != nil {
		_ = listener.Close()
		_ = bundle.Close()
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{file...}", func(w http.ResponseWriter, req *http.Request) {
		if req.Host != listener.Addr().String() {
			http.NotFound(w, req)
			return
		}
		bundle.ServeFile(w, req, req.PathValue("file"))
	})
	browser := &qaBrowser{
		url: "http://" + listener.Addr().String() + "/index.html", profile: profile,
		screenshot: filepath.Join(profile, "startup.png"),
		server:     &http.Server{Addr: listener.Addr().String(), Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: time.Minute},
		bundle:     bundle,
	}
	go func() { _ = browser.server.Serve(listener) }()
	if err := browser.open(ctx, chrome); err != nil {
		browser.Close()
		return nil, err
	}
	return browser, nil
}

func (b *qaBrowser) open(ctx context.Context, chrome string) error {
	commands, write, err := os.Pipe()
	if err != nil {
		return err
	}
	defer commands.Close()
	read, events, err := os.Pipe()
	if err != nil {
		_ = write.Close()
		return err
	}
	defer events.Close()
	defer func() {
		if b.pipe == nil {
			_ = write.Close()
			_ = read.Close()
		}
	}()
	cmd := command(ctx, "", chrome, "--headless", "--no-first-run", "--no-default-browser-check", "--use-mock-keychain",
		"--window-size=1280,800", "--user-data-dir="+filepath.Join(b.profile, "chrome"),
		"--use-gl=angle", "--use-angle=swiftshader", "--enable-unsafe-swiftshader",
		"--proxy-server=http://127.0.0.1:9", "--proxy-bypass-list=<-loopback>;"+b.server.Addr,
		"--remote-debugging-pipe", "--no-startup-window")
	cmd.ExtraFiles = []*os.File{commands, events}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start Chrome: %w", err)
	}
	b.process = cmd
	_ = commands.Close()
	_ = events.Close()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(startup, func() {
		_ = write.Close()
		_ = read.Close()
	})
	defer stop()
	devTools := browserPipe{read: bufio.NewReader(read), output: read, write: write}
	if err := b.capture(startup, &devTools); err != nil {
		if startup.Err() != nil {
			return fmt.Errorf("waiting for the CI Web build in Chrome: %w", startup.Err())
		}
		return err
	}
	if !stop() {
		return startup.Err()
	}
	b.pipe = &devTools
	return nil
}

type browserPipe struct {
	read   *bufio.Reader
	output *os.File
	write  *os.File
	id     int
}

func (p *browserPipe) call(session, method string, params any, result any) error {
	p.id++
	message := struct {
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Session string `json:"sessionId,omitempty"`
		Params  any    `json:"params,omitempty"`
	}{p.id, method, session, params}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if _, err := p.write.Write(append(payload, 0)); err != nil {
		return fmt.Errorf("send %s to Chrome: %w", method, err)
	}
	for {
		payload, err := p.read.ReadBytes(0)
		if err != nil {
			return fmt.Errorf("read %s from Chrome: %w", method, err)
		}
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(payload[:len(payload)-1], &response); err != nil {
			return err
		}
		if response.ID != p.id {
			continue
		}
		if len(response.Error) != 0 {
			return fmt.Errorf("Chrome %s failed: %s", method, response.Error)
		}
		return json.Unmarshal(response.Result, result)
	}
}

func (b *qaBrowser) capture(ctx context.Context, p *browserPipe) error {
	var target struct {
		ID string `json:"targetId"`
	}
	if err := p.call("", "Target.createTarget", map[string]string{"url": b.url}, &target); err != nil {
		return err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := p.call("", "Target.attachToTarget", map[string]any{"targetId": target.ID, "flatten": true}, &attached); err != nil {
		return err
	}
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for ctx.Err() == nil {
		const probe = `(() => { const c = document.querySelector('#canvas'); const s = document.querySelector('#status'); return { url: location.href, isolated: crossOriginIsolated, ready: !!c && c.width > 0 && c.height > 0 && (!s || getComputedStyle(s).visibility === 'hidden'), error: (document.querySelector('#status-notice')?.innerText || '').slice(0,300) }; })()`
		var evaluated struct {
			Result struct {
				Value struct {
					URL      string
					Isolated bool
					Ready    bool
					Error    string
				}
			}
		}
		err := p.call(attached.SessionID, "Runtime.evaluate", map[string]any{"expression": probe, "returnByValue": true}, &evaluated)
		if err != nil {
			return err
		}
		page := evaluated.Result.Value
		if page.URL == b.url {
			if page.Error != "" {
				return errors.New("Web export reported: " + strings.TrimSpace(page.Error))
			}
			if page.Isolated && page.Ready {
				b.session = attached.SessionID
				screenshot, err := b.capturePNG(p)
				if err != nil {
					return err
				}
				png, err := base64.StdEncoding.DecodeString(screenshot)
				if err != nil {
					return err
				}
				return os.WriteFile(b.screenshot, png, 0600)
			}
		}
		select {
		case <-ctx.Done():
		case <-tick.C:
		}
	}
	return ctx.Err()
}

func (b *qaBrowser) capturePNG(p *browserPipe) (string, error) {
	var screenshot struct{ Data string }
	err := p.call(b.session, "Page.captureScreenshot", map[string]string{"format": "png"}, &screenshot)
	return screenshot.Data, err
}
