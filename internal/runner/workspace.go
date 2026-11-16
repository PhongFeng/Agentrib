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

func (r *Runner) work(ctx context.Context, run protocol.Run, emit Emit) (protocol.Result, error) {
	var result protocol.Result
	project, exists := r.cfg.Projects[run.Spec.Project]
	if !exists {
		return result, errors.New("project is missing from the local runner configuration")
	}
	checkout := filepath.Join(r.cfg.State, "worktrees", run.ID)
	if err := os.MkdirAll(filepath.Dir(checkout), 0700); err != nil {
		return result, err
	}
	base := run.Spec.Commit
	if base == "" {
		if run.Spec.ReadOnly || run.Spec.Executor == "godot" {
			return result, errors.New("verification requires the development commit")
		}
		base = "HEAD"
	} else if !objectID(base) {
		return result, errors.New("invalid Git commit")
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
	dir, err := contained(checkout, filepath.Join(checkout, project.GodotDir))
	if err != nil {
		return result, err
	}
	marker, err := contained(checkout, filepath.Join(dir, "project.godot"))
	if err != nil {
		return result, errors.New("Git checkout must contain a Godot project.godot")
	}
	info, err := os.Stat(marker)
	if err != nil || !info.Mode().IsRegular() {
		return result, errors.New("project.godot must be a regular file")
	}
	emit("status", "Checked out "+base)
	result.Commit = base
	if run.Spec.Executor == "godot" {
		result.Artifacts, err = r.godot(ctx, run.ID, dir, project, emit)
		return result, err
	}
	backend := r.backends[run.Spec.Executor]
	if backend == nil {
		return result, errors.New("Agent CLI is unavailable on this machine")
	}
	mode := "Keep all edits in this worktree. The Runner records the final changes as a local Git commit."
	if run.Spec.ReadOnly {
		mode = "Review the checked-out version. Do not change source files. Report findings and the evidence you used."
	}
	prompt := "This is an Agentrib Godot game task. " + mode + " Work independently. Do not delegate, push, or publish.\n\n" + run.Spec.Prompt
	if err := backend.Execute(ctx, dir, prompt, run.Spec.ReadOnly, emit); err != nil {
		return result, err
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
	return result, nil
}

func (r *Runner) godot(ctx context.Context, id, dir string, project Project, emit Emit) ([]protocol.Artifact, error) {
	if r.godotCommand == "" {
		return nil, errors.New("Godot executable unavailable")
	}
	if _, err := contained(dir, filepath.Join(dir, project.TestScript)); err != nil {
		return nil, fmt.Errorf("test script: %w", err)
	}
	out := filepath.Join(r.cfg.State, "artifacts", id)
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
		emit("tool", "godot "+strings.Join(args, " "))
		cmd := command(ctx, dir, r.godotCommand, append([]string{"--headless", "--path", dir}, args...)...)
		var scriptError bool
		lines := &lineWriter{writeLine: func(s string) {
			_, _ = fmt.Fprintln(log, s)
			emit("godot", s)
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
			bundle := filepath.Join(out, "godot-web.zip")
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
