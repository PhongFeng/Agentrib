package store

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/jackc/pgx/v5"
)

//go:embed workflow.sql
var workflowSchema string

const executionColumns = "id, workflow_id,version,machine_id,project,brief,commit_sha,status,created,qa_phase,qa_decisions"
const workflowColumns = "id,version,name,steps,engine"

var ErrEngineUnavailable = errors.New("Selected game engine has no available adapter on this Runner")
var ErrProjectEngine = errors.New("Selected project does not match the workflow engine")
var ErrAgentUnavailable = errors.New("Selected Agent is unavailable on this Runner")
var ErrProductionUnavailable = errors.New("执行机器需要更新 Runner 才能运行此制作工作流")

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (s *Store) SaveWorkflow(ctx context.Context, workflow protocol.Workflow) (protocol.Workflow, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return workflow, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 16))`, workflow.ID); err != nil {
		return workflow, err
	}
	var version int
	var archived bool
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(version),0), coalesce(bool_and(archived),false) FROM workflows WHERE id=$1`, workflow.ID).Scan(&version, &archived); err != nil {
		return workflow, err
	}
	if version != workflow.Version || archived {
		return workflow, ErrConflict
	}
	workflow.Version = version + 1
	workflow.Engine = protocol.EngineID(workflow.Engine)
	if _, err := tx.Exec(ctx, `INSERT INTO workflows (`+workflowColumns+`) VALUES ($1,$2,$3,$4,$5)`, workflow.ID, workflow.Version, workflow.Name, workflow.Steps, workflow.Engine); err != nil {
		return workflow, err
	}
	return workflow, tx.Commit(ctx)
}

func scanWorkflow(row pgx.Row) (protocol.Workflow, error) {
	var workflow protocol.Workflow
	var steps []byte
	err := row.Scan(&workflow.ID, &workflow.Version, &workflow.Name, &steps, &workflow.Engine)
	if err == nil {
		err = json.Unmarshal(steps, &workflow.Steps)
	}
	for i := range workflow.Steps {
		step := &workflow.Steps[i]
		if step.ID == "producer" && step.Role == "制作人 / 产品 Agent" {
			step.Role = "产品 Agent"
		}
	}
	return workflow, err
}

func (s *Store) Workflows(ctx context.Context) ([]protocol.Workflow, error) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT ON (id) `+workflowColumns+` FROM workflows WHERE NOT archived ORDER BY id,version DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []protocol.Workflow{}
	for rows.Next() {
		workflow, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, workflow)
	}
	return list, rows.Err()
}

func scanExecution(row pgx.Row) (protocol.Execution, error) {
	var e protocol.Execution
	err := row.Scan(&e.ID, &e.WorkflowID, &e.Version, &e.MachineID, &e.Project, &e.Brief, &e.Commit, &e.Status, &e.Created, &e.QAPhase, &e.Decisions)
	return e, err
}

func (s *Store) StartExecution(ctx context.Context, input protocol.ExecutionInput) (protocol.Execution, error) {
	workflow, err := scanWorkflow(s.db.QueryRow(ctx, `SELECT `+workflowColumns+` FROM workflows WHERE id=$1 AND version=$2 AND NOT archived`, input.WorkflowID, input.Version))
	if err != nil {
		return protocol.Execution{}, err
	}
	engine, known := protocol.FindEngine(workflow.Engine)
	if !known || !engine.Implemented {
		return protocol.Execution{}, ErrEngineUnavailable
	}
	machine, err := scanMachine(s.db.QueryRow(ctx, `SELECT `+machineColumns+` FROM machines WHERE id=$1`, input.MachineID))
	if err != nil {
		return protocol.Execution{}, err
	}
	if !slices.Contains(machine.Projects, input.Project) || protocol.EngineID(machine.ProjectEngines[input.Project]) != engine.ID {
		return protocol.Execution{}, ErrProjectEngine
	}
	for _, step := range workflow.Steps {
		if !machine.ProductionWorkflow && (len(step.MergeFrom) > 0 || step.QAGate || step.ExportGame) {
			return protocol.Execution{}, ErrProductionUnavailable
		}
		if (step.Preflight || step.QARework) && !machine.WorkflowReview {
			return protocol.Execution{}, ErrProductionUnavailable
		}
		if agent, ok := protocol.FindAgent(step.CIRepairAgent); ok && !slices.Contains(machine.Executors, agent.ID) {
			return protocol.Execution{}, fmt.Errorf("%w: %s", ErrAgentUnavailable, agent.Name)
		}
		if agent, ok := protocol.FindAgent(step.Executor); ok && !slices.Contains(machine.Executors, agent.ID) {
			return protocol.Execution{}, fmt.Errorf("%w: %s", ErrAgentUnavailable, agent.Name)
		}
		if (step.Executor == "engine" || step.Executor == engine.ID) && !slices.Contains(machine.Executors, engine.ID) {
			return protocol.Execution{}, ErrEngineUnavailable
		}
	}
	row := s.db.QueryRow(ctx, `INSERT INTO executions (id, workflow_id,version,machine_id,project,brief,commit_sha)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO UPDATE SET id=excluded.id
		WHERE executions.workflow_id=excluded.workflow_id AND executions.version=excluded.version AND executions.machine_id=excluded.machine_id
		AND executions.project=excluded.project AND executions.brief=excluded.brief AND executions.commit_sha=excluded.commit_sha AND NOT executions.archived
		RETURNING `+executionColumns, input.ID, input.WorkflowID, input.Version, input.MachineID, input.Project, input.Brief, input.Commit)
	e, err := scanExecution(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, ErrConflict
	}
	return e, err
}

func (s *Store) Executions(ctx context.Context, project string) ([]protocol.Execution, error) {
	rows, err := s.db.Query(ctx, `SELECT `+executionColumns+` FROM executions
		WHERE NOT archived AND ($1='' OR project=$1) ORDER BY created DESC,id DESC LIMIT 100`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []protocol.Execution{}
	for rows.Next() {
		e, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

func (s *Store) Execution(ctx context.Context, id string) (protocol.Execution, error) {
	e, err := scanExecution(s.db.QueryRow(ctx, `SELECT `+executionColumns+` FROM executions WHERE id=$1 AND NOT archived`, id))
	if err != nil {
		return e, err
	}
	return hydrate(ctx, s.db, e)
}

func hydrate(ctx context.Context, db queryer, e protocol.Execution) (protocol.Execution, error) {
	var err error
	e.Workflow, err = scanWorkflow(db.QueryRow(ctx, `SELECT `+workflowColumns+` FROM workflows WHERE id=$1 AND version=$2`, e.WorkflowID, e.Version))
	if err != nil {
		return e, err
	}
	rows, err := db.Query(ctx, `SELECT `+runColumns+` FROM runs WHERE execution_id=$1 ORDER BY created,id`, e.ID)
	if err != nil {
		return e, err
	}
	e.Runs = []protocol.Run{}
	for rows.Next() {
		run, err := scanRun(rows, false)
		if err != nil {
			rows.Close()
			return e, err
		}
		e.Runs = append(e.Runs, run)
	}
	if err := rows.Err(); err != nil {
		return e, err
	}
	rows, err = db.Query(ctx, `SELECT step_id FROM step_approvals WHERE execution_id=$1 ORDER BY step_id`, e.ID)
	if err != nil {
		return e, err
	}
	defer rows.Close()
	e.Approved = []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return e, err
		}
		e.Approved = append(e.Approved, id)
	}
	if err := rows.Err(); err != nil {
		return e, err
	}
	_, latest := outcomes(e)
	e.Reviews = make(map[string]protocol.QAReview)
	e.Tickets = make(map[string][]protocol.Ticket)
	for _, step := range e.Workflow.Steps {
		if !step.QARework {
			continue
		}
		tickets := protocol.TrackTickets(e.Runs, step.ID, e.Decisions)
		if len(tickets) == 0 {
			continue
		}
		e.Tickets[step.ID] = tickets
		p := planQA(e, step, latest, e.Status == "failed")
		if p.Phase != "" {
			e.Reviews[step.ID] = p.QAReview
		} else if r := latest[step.ID]; pending(r) {
			e.Reviews[step.ID] = protocol.QAReview{RunID: r.ID, Phase: "rechecking", Reason: "QA 正在复验当前构建"}
		}
	}
	return e, nil
}

func outcomes(e protocol.Execution) (map[string]bool, map[string]protocol.Run) {
	done := make(map[string]bool)
	runs := make(map[string]protocol.Run)
	for _, approved := range e.Approved {
		done[approved] = true
	}
	for _, run := range e.Runs {
		if runs[run.StepID].Attempt > run.Attempt {
			continue
		}
		runs[run.StepID] = run
		done[run.StepID] = run.Status == "succeeded"
	}
	return done, runs
}

func interrupted(status string) bool {
	return status == "rejected" || status == "failed" || status == "lost" || status == "canceled"
}

// ContinueExecution opens new attempts on the same workflow and Git inputs.
// Finished steps, approvals and previous attempts remain part of its history.
func (s *Store) ContinueExecution(ctx context.Context, id string) (protocol.Execution, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return protocol.Execution{}, err
	}
	defer tx.Rollback(ctx)
	e, err := scanExecution(tx.QueryRow(ctx, `SELECT `+executionColumns+` FROM executions WHERE id=$1 AND NOT archived FOR UPDATE`, id))
	if err != nil {
		return e, err
	}
	if e.Status != "failed" {
		return e, ErrConflict
	}
	e, err = hydrate(ctx, tx, e)
	if err != nil {
		return e, err
	}
	_, latest := outcomes(e)
	retried := false
	for _, step := range e.Workflow.Steps {
		prior := latest[step.ID]
		if !interrupted(prior.Status) {
			continue
		}
		handled, err := handleStepFailure(ctx, tx, e, step, latest, true)
		if err != nil {
			return e, err
		}
		if !handled {
			if err := enqueueWorkflowAttempt(ctx, tx, e.ID, step.ID, prior.Attempt+1, prior.Spec); err != nil {
				return e, err
			}
		}
		retried = true
	}
	if !retried {
		return e, ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE executions SET status='running' WHERE id=$1`, e.ID); err != nil {
		return e, err
	}
	e.Status = "running"
	e, err = hydrate(ctx, tx, e)
	if err != nil {
		return e, err
	}
	return e, tx.Commit(ctx)
}

func ready(step protocol.Step, done map[string]bool) bool {
	for _, dependency := range step.DependsOn {
		if !done[dependency] {
			return false
		}
	}
	return true
}

func (s *Store) Approve(ctx context.Context, id, stepID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	e, err := scanExecution(tx.QueryRow(ctx, `SELECT `+executionColumns+` FROM executions WHERE id=$1 AND NOT archived FOR UPDATE`, id))
	if err != nil {
		return err
	}
	e, err = hydrate(ctx, tx, e)
	if err != nil {
		return err
	}
	done, _ := outcomes(e)
	if done[stepID] {
		for _, approved := range e.Approved {
			if approved == stepID {
				return nil
			}
		}
	}
	if e.Status != "running" {
		return ErrConflict
	}
	for _, step := range e.Workflow.Steps {
		if step.ID == stepID && step.Executor == "approval" && ready(step, done) {
			if _, err := tx.Exec(ctx, `INSERT INTO step_approvals (execution_id,step_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, stepID); err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
	}
	return ErrConflict
}

func (s *Store) Schedule(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `SELECT id FROM executions WHERE status='running' AND NOT archived ORDER BY created,id LIMIT 500`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		if err := s.scheduleExecution(ctx, id); err != nil {
			failures = append(failures, fmt.Errorf("schedule %s: %w", id, err))
		}
	}
	return errors.Join(failures...)
}

func (s *Store) scheduleExecution(ctx context.Context, id string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	e, err := scanExecution(tx.QueryRow(ctx, `SELECT `+executionColumns+` FROM executions WHERE id=$1 AND status='running' AND NOT archived FOR UPDATE SKIP LOCKED`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	e, err = hydrate(ctx, tx, e)
	if err != nil {
		return err
	}
	done, runs := outcomes(e)
	failed := false
	complete := true
	reviewComplete := true
	for _, step := range e.Workflow.Steps {
		complete = complete && done[step.ID]
		if step.QARework {
			reviewComplete = reviewComplete && done[step.ID]
		}
		if interrupted(runs[step.ID].Status) {
			handled, err := handleStepFailure(ctx, tx, e, step, runs, false)
			if err != nil {
				return err
			}
			failed = failed || !handled
		}
	}
	if failed || complete {
		status := "succeeded"
		if failed {
			status = "failed"
		}
		if _, err := tx.Exec(ctx, `UPDATE executions SET status=$2,qa_phase='' WHERE id=$1`, e.ID, status); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	for _, step := range e.Workflow.Steps {
		if step.Executor == "approval" || done[step.ID] || runs[step.ID].ID != "" || !ready(step, done) {
			continue
		}
		if step.Executor == "notify" {
			if !reviewComplete {
				continue
			}
			if _, err := tx.Exec(ctx, `INSERT INTO step_approvals (execution_id,step_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, e.ID, step.ID); err != nil {
				return err
			}
			continue
		}
		spec := protocol.Spec{MachineID: e.MachineID, Project: e.Project, Executor: step.Executor,
			Engine: e.Workflow.Engine, Title: step.Name, ReadOnly: step.ReadOnly, QAGate: step.QAGate, ExportGame: step.ExportGame,
			Preflight: step.Preflight, QARework: step.QARework, Prompt: workflowPrompt(step, e.Brief), Commit: e.Commit}
		if spec.Executor == "engine" {
			spec.Executor = spec.Engine
		}
		if step.BaseFrom != "" {
			for i, source := range append([]string{step.BaseFrom}, step.MergeFrom...) {
				predecessor := runs[source].Result
				if predecessor == nil || predecessor.Commit == "" {
					if _, err := tx.Exec(ctx, `UPDATE executions SET status='failed',qa_phase='' WHERE id=$1`, e.ID); err != nil {
						return err
					}
					return tx.Commit(ctx)
				}
				if i == 0 {
					spec.Commit = predecessor.Commit
				} else {
					spec.MergeCommits = append(spec.MergeCommits, predecessor.Commit)
				}
			}
		}
		if step.ReadOnly || step.QARework {
			for _, dependency := range step.DependsOn {
				if err := appendBuildEvidence(ctx, tx, &spec, runs[dependency]); err != nil {
					return err
				}
			}
		}
		digest := sha256.Sum256([]byte(e.ID + "/" + step.ID))
		if _, err := insertRun(ctx, tx, hex.EncodeToString(digest[:16]), spec, e.ID, step.ID, 1); err != nil {
			return err
		}
	}
	// Sleep only when this execution has no work besides an operator decision.
	phase := ""
	wait := false
	otherWork := false
	for _, step := range e.Workflow.Steps {
		if review, ok := e.Reviews[step.ID]; ok {
			if waitingForQA(review.Phase) {
				wait = true
				phase = review.Phase
			} else {
				otherWork = true
				if !wait {
					phase = review.Phase
				}
			}
		} else if !done[step.ID] && ready(step, done) && (step.Executor != "notify" || reviewComplete) {
			otherWork = true
		}
	}
	status := "running"
	if wait && !otherWork {
		status = "waiting_qa"
	}
	if phase != e.QAPhase || status != e.Status {
		if _, err := tx.Exec(ctx, `UPDATE executions SET status=$2,qa_phase=$3 WHERE id=$1`, e.ID, status, phase); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func workflowPrompt(step protocol.Step, brief string) string {
	return "Role: " + step.Role + "\n" + step.Prompt + "\n\nGame brief:\n" + brief
}

func appendBuildEvidence(ctx context.Context, tx pgx.Tx, spec *protocol.Spec, prior protocol.Run) error {
	engine, ok := protocol.FindEngine(prior.Spec.Executor)
	if !ok || prior.Result == nil {
		return nil
	}
	var evidence string
	if err := tx.QueryRow(ctx, `WITH test_start AS (
		SELECT max(seq) AS seq FROM run_events WHERE run_id=$1 AND kind='tool' AND body LIKE 'godot --script %'
	), test_end AS (
		SELECT coalesce(min(e.seq),10001) AS seq FROM run_events e,test_start s
		WHERE e.run_id=$1 AND e.kind='tool' AND e.seq>s.seq
	)
	SELECT coalesce(string_agg(body,chr(10) ORDER BY seq),'') FROM (
		SELECT e.seq,left(e.body,240) AS body FROM run_events e,test_start s,test_end f
		WHERE e.run_id=$1 AND (e.kind='test' OR ($2='godot' AND e.kind='godot' AND e.seq>s.seq AND e.seq<f.seq))
		ORDER BY e.seq DESC LIMIT 16
	) AS lines`, prior.ID, engine.ID).Scan(&evidence); err != nil {
		return err
	}
	spec.Prompt += "\n\n" + engine.Name + " test evidence (test script output):\n" + strings.TrimSpace(evidence)
	if prior.Result != nil && len(prior.Result.Artifacts) > 0 {
		spec.Prompt += "\nBuild artifacts:\n"
		for _, artifact := range prior.Result.Artifacts {
			spec.Prompt += fmt.Sprintf("- %s (%d bytes)\n", artifact.Name, artifact.Bytes)
		}
	}
	return nil
}
