package runner

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func objectID(s string) bool {
	if len(s) != 40 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func git(ctx context.Context, path string, args ...string) (string, error) {
	out, err := command(ctx, path, "git", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func executionBase(ctx context.Context, repository, execution string) (string, error) {
	if !runID.MatchString(execution) {
		return "", errors.New("invalid workflow execution ID")
	}
	ref := "refs/agentrib/executions/" + execution + "/base"
	base, err := git(ctx, repository, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err == nil {
		return base, nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return "", err
	}
	base, err = git(ctx, repository, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	if !objectID(base) {
		return "", errors.New("Git project must use SHA-1 object IDs")
	}
	// Create the ref only if absent so concurrent steps agree on one starting commit.
	if _, err := git(ctx, repository, "update-ref", ref, base, strings.Repeat("0", 40)); err != nil {
		if existing, lookupErr := git(ctx, repository, "rev-parse", "--verify", ref+"^{commit}"); lookupErr == nil {
			return existing, nil
		}
		return "", err
	}
	return base, nil
}

func (r *Runner) work(ctx context.Context, run protocol.Run, emit Emit) (protocol.Result, error) {
	var result protocol.Result
	if run.Spec.AgentCheck {
		return result, r.checkAgent(ctx, run.Spec.Executor)
	}
	project, exists := r.cfg.Projects[run.Spec.Project]
	if !exists {
		project, exists = r.managedProject(run.Spec.Project)
	}
	if !exists {
		return result, errors.New("project Git workspace is unavailable on this Runner")
	}
	engineID := protocol.EngineID(run.Spec.Engine)
	engine, known := protocol.FindEngine(engineID)
	adapter := r.engines[engineID]
	if project.Engine != engineID || !known || adapter == nil {
		return result, errors.New("selected engine does not match this Runner project")
	}
	engineBuild := run.Spec.Executor == engineID
	brief := run.Spec.BriefMode != ""
	if (brief || run.Spec.QAGate && !run.Spec.QARework) && (!run.Spec.ReadOnly || engineBuild) {
		return result, errors.New("brief and QA require a read-only Agent")
	}
	checkout := filepath.Join(r.cfg.State, "worktrees", run.ID)
	if err := os.MkdirAll(filepath.Dir(checkout), 0700); err != nil {
		return result, err
	}
	base := run.Spec.Commit
	switch {
	case base != "":
		if !objectID(base) {
			return result, errors.New("invalid Git commit")
		}
	case run.ExecutionID != "":
		var err error
		base, err = executionBase(ctx, project.Repository, run.ExecutionID)
		if err != nil {
			return result, fmt.Errorf("record workflow starting commit: %w", err)
		}
	case !brief && (run.Spec.ReadOnly || engineBuild):
		return result, errors.New("verification requires the development commit")
	default:
		base = "HEAD"
	}
	base, err := git(ctx, project.Repository, "rev-parse", "--verify", base+"^{commit}")
	if err != nil {
		return result, fmt.Errorf("resolve base commit: %w", err)
	}
	if !objectID(base) {
		return result, errors.New("Git project must use SHA-1 object IDs")
	}
	if _, err := git(ctx, project.Repository, "worktree", "add", "--detach", checkout, base); err != nil {
		return result, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if _, err := git(cleanup, project.Repository, "worktree", "remove", "--force", checkout); err != nil {
			emit("diagnostic", "Temporary worktree cleanup failed")
		}
	}()
	if len(run.Spec.MergeCommits) > 0 {
		base, err = mergeWorkflowInputs(ctx, checkout, run.Spec.MergeCommits, emit)
		if err != nil {
			return result, err
		}
		if _, err := git(ctx, project.Repository, "update-ref", "refs/agentrib/runs/"+run.ID, base); err != nil {
			return result, err
		}
	}
	dir, err := contained(checkout, filepath.Join(checkout, project.EngineDir))
	if err != nil {
		return result, err
	}
	if err := adapter.ValidateDirectory(checkout, dir); err != nil {
		return result, err
	}
	emit("status", "Checked out "+base)
	result.Commit = base
	if engineBuild {
		if run.Spec.ExportGame && project.WebPreset == "" {
			project.WebPreset = "Web"
		}
		result.Artifacts, err = adapter.Build(ctx, dir, filepath.Join(r.cfg.State, "artifacts", run.ID), project, emit)
		return result, err
	}
	backend := r.backends[run.Spec.Executor]
	if backend == nil {
		return result, errors.New("Agent CLI is unavailable on this machine")
	}
	if qoder, ok := backend.(QoderCN); ok {
		if godot, ok := adapter.(godotEngine); ok {
			qoder.godot = godot.command
			backend = qoder
		}
	}
	var finalReply string
	if run.IsProductTriage() {
		answer, err := r.finalAgentReply(ctx, backend, dir, run.Spec.Prompt, run.Spec.ReadOnly, protocol.MaxBriefBytes, emit)
		if err != nil {
			return result, err
		}
		// Only the routing decision leaves this disposable checkout.
		result.Reply = answer
		return result, nil
	}
	if brief {
		finalReply, err = r.finalAgentReply(ctx, backend, dir, "游戏引擎为 "+engine.Name+"。\n\n"+run.Spec.Prompt, true, protocol.MaxBriefBytes, emit)
	} else {
		mode := "Keep all edits in this worktree. The Runner records the final changes as a local Git commit."
		if run.Spec.ReadOnly {
			mode = "Review the checked-out version. Do not change source files. Report findings and the evidence you used."
		} else if run.Spec.QARework {
			mode = "Review the exact CI revision in a disposable worktree. Temporary untracked test files are allowed. Do not modify tracked files or make commits."
		}
		prompt := "This is an Agentrib game task. Selected engine: " + engine.Name + ". " + mode + " Work independently. Do not delegate, push, or publish.\n\n" + run.Spec.Prompt
		if run.Spec.QAGate {
			if run.Spec.WebBuild != nil {
				browser, err := r.prepareQABrowser(ctx, *run.Spec.WebBuild)
				if err != nil {
					emit("diagnostic", "QA 浏览器准备失败："+err.Error())
					prompt += "\n\nRunner 未能准备 CI 原包的测试浏览器：" + err.Error() + "。根据实际证据报告网页验收状态。"
				} else {
					defer browser.Close()
					emit("status", "Runner 已在测试浏览器启动原始 CI 网页包")
					prompt += browser.evidence()
					if codex, ok := backend.(Codex); ok {
						if options, err := browser.codexTools(ctx); err != nil {
							emit("diagnostic", "QA 浏览器工具不可用："+err.Error())
						} else {
							codex.browser = options
							backend = codex
							prompt += "\n使用 MCP 工具 agentrib_game_qa 的 game_browser 操作已启动的浏览器。先查看截图，再按坐标点击或发送按键，每次操作返回当前画面。"
						}
					}
				}
			}
			prompt += "\n\n验收报告的首行必须单独写 QA: PASS 或 QA: FAIL。全部必要标准均有证据且通过时使用 QA: PASS。否则使用 QA: FAIL。正文写明每项结论与证据。"
			if run.Spec.QARework {
				prompt += qaReworkInstructions
			}
			finalReply, err = r.finalAgentReply(ctx, backend, dir, prompt, run.Spec.ReadOnly, maxQAReplyBytes, emit)
		} else {
			err = backend.Execute(ctx, dir, prompt, run.Spec.ReadOnly, emit)
		}
	}
	if err != nil {
		return result, err
	}
	if run.Spec.QARework {
		head, err := git(ctx, checkout, "rev-parse", "HEAD")
		if err != nil {
			return result, err
		}
		statusArgs := []string{"status", "--porcelain"}
		if !run.Spec.ReadOnly {
			statusArgs = append(statusArgs, "--untracked-files=no")
		}
		changes, err := git(ctx, checkout, statusArgs...)
		if err != nil {
			return result, err
		}
		if head != base || changes != "" {
			return result, errors.New("QA 修改了待验收版本")
		}
		return reviewRevision(result, finalReply, emit)
	}
	if run.Spec.Preflight {
		if err := adapter.Check(ctx, dir, emit); err != nil {
			return result, fmt.Errorf("开发交付检查未通过: %w", err)
		}
		emit("status", "开发交付检查已通过")
	}
	changes, err := git(ctx, checkout, "status", "--porcelain")
	if err != nil {
		return result, err
	}
	if run.Spec.ReadOnly {
		head, err := git(ctx, checkout, "rev-parse", "HEAD")
		if err != nil {
			return result, err
		}
		if changes != "" || head != base {
			return result, errors.New("read-only step wrote to the checkout")
		}
		if brief {
			result.Commit = ""
			result.Reply = finalReply
		}
		if run.Spec.QAGate {
			emit("qa", finalReply)
			verdict, _, _ := strings.Cut(finalReply, "\n")
			if strings.TrimSpace(verdict) != "QA: PASS" {
				return result, errors.New("QA 验收未通过，请查看报告")
			}
		}
		return result, nil
	}
	if changes != "" {
		if _, err := git(ctx, checkout, "add", "--all"); err != nil {
			return result, err
		}
		if _, err := git(ctx, checkout, "-c", "user.name=Agentrib", "-c", "user.email=agentrib@localhost",
			"-c", "commit.gpgsign=false", "commit", "-m", "agentrib: "+run.Spec.Title); err != nil {
			return result, err
		}
	}
	commit, err := git(ctx, checkout, "rev-parse", "HEAD")
	if err != nil {
		return result, err
	}
	if _, err := git(ctx, project.Repository, "update-ref", "refs/agentrib/runs/"+run.ID, commit); err != nil {
		return result, err
	}
	result.Commit = commit
	if run.ExecutionID != "" {
		result.Artifacts, err = capturePreviews(ctx, checkout, dir, commit, run.StepID, filepath.Join(r.cfg.State, "artifacts", run.ID))
		if err != nil {
			emit("diagnostic", "未能整理部分产出预览："+err.Error())
		}
	}
	return result, nil
}

func mergeWorkflowInputs(ctx context.Context, checkout string, commits []string, emit Emit) (string, error) {
	for _, commit := range commits {
		if !objectID(commit) {
			return "", errors.New("invalid workflow input commit")
		}
		if _, err := git(ctx, checkout, "rev-parse", "--verify", commit+"^{commit}"); err != nil {
			return "", err
		}
		emit("status", "Merging upstream commit "+commit)
		if _, err := git(ctx, checkout, "-c", "user.name=Agentrib", "-c", "user.email=agentrib@localhost",
			"-c", "commit.gpgsign=false", "merge", "--no-edit", commit); err != nil {
			return "", fmt.Errorf("merge workflow input %s: %w", commit, err)
		}
	}
	return git(ctx, checkout, "rev-parse", "HEAD")
}

func (g godotEngine) Build(ctx context.Context, dir, out string, project Project, emit Emit) ([]protocol.Artifact, error) {
	if g.command == "" {
		return nil, errors.New("Godot executable unavailable")
	}
	if _, err := contained(dir, filepath.Join(dir, project.TestScript)); err != nil {
		return nil, fmt.Errorf("test script: %w", err)
	}
	if err := os.MkdirAll(out, 0700); err != nil {
		return nil, err
	}
	logPath := filepath.Join(out, "godot.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	execGodot := func(args ...string) error {
		return g.run(ctx, dir, func(kind, text string) {
			if kind != "tool" {
				_, _ = fmt.Fprintln(log, text)
			}
			emit(kind, text)
		}, args...)
	}
	checkErr := execGodot("--import")
	if checkErr == nil {
		checkErr = execGodot("--script", "res://"+filepath.ToSlash(project.TestScript))
	}
	files := []string{logPath}
	if checkErr == nil && project.WebPreset != "" {
		web := filepath.Join(out, "web")
		if err := os.Mkdir(web, 0700); err != nil {
			return nil, err
		}
		checkErr = execGodot("--export-release", project.WebPreset, filepath.Join(web, "index.html"))
		if checkErr == nil {
			bundle := filepath.Join(out, protocol.WebBuildArchive)
			checkErr = zipExport(web, bundle)
			if checkErr == nil {
				files = append(files, bundle)
			}
		}
	}
	if err := log.Close(); err != nil {
		return nil, err
	}
	var list []protocol.Artifact
	for _, file := range files {
		artifact, err := identify(file)
		if err != nil {
			return list, err
		}
		if artifact.Bytes > 0 {
			list = append(list, artifact)
		}
	}
	return list, checkErr
}

func identify(path string) (protocol.Artifact, error) {
	file, err := os.Open(path)
	if err != nil {
		return protocol.Artifact{}, err
	}
	defer file.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	return protocol.Artifact{Name: filepath.Base(path), Digest: hex.EncodeToString(digest.Sum(nil)), Bytes: size}, err
}

func zipExport(root, target string) error {
	if info, err := os.Stat(filepath.Join(root, "index.html")); err != nil || info.Size() == 0 {
		return errors.New("Godot export did not produce index.html")
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(file)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		writer, err := archive.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(writer, input)
		return errors.Join(err, input.Close())
	})
	return errors.Join(err, archive.Close(), file.Close())
}
