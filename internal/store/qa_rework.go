package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/jackc/pgx/v5"
)

const automaticQARounds = 2

func workflowStep(workflow protocol.Workflow, id string) protocol.Step {
	for _, step := range workflow.Steps {
		if step.ID == id {
			return step
		}
	}
	return protocol.Step{}
}

type qaPlan struct {
	protocol.QAReview
	stepID string
	spec   protocol.Spec
	build  protocol.Run
}

func (p *qaPlan) state(phase, reason string) qaPlan {
	p.Phase, p.Reason = phase, reason
	return *p
}

func pending(run protocol.Run) bool {
	switch run.Status {
	case "claimed", "queued", "running", "canceling":
		return true
	}
	return false
}

func waitingForQA(phase string) bool {
	return phase == "product_review" || phase == "environment"
}

func accountableBugs(bugs []protocol.Bug) []protocol.Bug {
	var work []protocol.Bug
	for _, bug := range bugs {
		if bug.Owner != "environment" {
			work = append(work, bug)
		}
	}
	return work
}

func rejectedQA(run protocol.Run) bool {
	return run.Status == "rejected" && run.Result != nil && protocol.ValidBugs(run.Result.Bugs)
}

// A retry of the same build can be interrupted even though QA already
// recorded a usable rejection. Keep the report selected on manual continuation
// until a fresh QA attempt replaces that interrupted attempt.
func qaReport(e protocol.Execution, latest, ci protocol.Run, manual bool) protocol.Run {
	if !interrupted(latest.Status) || rejectedQA(latest) {
		return latest
	}
	var report protocol.Run
	for _, run := range e.Runs {
		if run.StepID == latest.StepID && run.Attempt < latest.Attempt &&
			run.Attempt > report.Attempt && run.Spec.Commit == latest.Spec.Commit && rejectedQA(run) {
			report = run
		}
	}
	if report.ID == "" {
		return latest
	}
	d := e.Decisions[report.ID]
	if d.ResumeAfter == latest.ID {
		return report
	}
	// A rebuilt revision or an explicitly requested recheck should resume
	// verification on that revision.
	if manual && ci.Spec.RecheckOf != report.ID && !d.Recheck && !d.EnvironmentReady {
		return report
	}
	return latest
}

// The scheduler, operator actions and console all use the same decision for a
// specific QA attempt. Only the scheduler creates jobs, under the execution lock.
func planQA(e protocol.Execution, step protocol.Step, runs map[string]protocol.Run, manual bool) qaPlan {
	p := qaPlan{}
	if !step.QARework {
		return p
	}
	ciStep := workflowStep(e.Workflow, step.BaseFrom)
	ci := runs[ciStep.ID]
	qa := qaReport(e, runs[step.ID], ci, manual)
	if !rejectedQA(qa) {
		return p
	}
	p.RunID = qa.ID
	rebuilt := ci.Spec.RecheckOf == qa.ID
	if rebuilt && ci.Status != "succeeded" {
		return p.state("rework", "等待 CI 构建与测试")
	}
	if ci.Status != "succeeded" || ci.Result == nil || ci.Result.Commit == "" || !rebuilt && qa.Spec.Commit != ci.Result.Commit {
		return qaPlan{}
	}
	decision := e.Decisions[qa.ID]
	bugs := accountableBugs(qa.Result.Bugs)
	develop := workflowStep(e.Workflow, ciStep.BaseFrom)
	sources := map[string]protocol.Run{"develop": runs[develop.ID]}
	for _, role := range []string{"art", "audio", "develop"} {
		if role != "develop" && slices.Contains(develop.DependsOn, role) {
			sources[role] = runs[role]
		}
		src := sources[role]
		if _, ok := protocol.FindAgent(src.Spec.Executor); ok && !src.Spec.ReadOnly && src.Status == "succeeded" {
			p.Roles = append(p.Roles, role)
		}
	}
	if rebuilt || len(bugs) == 0 || decision.Recheck {
		if len(bugs) < len(qa.Result.Bugs) && !decision.EnvironmentReady {
			return p.state("environment", "请恢复 QA 执行环境，再对当前构建补验")
		}
		spec := qa.Spec
		var open []protocol.Bug
		for _, t := range e.Tickets[step.ID] {
			if !t.Closed {
				open = append(open, t.Bug)
			}
		}
		spec.Commit = ci.Result.Commit
		spec.Prompt = workflowPrompt(step, e.Brief) + "\n复验下列未关闭缺陷及其余必要标准，记录原编号。\n" + bugEvidence(open) + "\n产品补充说明：\n" + decision.Note
		p.stepID, p.spec, p.build = step.ID, spec, ci
		return p.state("rechecking", "等待 QA 复验当前构建")
	}

	p = routeQABugs(p, e, runs, qa, ci, bugs, decision)
	if p.Phase != "" {
		return p
	}
	return nextQARepair(p, e, runs, sources, qa, ci, bugs, decision, manual)
}

func routeQABugs(p qaPlan, e protocol.Execution, runs map[string]protocol.Run, qa, ci protocol.Run, bugs []protocol.Bug, decision protocol.QADecision) qaPlan {
	if len(decision.Assignments) != 0 {
		p.Assignments = decision.Assignments
		return p
	}
	rounds := map[string]bool{}
	for _, run := range e.Runs {
		if run.StepID == ci.StepID && run.Spec.RecheckOf != "" {
			rounds[run.Spec.RecheckOf] = true
		}
	}
	if len(rounds) >= automaticQARounds {
		return p.state("product_review", "已完成两轮自动返工，请产品确认下一轮分工和修复要求")
	}

	p.Assignments = make(map[string]protocol.Assignment)
	var ambiguous []protocol.Bug
	for _, bug := range bugs {
		if slices.Contains(p.Roles, bug.Owner) {
			p.Assignments[bug.ID] = protocol.Assignment{Owner: bug.Owner}
		} else {
			ambiguous = append(ambiguous, bug)
		}
	}
	if len(ambiguous) == 0 {
		return p
	}
	triageID := qa.StepID + ":triage"
	triage := runs[triageID]
	if triage.Spec.RepairOf != qa.ID {
		source := runs["producer"]
		agent, ok := protocol.FindAgent(source.Spec.Executor)
		if !ok || source.Status != "succeeded" {
			return p.state("product_review", "请产品指定缺陷的主责和协作角色")
		}
		p.stepID = triageID
		p.spec = protocol.Spec{
			Project: e.Project, MachineID: e.MachineID, Engine: e.Workflow.Engine, Executor: agent.ID, ReadOnly: agent.ReadOnly,
			Commit: ci.Result.Commit, RepairOf: qa.ID, Title: "产品分诊 QA 缺陷",
			Prompt: "阅读已确认的项目需求与验收标准，为以下每个编号指定一个主责和必要的协作角色。请勿修改项目。只输出 JSON 对象，键为缺陷编号，值格式为 {\"owner\":\"develop\",\"helpers\":[\"audio\"]}。可用角色：" + strings.Join(p.Roles, ", ") + "。如果需要人工澄清，输出 {}。\n游戏简报：\n" + e.Brief + "\n" + bugEvidence(ambiguous),
		}
		return p.state("triaging", "等待产品 Agent 分派缺陷")
	}
	if pending(triage) {
		return p.state("triaging", "产品 Agent 正在分派缺陷")
	}
	var assignments map[string]protocol.Assignment
	if triage.Status != "succeeded" || triage.Result == nil || json.Unmarshal([]byte(triage.Result.Reply), &assignments) != nil || !protocol.ValidAssignments(assignments, ambiguous, p.Roles) {
		return p.state("product_review", "产品分诊需要人工确认主责和协作角色")
	}
	for id, a := range assignments {
		p.Assignments[id] = a
	}
	return p
}

func nextQARepair(p qaPlan, e protocol.Execution, runs, sources map[string]protocol.Run, qa, ci protocol.Run, bugs []protocol.Bug, decision protocol.QADecision, manual bool) qaPlan {
	plan := p.Assignments
	base := ci.Result.Commit
	for _, role := range []string{"art", "audio", "develop"} {
		var assigned []protocol.Bug
		for _, bug := range bugs {
			a := plan[bug.ID]
			if a.Owner == role || slices.Contains(a.Helpers, role) {
				bug.Owner = a.Owner
				assigned = append(assigned, bug)
			}
		}
		if len(assigned) == 0 && role != "develop" {
			continue
		}
		id := qa.StepID + ":fix:" + role
		prior := runs[id]
		title := map[string]string{"art": "美术修复资源", "audio": "音效修复资源", "develop": "开发修复并集成资源"}[role]
		if prior.Spec.RepairOf == qa.ID && prior.Spec.QADispatch == decision.Dispatch {
			if pending(prior) {
				return p.state("rework", title)
			}
			if prior.Status == "succeeded" && prior.Result != nil && prior.Result.Commit != "" {
				base = prior.Result.Commit
				continue
			}
			if !manual {
				return qaPlan{}
			}
			p.stepID, p.spec = id, prior.Spec
			return p.state("rework", title)
		}
		spec := sources[role].Spec
		if !slices.Contains(p.Roles, role) {
			return qaPlan{}
		}
		spec.Title, spec.Commit, spec.MergeCommits, spec.RepairOf = title, base, nil, qa.ID
		spec.QADispatch = decision.Dispatch
		spec.Prompt = "按原验收标准处理分派给你的缺陷。\n" + bugEvidence(assigned)
		if role == "develop" {
			spec.Prompt += "\n接入返工资源，检查下列缺陷的集成结果。\n" + bugEvidence(bugs)
		}
		spec.Prompt += "\n游戏简报：\n" + e.Brief + "\n产品补充说明：\n" + decision.Note
		p.stepID, p.spec = id, spec
		return p.state("rework", title)
	}
	if base == ci.Result.Commit {
		return p.state("product_review", "返工未产生版本变更，请产品核对分工或补充复验说明")
	}
	spec := ci.Spec
	spec.Commit, spec.MergeCommits, spec.RecheckOf = base, nil, qa.ID
	p.stepID, p.spec = ci.StepID, spec
	return p.state("rework", "等待 CI 重建并测试修复版本")
}

func bugEvidence(bugs []protocol.Bug) string {
	var text strings.Builder
	for _, bug := range bugs {
		fmt.Fprintf(&text, "- %s [%s] %s\n  复现：%s\n  预期：%s\n  实际：%s\n", bug.ID, bug.Owner, bug.Title, bug.Steps, bug.Expected, bug.Actual)
	}
	return text.String()
}

func handleStepFailure(ctx context.Context, tx pgx.Tx, e protocol.Execution, step protocol.Step, runs map[string]protocol.Run, manual bool) (bool, error) {
	if !step.QARework {
		return handleCIFailure(ctx, tx, e, step, runs, manual)
	}
	p := planQA(e, step, runs, manual)
	if p.Phase == "" {
		return false, nil
	}
	d := e.Decisions[p.RunID]
	resuming := manual && p.RunID != runs[step.ID].ID && d.ResumeAfter != runs[step.ID].ID
	if resuming {
		d.ResumeAfter = runs[step.ID].ID
	}
	// Freeze the approved routing with the first repair, for subsequent roles
	// and for the ticket history after this QA attempt has ended.
	newAssignments := len(p.Assignments) != 0 && len(d.Assignments) == 0 && strings.HasPrefix(p.stepID, step.ID+":fix:")
	if newAssignments {
		d.Assignments = p.Assignments
	}
	if resuming || newAssignments {
		if _, err := tx.Exec(ctx, `UPDATE executions SET qa_decisions=jsonb_set(qa_decisions,ARRAY[$2::text],$3) WHERE id=$1`, e.ID, p.RunID, d); err != nil {
			return false, err
		}
	}
	if p.stepID == "" {
		return true, nil
	}
	if p.build.ID != "" {
		if err := appendBuildEvidence(ctx, tx, &p.spec, p.build); err != nil {
			return false, err
		}
	}
	return true, enqueueWorkflowAttempt(ctx, tx, e.ID, p.stepID, runs[p.stepID].Attempt+1, p.spec)
}
