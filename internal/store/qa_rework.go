package store

import (
	"context"
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

// Rework starts at the rejected revision, so earlier fixes and all approved
// assets remain in the next build. Each role receives the preceding role's output.
func handleQAFailure(ctx context.Context, tx pgx.Tx, e protocol.Execution, step protocol.Step, runs map[string]protocol.Run, manual bool) (bool, error) {
	qa := runs[step.ID]
	if !step.QARework || qa.Status != "failed" || qa.Result == nil || !protocol.ValidBugs(qa.Result.Bugs) {
		return false, nil
	}

	ciStep := workflowStep(e.Workflow, step.BaseFrom)
	ci := runs[ciStep.ID]
	if ci.Spec.RecheckOf == qa.ID {
		if ci.Status != "succeeded" {
			// CI handles its own failure and keeps this review waiting.
			return true, nil
		}
		if ci.Result == nil || ci.Result.Commit == "" {
			return false, nil
		}
		spec := qa.Spec
		spec.Commit = ci.Result.Commit
		spec.Prompt = workflowPrompt(step, e.Brief) + "\n\n复验上一轮缺陷，同时重新检查其余必要标准。\n" + bugEvidence(qa.Result.Bugs)
		if err := appendBuildEvidence(ctx, tx, &spec, ci); err != nil {
			return false, err
		}
		return true, enqueueWorkflowAttempt(ctx, tx, e.ID, step.ID, qa.Attempt+1, spec)
	}
	if ci.Status != "succeeded" || ci.Result == nil || ci.Result.Commit == "" || qa.Spec.Commit != ci.Result.Commit {
		return false, nil
	}
	rounds := make(map[string]bool)
	for _, run := range e.Runs {
		if run.StepID == ci.StepID && run.Spec.RecheckOf != "" {
			rounds[run.Spec.RecheckOf] = true
		}
	}
	if !manual && len(rounds) >= automaticQARounds {
		return false, nil
	}

	byOwner := make(map[string][]protocol.Bug)
	for _, bug := range qa.Result.Bugs {
		if bug.Owner == "environment" {
			return false, nil
		}
		byOwner[bug.Owner] = append(byOwner[bug.Owner], bug)
	}
	develop := workflowStep(e.Workflow, ciStep.BaseFrom)
	for _, owner := range []string{"art", "audio"} {
		if len(byOwner[owner]) == 0 {
			continue
		}
		source := runs[owner]
		if _, ok := protocol.FindAgent(source.Spec.Executor); !ok || source.Spec.ReadOnly || source.Status != "succeeded" ||
			!slices.Contains(develop.DependsOn, owner) {
			return false, nil
		}
	}

	base := ci.Result.Commit
	for _, owner := range []string{"art", "audio", "develop"} {
		if owner != "develop" && len(byOwner[owner]) == 0 {
			continue
		}
		id := step.ID + ":fix:" + owner
		prior := runs[id]
		if prior.Spec.RepairOf == qa.ID {
			switch prior.Status {
			case "queued", "claimed", "running", "canceling":
				return true, nil
			case "succeeded":
				if prior.Result != nil && prior.Result.Commit != "" && (prior.Result.Commit != base || owner == "develop" && len(byOwner[owner]) == 0) {
					base = prior.Result.Commit
					continue
				}
			}
			if !manual {
				return false, nil
			}
			return true, enqueueWorkflowAttempt(ctx, tx, e.ID, id, prior.Attempt+1, prior.Spec)
		}
		sourceID := owner
		instructions := "只修复分派给你的缺陷及相关资源，记录修改后的文件和接入方式。"
		title := map[string]string{"art": "修复美术资源", "audio": "修复音效资源", "develop": "修复代码并接入返工资源"}[owner]
		bugs := byOwner[owner]
		if owner == "develop" {
			sourceID = develop.ID
			instructions = "修复开发缺陷，检查美术和音效返工产出并接入游戏。按缺陷单复现和验证，保留原验收标准。"
			bugs = qa.Result.Bugs
		}
		source := runs[sourceID]
		if _, ok := protocol.FindAgent(source.Spec.Executor); !ok || source.Spec.ReadOnly || source.Status != "succeeded" {
			return false, nil
		}
		spec := source.Spec
		spec.Title, spec.Commit, spec.MergeCommits, spec.RepairOf = title, base, nil, qa.ID
		spec.Prompt = instructions + "\n\n游戏简报：\n" + e.Brief + "\n\nQA 缺陷单：\n" + bugEvidence(bugs)
		return true, enqueueWorkflowAttempt(ctx, tx, e.ID, id, prior.Attempt+1, spec)
	}
	if base == ci.Result.Commit {
		return false, nil
	}
	spec := ci.Spec
	spec.Commit, spec.MergeCommits, spec.RecheckOf = base, nil, qa.ID
	return true, enqueueWorkflowAttempt(ctx, tx, e.ID, ci.StepID, ci.Attempt+1, spec)
}

func bugEvidence(bugs []protocol.Bug) string {
	var text strings.Builder
	for _, bug := range bugs {
		fmt.Fprintf(&text, "- [%s] %s\n  复现：%s\n  预期：%s\n  实际：%s\n", bug.Owner, bug.Title, bug.Steps, bug.Expected, bug.Actual)
	}
	return text.String()
}

func handleStepFailure(ctx context.Context, tx pgx.Tx, e protocol.Execution, step protocol.Step, runs map[string]protocol.Run, manual bool) (bool, error) {
	if step.QARework {
		return handleQAFailure(ctx, tx, e, step, runs, manual)
	}
	return handleCIFailure(ctx, tx, e, step, runs, manual)
}
