package control

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/PhongFeng/Agentrib/internal/store"
)

var gameTemplate = protocol.Workflow{ID: "game-delivery", Name: "游戏制作与交付", Engine: protocol.DefaultEngine, Steps: []protocol.Step{
	{ID: "producer", Name: "完善需求并拆分任务", Role: "制作人 / 产品 Agent", DependsOn: []string{},
		Prompt: "以已确认的游戏简报和现有项目为依据，写入 docs/production/requirements.md，明确玩法、范围、约束和可验证的验收标准。同目录下分别写 art.md、audio.md、development.md，给各 Agent 说明任务、资源清单、格式、路径、接口和验收标准。约定美术与音效资源各自使用的目录，明确双方的集成接口。提交需求文档，并用简报中的明确决策约束方案。"},
	{ID: "art", Name: "制作美术资源", Role: "美术 Agent", DependsOn: []string{"producer"}, BaseFrom: "producer",
		Prompt: "阅读 docs/production/requirements.md 和 docs/production/art.md。制作任务要求的可用美术资源，按约定路径提交，并在 docs/production/art-handoff.md 记录文件、用途和接入说明。只修改本角色负责的资源和交接文档。"},
	{ID: "audio", Name: "制作音效资源", Role: "音效 Agent", DependsOn: []string{"producer"}, BaseFrom: "producer",
		Prompt: "阅读 docs/production/requirements.md 和 docs/production/audio.md。制作任务要求的可用音效和音乐，按约定路径提交，并在 docs/production/audio-handoff.md 记录文件、触发时机和接入说明。只修改本角色负责的资源和交接文档。"},
	{ID: "develop", Name: "开发玩法并接入资源", Role: "开发 Agent", DependsOn: []string{"art", "audio"}, BaseFrom: "art", MergeFrom: []string{"audio"}, Preflight: true,
		Prompt: "阅读 docs/production/requirements.md、development.md 和美术、音效交接文档。确认必要资源齐全，在合并后的版本接入真实资源并实现可玩的游戏。按验收标准补齐可运行的测试。Godot 项目需提交可用的 Web 导出预设，默认名为 Web，保留已有有效预设，并配置可运行的主场景。"},
	{ID: "ci", Name: "CI 构建与测试", Role: "构建与测试", Executor: "engine", DependsOn: []string{"develop"}, BaseFrom: "develop", ExportGame: true},
	{ID: "qa", Name: "验收游戏", Role: "QA Agent", DependsOn: []string{"ci"}, BaseFrom: "ci", ReadOnly: true, QAGate: true, QARework: true,
		Prompt: "依据需求文档和已确认的简报，对集成后的游戏逐条验收。核对玩法、美术与音效接入、引擎测试证据以及导出结果。记录每条标准的结论、证据、复现步骤及发现的问题。仅在全部必要标准通过时判定可交付，缺少证据的必要标准标为未验证。"},
	{ID: "release", Name: "通知交付", Role: "制作工作台", Executor: "notify", DependsOn: []string{"qa"}},
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
		agent, usesAgent := protocol.FindAgent(step.Executor)
		if step.Executor != "approval" && step.Executor != "notify" && step.Executor != "engine" && !usesAgent && step.Executor != engine.ID {
			return fmt.Errorf("Unknown executor for %s", step.ID)
		}
		if len(step.Prompt) > 3000 || strings.ContainsRune(step.Prompt, 0) ||
			usesAgent && strings.TrimSpace(step.Prompt) == "" {
			return fmt.Errorf("Invalid Agent instructions for %s", step.ID)
		}
		if step.ReadOnly && !agent.ReadOnly {
			return fmt.Errorf("Selected executor does not support read-only step %s", step.ID)
		}
		builds := step.Executor == "engine" || step.Executor == engine.ID
		if step.QAGate && !step.ReadOnly && !step.QARework || step.ExportGame && !builds ||
			step.Preflight && (!usesAgent || step.ReadOnly || step.QAGate) || step.QARework && (!usesAgent || !step.QAGate) {
			return fmt.Errorf("Invalid QA or export setting for %s", step.ID)
		}
		if _, ok := protocol.FindAgent(step.CIRepairAgent); step.CIRepairAgent != "" && (!builds || !ok) {
			return fmt.Errorf("请选择有效的 CI 失败处理 Agent (%s)", step.ID)
		}
		steps[step.ID] = step
	}
	reviews := make(map[string]bool)
	for _, step := range workflow.Steps {
		deps := make(map[string]bool)
		for _, id := range step.DependsOn {
			if id == step.ID || deps[id] || steps[id].ID == "" {
				return fmt.Errorf("Invalid dependency for %s", step.ID)
			}
			deps[id] = true
		}
		if len(step.MergeFrom) > 0 && step.BaseFrom == "" {
			return fmt.Errorf("Choose a primary commit source before merging inputs for %s", step.ID)
		}
		sources := map[string]bool{}
		for _, source := range append([]string{step.BaseFrom}, step.MergeFrom...) {
			if source == "" && step.BaseFrom == "" {
				continue
			}
			upstream := steps[source].Executor
			if !deps[source] || upstream == "approval" || upstream == "notify" || sources[source] ||
				step.Executor == "approval" || step.Executor == "notify" {
				return fmt.Errorf("Commit source for %s must be a unique direct executable dependency", step.ID)
			}
			sources[source] = true
		}
		if step.CIRepairAgent != "" && !steps[step.BaseFrom].Preflight {
			return fmt.Errorf("CI 的提交来源必须开启开发交付检查 (%s)", step.ID)
		}
		if step.QARework {
			ci := steps[step.BaseFrom]
			if (ci.Executor != "engine" && ci.Executor != engine.ID) || !steps[ci.BaseFrom].Preflight || len(step.MergeFrom) != 0 || reviews[ci.ID] {
				return fmt.Errorf("自动返工需要一个独立的 CI 提交来源，其开发步骤须开启交付检查 (%s)", step.ID)
			}
			reviews[ci.ID] = true
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
	project := r.URL.Query().Get("project")
	if project != "" && !slugPattern.MatchString(project) {
		problem(w, http.StatusBadRequest, "Invalid project")
		return
	}
	list, err := a.store.Executions(r.Context(), project)
	response(w, http.StatusOK, list, err)
}

func (a *API) startExecution(w http.ResponseWriter, r *http.Request) {
	var input protocol.ExecutionInput
	if !read(w, r, &input) {
		return
	}
	if !idPattern.MatchString(input.ID) || !slugPattern.MatchString(input.WorkflowID) || input.Version < 1 ||
		!idPattern.MatchString(input.MachineID) || !slugPattern.MatchString(input.Project) || !validText(input.Brief, protocol.MaxBriefBytes) ||
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

func (a *API) continueExecution(w http.ResponseWriter, r *http.Request) {
	execution, err := a.store.ContinueExecution(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrConflict) {
		problem(w, http.StatusConflict, "此运行没有可重试的步骤，请刷新后查看最新状态。")
		return
	}
	response(w, http.StatusOK, execution, err)
}

func (a *API) decideQA(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action      string                         `json:"action"`
		Assignments map[string]protocol.Assignment `json:"assignments,omitempty"`
		Note        string                         `json:"note,omitempty"`
	}
	if !read(w, r, &input) {
		return
	}
	if len(input.Note) > 3000 || strings.ContainsRune(input.Note, 0) {
		problem(w, http.StatusBadRequest, "产品说明过长或包含无效字符")
		return
	}
	err := a.store.DecideQA(r.Context(), r.PathValue("id"), r.PathValue("run"), input.Action, protocol.QADecision{Assignments: input.Assignments, Note: input.Note})
	if errors.Is(err, store.ErrConflict) {
		problem(w, http.StatusConflict, "缺陷状态已变化或分派无效，请刷新并确认当前分工")
		return
	}
	response(w, http.StatusOK, map[string]string{"status": "accepted"}, err)
}

func (a *API) approve(w http.ResponseWriter, r *http.Request) {
	err := a.store.Approve(r.Context(), r.PathValue("id"), r.PathValue("step"))
	response(w, http.StatusOK, map[string]string{"status": "approved"}, err)
}
