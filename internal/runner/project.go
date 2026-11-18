package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

const projectMarker = ".git/agentrib-managed"
const starterTest = "tests/smoke.gd"

func (r *Runner) managedProject(key string) (Project, bool) {
	if !projectKey.MatchString(key) {
		return Project{}, false
	}
	repo := filepath.Join(r.cfg.State, "projects", key)
	info, err := os.Lstat(repo)
	if err != nil || !info.IsDir() {
		return Project{}, false
	}
	marker, err := contained(repo, filepath.Join(repo, projectMarker))
	if err != nil {
		return Project{}, false
	}
	markerInfo, err := os.Stat(marker)
	if err != nil || !markerInfo.Mode().IsRegular() {
		return Project{}, false
	}
	return Project{Repository: repo, Engine: protocol.DefaultEngine, TestScript: starterTest}, true
}

func (r *Runner) updateProjects(heartbeat *protocol.Heartbeat) {
	heartbeat.Projects = []string{}
	heartbeat.ProjectEngines = make(map[string]string)
	heartbeat.ProjectWorkspaces = make(map[string]string)
	add := func(key string, project Project) {
		heartbeat.Projects = append(heartbeat.Projects, key)
		heartbeat.ProjectEngines[key] = project.Engine
		heartbeat.ProjectWorkspaces[key] = project.Repository
	}
	for key, project := range r.cfg.Projects {
		add(key, project)
	}
	dirs, _ := os.ReadDir(filepath.Join(r.cfg.State, "projects"))
	for _, dir := range dirs {
		if len(heartbeat.Projects) >= protocol.MaxProjects {
			break
		}
		if _, configured := r.cfg.Projects[dir.Name()]; configured {
			continue
		}
		if dir.IsDir() {
			if project, ok := r.managedProject(dir.Name()); ok {
				add(dir.Name(), project)
			}
		}
	}
	sort.Strings(heartbeat.Projects)
}

func (r *Runner) initializeProject(ctx context.Context, request protocol.ProjectRequest) *protocol.ProjectResult {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result := &protocol.ProjectResult{Attempt: request.Attempt}
	if err := r.provisionProject(ctx, request.ProjectInput); err != nil {
		slog.Warn("project initialization failed", "project", request.ID, "error", err)
		result.Error = "Git 工作区创建失败，请查看 Runner 日志后重试。"
	}
	return result
}

func (r *Runner) provisionProject(ctx context.Context, project protocol.ProjectInput) error {
	if !projectKey.MatchString(project.ID) || project.Engine != protocol.DefaultEngine {
		return errors.New("unsupported project ID or engine")
	}
	if _, exists := r.cfg.Projects[project.ID]; exists {
		return errors.New("project ID is already configured on this Runner")
	}
	if existing, ok := r.managedProject(project.ID); ok {
		// A previous attempt may have published the repository before its heartbeat was acknowledged.
		_, err := git(ctx, existing.Repository, "rev-parse", "--verify", "HEAD^{commit}")
		return err
	}
	var inventory protocol.Heartbeat
	r.updateProjects(&inventory)
	if len(inventory.Projects) >= protocol.MaxProjects {
		return errors.New("Runner project limit reached")
	}
	root := filepath.Join(r.cfg.State, "projects")
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	target := filepath.Join(root, project.ID)
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("project workspace path is already in use: %s", target)
	}
	stage, err := os.MkdirTemp(root, ".init-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if _, err := git(ctx, stage, "-c", "init.defaultBranch=main", "init", "--template=", "--object-format=sha1"); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(stage, "tests"), 0700); err != nil {
		return err
	}
	// Godot uses quoted string values in project.godot. Keep its title on one line.
	title := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\r", " ", "\n", " ", "\t", " ").Replace(project.Name)
	files := map[string]string{
		".gitignore":    "/.godot/\n",
		"project.godot": "config_version=5\n\n[application]\n\nconfig/name=\"" + title + "\"\n\n[rendering]\n\nrenderer/rendering_method=\"gl_compatibility\"\n",
		starterTest:     "extends SceneTree\n\nfunc _initialize() -> void:\n\tquit(0 if ProjectSettings.get_setting(\"application/config/name\", \"\") != \"\" else 1)\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(stage, name), []byte(content), 0600); err != nil {
			return err
		}
	}
	if _, err := git(ctx, stage, "add", "-f", "--", ".gitignore", "project.godot", starterTest); err != nil {
		return err
	}
	if _, err := git(ctx, stage, "-c", "user.name=Agentrib", "-c", "user.email=agentrib@localhost", "-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=.git/hooks", "commit", "-m", "Initialize Godot project"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, projectMarker), nil, 0600); err != nil {
		return err
	}
	// Publish the workspace only after Git has a valid initial commit.
	return os.Rename(stage, target)
}
