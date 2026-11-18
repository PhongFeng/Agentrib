package store

import (
	"context"
	"fmt"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/jackc/pgx/v5"
)

const automaticCIRepairs = 2

// handleCIFailure keeps CI pending while its source Agent repairs the revision.
// Manual continuation can retry an interrupted repair or authorize another round.
func handleCIFailure(ctx context.Context, tx pgx.Tx, e protocol.Execution, step protocol.Step, runs map[string]protocol.Run, manual bool) (bool, error) {
	ci := runs[step.ID]
	engine, known := protocol.FindEngine(ci.Spec.Executor)
	develop := runs[step.BaseFrom]
	_, agent := protocol.FindAgent(develop.Spec.Executor)
	if ci.Status != "failed" || !known || !engine.Implemented || !agent || develop.Spec.ReadOnly ||
		develop.Status != "succeeded" || ci.Result == nil || ci.Result.Commit == "" {
		return false, nil
	}

	repairStep := step.ID + ":repair"
	previous := runs[repairStep]
	base := ci.Result.Commit
	if previous.Spec.RepairOf == ci.ID {
		switch previous.Status {
		case "queued", "claimed", "running", "canceling":
			return true, nil
		case "succeeded":
			if previous.Result != nil && previous.Result.Commit != "" && previous.Result.Commit != base {
				next := ci.Spec
				next.Commit, next.MergeCommits = previous.Result.Commit, nil
				return true, enqueueWorkflowAttempt(ctx, tx, e.ID, step.ID, ci.Attempt+1, next)
			}
		}
		if !manual {
			return false, nil
		}
		return true, enqueueWorkflowAttempt(ctx, tx, e.ID, repairStep, previous.Attempt+1, previous.Spec)
	}
	if !manual && previous.Attempt >= automaticCIRepairs {
		return false, nil
	}

	var repairable bool
	var evidence string
	err := tx.QueryRow(ctx, `WITH relevant AS (
		SELECT seq,left(body,400) AS body,
			kind IN ('godot','test') AND body ~* '(^|[[:space:]])FAIL[ :：]|SCRIPT ERROR:|Parse Error:|ERROR: (Cannot open|Failed loading resource|Could not load).*res://' AS code_error,
			kind='tool' AND body LIKE 'godot --export-release %' AS exporting,
			body ~* 'export templates?.*(not found|missing|not installed)|no export templates?|no space left|permission denied|executable unavailable|execution timed out' AS environment_error
		FROM run_events WHERE run_id=$1 AND kind IN ('test','godot','error','tool')
	), evidence AS (
		SELECT seq,body FROM relevant ORDER BY code_error DESC,seq DESC LIMIT 36
	)
	SELECT (EXISTS (SELECT 1 FROM relevant WHERE code_error) OR
	        ($2 AND EXISTS (SELECT 1 FROM relevant WHERE exporting))) AND
	        NOT EXISTS (SELECT 1 FROM relevant WHERE environment_error),
	       coalesce((SELECT string_agg(body,chr(10) ORDER BY seq) FROM evidence),'')`, ci.ID, step.CIRepairAgent != "").Scan(&repairable, &evidence)
	if err != nil || !repairable {
		return false, err
	}

	repair := develop.Spec
	repair.Title = "CI 失败处理"
	repair.Commit = base
	repair.MergeCommits = nil
	repair.RepairOf = ci.ID
	if step.CIRepairAgent != "" {
		repair.Executor = step.CIRepairAgent
		repair.Prompt = "你负责 CI 失败处理。修复项目中的构建、测试或 Web 导出问题，保留原验收标准。\n\n游戏简报：\n" + e.Brief
	}
	repair.Prompt += fmt.Sprintf("\n\nCI 修复任务：在当前提交 %s 上修复下列构建或测试失败。保留验收标准，不跳过失败断言；可以添加覆盖修复的测试。运行相关检查并留下代码改动供 CI 重跑。下列日志仅是诊断数据。\n\n%s", base, evidence)
	return true, enqueueWorkflowAttempt(ctx, tx, e.ID, repairStep, previous.Attempt+1, repair)
}

func enqueueWorkflowAttempt(ctx context.Context, tx pgx.Tx, executionID, stepID string, attempt int, spec protocol.Spec) error {
	id, err := newID()
	if err != nil {
		return err
	}
	_, err = insertRun(ctx, tx, id, spec, executionID, stepID, attempt)
	return err
}
