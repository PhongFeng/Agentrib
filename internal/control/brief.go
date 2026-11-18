package control

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

var briefSkillPattern = regexp.MustCompile(`^\$?[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func (a *API) createBriefTurn(w http.ResponseWriter, r *http.Request) {
	var input protocol.BriefTurn
	if !read(w, r, &input) {
		return
	}
	agent, known := protocol.FindAgent(input.Executor)
	if !idPattern.MatchString(input.ID) || !idPattern.MatchString(input.MachineID) || !slugPattern.MatchString(input.Project) ||
		!known || !agent.ReadOnly || input.Mode != "discussion" && input.Mode != "draft" {
		problem(w, http.StatusBadRequest, "请选择支持只读执行的产品 Agent 和游戏项目")
		return
	}
	if len(input.Skills) > 8 || len(input.Skills) > 0 && agent.ID != "codex" {
		problem(w, http.StatusBadRequest, "Codex 每次最多可指定 8 个技能")
		return
	}
	var prompt strings.Builder
	prompt.WriteString("你是这个游戏项目的产品策划。以用户的语言回复。项目代码若有助于理解现状，可从当前 Git 工作区读取。讨论和撰写简报期间保持项目只读。")
	if input.Mode == "draft" {
		prompt.WriteString("\n根据对话生成一份可直接交给后续 Agent 的 Markdown 游戏简报。包含目标与目标玩家、核心玩法或本次改动、范围与约束、可执行的验收标准。区分已确认内容与待定事项。只输出简报正文，总长度控制在 1800 个汉字以内。")
	} else {
		prompt.WriteString("\n回答最后一条用户消息。帮用户梳理目标、玩法、范围及验收标准。缺少关键信息时，每次问一两个最影响方案的问题。回复简洁。")
	}
	for _, skill := range input.Skills {
		if !briefSkillPattern.MatchString(skill) {
			problem(w, http.StatusBadRequest, "请输入已安装技能的名称")
			return
		}
		prompt.WriteString("\n请使用 $" + strings.TrimPrefix(skill, "$") + " 技能。若未找到，请在回复中说明。")
	}
	if len(input.Messages) == 0 || len(input.Messages) > 60 || input.Messages[0].Role != "user" ||
		input.Mode == "discussion" && input.Messages[len(input.Messages)-1].Role != "user" {
		problem(w, http.StatusBadRequest, "请先输入需求。每段对话最多支持 60 条消息")
		return
	}
	total := 0
	for _, message := range input.Messages {
		if message.Role != "user" && message.Role != "assistant" || !validText(message.Text, protocol.MaxBriefBytes) {
			problem(w, http.StatusBadRequest, "对话消息内容无效或过长")
			return
		}
		total += len(message.Text)
	}
	if total > 96<<10 {
		problem(w, http.StatusBadRequest, "对话过长，请开启新对话")
		return
	}
	history, err := json.Marshal(input.Messages)
	if err != nil {
		response(w, 0, nil, err)
		return
	}
	prompt.WriteString("\n\n对话记录（按时间顺序）\n")
	prompt.Write(history)
	run, err := a.store.CreateBriefRun(r.Context(), input.ID, protocol.Spec{
		MachineID: input.MachineID, Project: input.Project, Executor: agent.ID,
		Title: "产品 Agent · 游戏简报", Prompt: prompt.String(), ReadOnly: true, BriefMode: input.Mode,
	})
	response(w, http.StatusAccepted, run, err)
}
