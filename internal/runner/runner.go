package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

const (
	heartbeatPeriod     = 8 * time.Second
	claimPeriod         = 2 * time.Second
	reportPeriod        = 1500 * time.Millisecond
	disconnectLimit     = 28 * time.Second
	maxJobDuration      = 20 * time.Minute
	workflowJobDuration = 12 * time.Hour
)

type Runner struct {
	cfg        Config
	client     *http.Client
	backends   map[string]Backend
	engines    map[string]engineAdapter
	agentPaths map[string]string
}

var errLease = errors.New("run lease no longer active")
var errToken = errors.New("runner token rejected")
var runID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func New(cfg Config) *Runner {
	client := &http.Client{Timeout: 8 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("runner API redirect refused") }}
	if cfg.proxy != nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(cfg.proxy)
		client.Transport = transport
	}
	return &Runner{cfg: cfg, client: client,
		backends: make(map[string]Backend), engines: make(map[string]engineAdapter), agentPaths: make(map[string]string)}
}

func (r *Runner) Detect(ctx context.Context) protocol.Heartbeat {
	h := protocol.Heartbeat{Executors: []string{}, AgentStatuses: make(map[string]protocol.AgentStatus), BriefChat: true, ProductionWorkflow: true, WorkflowReview: true}
	_, gitErr := findCommand("", "git")
	h.CreateProjects = gitErr == nil
	r.updateProjects(&h)
	for _, agent := range protocol.Agents {
		key := agent.ID
		path, err := findCommand(r.cfg.Commands[key], agent.Commands...)
		if err != nil {
			slog.Info("executor unavailable", "executor", key)
			continue
		}
		r.agentPaths[key] = path
		h.AgentStatuses[key] = protocol.AgentStatus{}
		check, cancel := context.WithTimeout(ctx, 8*time.Second)
		backend := backendFor(agent, path)
		err = backend.Probe(check)
		cancel()
		if err != nil {
			slog.Info("executor probe failed", "executor", key, "error", err)
			continue
		}
		r.backends[key] = backend
		h.Executors = append(h.Executors, key)
		if key == "codex" {
			h.AgentStatuses[key] = protocol.AgentStatus{Account: "signed_in"}
		}
	}
	for key, adapter := range engineAdapters {
		path, err := findCommand(r.cfg.Commands[key], adapter.commands...)
		if err == nil {
			check, cancel := context.WithTimeout(ctx, 8*time.Second)
			err = adapter.create(path).Probe(check)
			cancel()
		}
		if err != nil {
			slog.Info("engine command unavailable", "engine", key, "error", err)
			path = ""
		} else {
			h.Executors = append(h.Executors, key)
		}
		r.engines[key] = adapter.create(path)
	}
	return h
}

func (r *Runner) post(ctx context.Context, path string, body, target any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return r.request(ctx, http.MethodPost, path, "application/json", bytes.NewReader(data), int64(len(data)), nil, target)
}

func (r *Runner) request(ctx context.Context, method, path, contentType string, body io.Reader, length int64, headers map[string]string, target any) error {
	req, err := http.NewRequestWithContext(ctx, method, r.cfg.Server+"/api/v1/runner"+path, body)
	if err != nil {
		return err
	}
	req.ContentLength = length
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+r.cfg.token)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	client := r.client
	if method == http.MethodPut {
		long := *client
		long.Timeout = 3 * time.Minute
		client = &long
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return errToken
	case http.StatusConflict:
		return errLease
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("runner API %s returned HTTP %d", path, resp.StatusCode)
	}
	if target != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(target)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return err
}

func (r *Runner) uploadArtifacts(ctx context.Context, job protocol.Run, result *protocol.Result) error {
	files := result.Artifacts
	result.Artifacts = nil
	for _, artifact := range files {
		if err := r.uploadArtifact(ctx, job, artifact); err != nil {
			if strings.HasPrefix(artifact.Name, protocol.PreviewArtifactPrefix) {
				// The Git result remains usable when an optional preview upload fails.
				continue
			}
			return fmt.Errorf("upload %s: %w", artifact.Name, err)
		}
		result.Artifacts = append(result.Artifacts, artifact)
	}
	return nil
}

func (r *Runner) uploadArtifact(ctx context.Context, job protocol.Run, artifact protocol.Artifact) error {
	file, err := os.Open(filepath.Join(r.cfg.State, "artifacts", job.ID, artifact.Name))
	if err != nil {
		return err
	}
	defer file.Close()
	headers := map[string]string{"X-Agentrib-Lease": job.Lease, "X-Agentrib-Sha256": artifact.Digest}
	var uploaded protocol.Artifact
	err = r.request(ctx, http.MethodPut, "/runs/"+job.ID+"/artifacts/"+artifact.Name, "application/octet-stream", file, artifact.Bytes, headers, &uploaded)
	if err == nil && uploaded != artifact {
		err = errors.New("artifact confirmation does not match")
	}
	return err
}

func (r *Runner) Run(ctx context.Context) error {
	h := r.Detect(ctx)
	if len(h.Executors) == 0 && len(h.AgentStatuses) == 0 {
		return errors.New("no usable Agent or game engine command found")
	}
	slog.Info("starting runner", "executors", h.Executors, "projects", h.Projects)
	loop, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	connected := make(chan struct{})
	agentStatuses := make(chan map[string]protocol.AgentStatus, 1)
	go r.pollAgentStatuses(loop, h.AgentStatuses, agentStatuses)
	go r.heartbeats(loop, stop, h, agentStatuses, connected)
	select {
	case <-loop.Done():
		return context.Cause(loop)
	case <-connected:
	}
	for loop.Err() == nil {
		var job *protocol.Run
		err := r.post(loop, "/claim", struct{}{}, &job)
		if errors.Is(err, errToken) {
			return err
		}
		if err != nil && loop.Err() == nil {
			slog.Warn("claim failed", "error", err)
		}
		if job != nil && err == nil {
			r.runOnce(loop, *job)
		}
		sleep(loop, claimPeriod)
	}
	return context.Cause(loop)
}

func (r *Runner) heartbeats(ctx context.Context, stop context.CancelCauseFunc, heartbeat protocol.Heartbeat,
	agentStatuses <-chan map[string]protocol.AgentStatus, connected chan struct{}) {
	for ctx.Err() == nil {
		select {
		case heartbeat.AgentStatuses = <-agentStatuses:
		default:
		}
		r.updateProjects(&heartbeat)
		var reply protocol.HeartbeatReply
		err := r.post(ctx, "/heartbeat", heartbeat, &reply)
		if errors.Is(err, errToken) {
			stop(err)
			return
		}
		if err == nil && connected != nil {
			close(connected)
			connected = nil
			slog.Info("runner connected")
		}
		if err != nil && ctx.Err() == nil {
			slog.Warn("heartbeat failed", "error", err)
		}
		if err == nil {
			previous := heartbeat.ProjectResult
			heartbeat.ProjectResult = nil
			if heartbeat.CreateProjects && reply.Project != nil && runID.MatchString(reply.Project.Attempt) {
				if previous != nil && previous.Attempt == reply.Project.Attempt {
					heartbeat.ProjectResult = previous
					sleep(ctx, heartbeatPeriod)
					continue
				}
				heartbeat.ProjectResult = r.initializeProject(ctx, *reply.Project)
				continue
			}
		}
		sleep(ctx, heartbeatPeriod)
	}
}

func sleep(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

func (r *Runner) runOnce(ctx context.Context, job protocol.Run) {
	if !runID.MatchString(job.ID) || !runID.MatchString(job.Lease) {
		return
	}
	path := "/runs/" + job.ID
	startCtx, startCancel := context.WithTimeout(ctx, 18*time.Second)
	err := r.post(startCtx, path+"/start", map[string]string{"lease": job.Lease}, nil)
	for err != nil && !errors.Is(err, errLease) && !errors.Is(err, errToken) && startCtx.Err() == nil {
		sleep(startCtx, time.Second)
		err = r.post(startCtx, path+"/start", map[string]string{"lease": job.Lease}, nil)
	}
	startCancel()
	if err != nil {
		slog.Warn("run start not accepted", "run", job.ID, "error", err)
		return
	}
	logs := &eventBuffer{secret: r.cfg.token}
	duration := maxJobDuration
	if job.ExecutionID != "" {
		duration = workflowJobDuration
	}
	worker, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	finished := make(chan protocol.Result, 1)
	go func() {
		logs.add("status", "Runner started "+job.Spec.Executor)
		result, err := r.work(worker, job, logs.add)
		err = errors.Join(err, r.uploadArtifacts(worker, job, &result))
		switch {
		case errors.Is(worker.Err(), context.Canceled):
			result.Bugs, result.Verified = nil, nil
			result.Status, result.Detail = "canceled", "Process stopped"
		case errors.Is(worker.Err(), context.DeadlineExceeded):
			result.Bugs, result.Verified = nil, nil
			result.Status, result.Detail = "failed", "Execution timed out"
			logs.add("error", fmt.Sprintf("Execution timed out after %s", duration))
		case errors.Is(err, errQARejected):
			// Older control planes expect a failed transport result for QA tickets.
			result.Status, result.Detail = "failed", errQARejected.Error()
			logs.add("qa", result.Detail)
		case err != nil:
			result.Status, result.Detail = "failed", "Execution failed; see events"
			logs.add("error", err.Error())
		default:
			result.Status, result.Detail = "succeeded", "Execution completed"
		}
		if result.Status != "succeeded" {
			result.Reply = ""
		}
		finished <- result
	}()
	var result *protocol.Result
	lastContact := time.Now()
	for {
		select {
		case done := <-finished:
			result = &done
			finished = nil
		default:
		}
		events, rest := logs.batch()
		var final *protocol.Result
		if !rest {
			final = result
		}
		var reply protocol.RunReply
		if ctx.Err() != nil {
			cancel()
			if finished != nil {
				<-finished
			}
			return
		}
		err := r.post(ctx, path+"/report", protocol.Report{Lease: job.Lease, Events: events, Result: final}, &reply)
		if err == nil {
			lastContact = time.Now()
			logs.ack(len(events))
			if reply.Status == "canceling" {
				cancel()
			}
			if final != nil {
				slog.Info("run finished", "run", job.ID, "status", reply.Status)
				return
			}
		} else if errors.Is(err, errLease) || errors.Is(err, errToken) || time.Since(lastContact) > disconnectLimit {
			cancel()
			if finished != nil {
				<-finished
			}
			slog.Warn("run stopped after losing its lease or connection", "run", job.ID, "error", err)
			return
		}
		if !rest || err != nil {
			sleep(ctx, reportPeriod)
		}
	}
}

type eventBuffer struct {
	mu     sync.Mutex
	items  []protocol.Event
	seq    int
	secret string
}

func (b *eventBuffer) add(kind, message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.secret != "" {
		message = strings.ReplaceAll(message, b.secret, "[redacted]")
	}
	message = strings.TrimSpace(strings.ReplaceAll(strings.ToValidUTF8(message, "�"), "\x00", ""))
	for len(message) > 0 && b.seq < 10000 {
		end := min(len(message), 5500)
		for end < len(message) && !utf8.RuneStart(message[end]) {
			end--
		}
		line := strings.TrimSpace(message[:end])
		message = message[end:]
		if line != "" {
			b.seq++
			b.items = append(b.items, protocol.Event{Seq: b.seq, Kind: kind, Text: line})
		}
	}
}

func (b *eventBuffer) batch() ([]protocol.Event, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]protocol.Event(nil), b.items[:min(len(b.items), 24)]...), len(b.items) > 24
}

func (b *eventBuffer) ack(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.items = b.items[n:]
}
