package control

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

var gameTemplate = protocol.Workflow{ID: "game-delivery", Name: "游戏制作与交付", Engine: protocol.DefaultEngine, Steps: []protocol.Step{
	{ID: "brief", Name: "确认游戏简报", Role: "策划 / 制作人", Executor: "approval", DependsOn: []string{}},
	{ID: "ui", Name: "生成游戏界面", Role: "界面设计", Executor: "minimax-code", DependsOn: []string{"brief"},
		Prompt: "Implement the agreed UI in reusable components, scenes, themes and assets for the selected engine. Include UI states and responsive layouts as required by the brief. Record file paths and integration notes for the development step. Keep gameplay rules and level data separate."},
	{ID: "build", Name: "集成界面与开发玩法", Role: "玩法程序", Executor: "codex", DependsOn: []string{"ui"}, BaseFrom: "ui",
		Prompt: "Start from the UI commit and read its integration notes. Wire the UI to the agreed gameplay, keep level data separate, and document how to test the result with the selected engine."},
	{ID: "tests", Name: "运行引擎测试并导出", Role: "构建与测试", Executor: "engine", DependsOn: []string{"build"}, BaseFrom: "build"},
	{ID: "qa", Name: "独立验收", Role: "QA 负责人", Executor: "qoder-cn", DependsOn: []string{"tests"}, BaseFrom: "tests", ReadOnly: true,
		Prompt: "Compare the UI integration, gameplay in the exact checked-out commit and engine test evidence to the brief. Report pass, fail, or unverified for each criterion, with reproduction steps and a release recommendation. Review source and supplied test evidence with read-only tools."},
	{ID: "release", Name: "确认交付版本", Role: "制作人 / 操作者", Executor: "approval", DependsOn: []string{"qa"}},
}}

func (a *API) template(w http.ResponseWriter, _ *http.Request) {
	send(w, http.StatusOK, gameTemplate)
}

func (a *API) workflows(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.Workflows(r.Context())
	response(w, http.StatusOK, list, err)
}

func (a *API) saveWorkflow(w http.ResponseWriter, r *http.Request) {
	var spec protocol.Workflow
	if !read(w, r, &spec) {
		return
	}
	if err := validateWorkflow(spec); err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	workflow, err := a.store.SaveWorkflow(r.Context(), spec)
	response(w, http.StatusCreated, workflow, err)
}

func validateWorkflow(workflow protocol.Workflow) error {
	if !slugPattern.MatchString(workflow.ID) || !validText(workflow.Name, 120) || workflow.Version < 0 || len(workflow.Steps) < 1 || len(workflow.Steps) > 24 {
		return errors.New("Provide a workflow ID, name, and 1–24 steps")
	}
	engine, known := protocol.FindEngine(protocol.EngineID(workflow.Engine))
	if !known {
		return errors.New("Unknown workflow engine")
	}
	steps := make(map[string]protocol.Step)
	for _, step := range workflow.Steps {
		if !slugPattern.MatchString(step.ID) || steps[step.ID].ID != "" || !validText(step.Name, 160) || !validText(step.Role, 100) {
			return errors.New("Each step needs a unique ID, name, and role")
		}
		if step.Executor != "approval" && step.Executor != "engine" && !agentExecutor(step.Executor) && step.Executor != engine.ID {
			return fmt.Errorf("Unknown executor for %s", step.ID)
		}
		if len(step.Prompt) > 3000 || strings.ContainsRune(step.Prompt, 0) ||
			agentExecutor(step.Executor) && strings.TrimSpace(step.Prompt) == "" {
			return fmt.Errorf("Invalid Agent instructions for %s", step.ID)
		}
		if step.Executor == "minimax-code" && step.ReadOnly {
			return fmt.Errorf("MiniMax Code does not support read-only step %s", step.ID)
		}
		steps[step.ID] = step
	}
	for _, step := range workflow.Steps {
		deps := make(map[string]bool)
		for _, id := range step.DependsOn {
			if id == step.ID || deps[id] || steps[id].ID == "" {
				return fmt.Errorf("Invalid dependency for %s", step.ID)
			}
			deps[id] = true
		}
		if step.BaseFrom != "" && (!deps[step.BaseFrom] || steps[step.BaseFrom].Executor == "approval") {
			return fmt.Errorf("Commit source for %s must be a direct executable dependency", step.ID)
		}
	}
	resolved := make(map[string]bool)
	for len(resolved) < len(steps) {
		before := len(resolved)
		for _, step := range workflow.Steps {
			ready := true
			for _, dependency := range step.DependsOn {
				ready = ready && resolved[dependency]
			}
			if ready {
				resolved[step.ID] = true
			}
		}
		if len(resolved) == before {
			return errors.New("Steps form a dependency cycle")
		}
	}
	return nil
}

func (a *API) executions(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.Executions(r.Context())
	response(w, http.StatusOK, list, err)
}

func (a *API) startExecution(w http.ResponseWriter, r *http.Request) {
	var input protocol.ExecutionInput
	if !read(w, r, &input) {
		return
	}
	if !idPattern.MatchString(input.ID) || !slugPattern.MatchString(input.WorkflowID) || input.Version < 1 ||
		!idPattern.MatchString(input.MachineID) || !slugPattern.MatchString(input.Project) || !validText(input.Brief, 8000) ||
		input.Commit != "" && !commitPattern.MatchString(input.Commit) {
		problem(w, http.StatusBadRequest, "Invalid execution input")
		return
	}
	execution, err := a.store.StartExecution(r.Context(), input)
	response(w, http.StatusCreated, execution, err)
}

func (a *API) execution(w http.ResponseWriter, r *http.Request) {
	execution, err := a.store.Execution(r.Context(), r.PathValue("id"))
	response(w, http.StatusOK, execution, err)
}

func (a *API) approve(w http.ResponseWriter, r *http.Request) {
	err := a.store.Approve(r.Context(), r.PathValue("id"), r.PathValue("step"))
	response(w, http.StatusOK, map[string]string{"status": "approved"}, err)
}
