package store

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/jackc/pgx/v5"
)

//go:embed workflow.sql
var workflowSchema string

const executionColumns = "id, workflow_id,version,machine_id,project,brief,commit_sha,status,created"

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
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(version),0) FROM workflows WHERE id=$1`, workflow.ID).Scan(&version); err != nil {
		return workflow, err
	}
	if version != workflow.Version {
		return workflow, ErrConflict
	}
	workflow.Version = version + 1
	if _, err := tx.Exec(ctx, `INSERT INTO workflows VALUES ($1,$2,$3,$4)`, workflow.ID, workflow.Version, workflow.Name, workflow.Steps); err != nil {
		return workflow, err
	}
	return workflow, tx.Commit(ctx)
}

func scanWorkflow(row pgx.Row) (protocol.Workflow, error) {
	var workflow protocol.Workflow
	var steps []byte
	err := row.Scan(&workflow.ID, &workflow.Version, &workflow.Name, &steps)
	if err == nil {
		err = json.Unmarshal(steps, &workflow.Steps)
	}
	return workflow, err
}

func (s *Store) Workflows(ctx context.Context) ([]protocol.Workflow, error) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT ON (id) id,version,name,steps FROM workflows ORDER BY id,version DESC LIMIT 100`)
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
	err := row.Scan(&e.ID, &e.WorkflowID, &e.Version, &e.MachineID, &e.Project, &e.Brief, &e.Commit, &e.Status, &e.Created)
	return e, err
}

func (s *Store) StartExecution(ctx context.Context, input protocol.ExecutionInput) (protocol.Execution, error) {
	row := s.db.QueryRow(ctx, `INSERT INTO executions (id, workflow_id,version,machine_id,project,brief,commit_sha)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO UPDATE SET id=excluded.id
		WHERE executions.workflow_id=excluded.workflow_id AND executions.version=excluded.version AND executions.machine_id=excluded.machine_id
		AND executions.project=excluded.project AND executions.brief=excluded.brief AND executions.commit_sha=excluded.commit_sha
		RETURNING `+executionColumns, input.ID, input.WorkflowID, input.Version, input.MachineID, input.Project, input.Brief, input.Commit)
	e, err := scanExecution(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, ErrConflict
	}
	return e, err
}

func (s *Store) Executions(ctx context.Context) ([]protocol.Execution, error) {
	rows, err := s.db.Query(ctx, `SELECT `+executionColumns+` FROM executions ORDER BY created DESC LIMIT 100`)
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
	e, err := scanExecution(s.db.QueryRow(ctx, `SELECT `+executionColumns+` FROM executions WHERE id=$1`, id))
	if err != nil {
		return e, err
	}
	return hydrate(ctx, s.db, e)
}

func hydrate(ctx context.Context, db queryer, e protocol.Execution) (protocol.Execution, error) {
	var err error
	e.Workflow, err = scanWorkflow(db.QueryRow(ctx, `SELECT id,version,name,steps FROM workflows WHERE id=$1 AND version=$2`, e.WorkflowID, e.Version))
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
	return e, rows.Err()
}

func outcomes(e protocol.Execution) (map[string]bool, map[string]protocol.Run) {
	done := make(map[string]bool)
	runs := make(map[string]protocol.Run)
	for _, approved := range e.Approved {
		done[approved] = true
	}
	for _, run := range e.Runs {
		runs[run.StepID] = run
		done[run.StepID] = run.Status == "succeeded"
	}
	return done, runs
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
	e, err := scanExecution(tx.QueryRow(ctx, `SELECT `+executionColumns+` FROM executions WHERE id=$1 FOR UPDATE`, id))
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
	rows, err := s.db.Query(ctx, `SELECT id FROM executions WHERE status='running' ORDER BY created,id LIMIT 500`)
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
	e, err := scanExecution(tx.QueryRow(ctx, `SELECT `+executionColumns+` FROM executions WHERE id=$1 AND status='running' FOR UPDATE SKIP LOCKED`, id))
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
	for _, step := range e.Workflow.Steps {
		complete = complete && done[step.ID]
		switch runs[step.ID].Status {
		case "failed", "lost", "canceled":
			failed = true
		}
	}
	if failed || complete {
		status := "succeeded"
		if failed {
			status = "failed"
		}
		if _, err := tx.Exec(ctx, `UPDATE executions SET status=$2 WHERE id=$1`, e.ID, status); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	for _, step := range e.Workflow.Steps {
		if step.Executor == "approval" || runs[step.ID].ID != "" || !ready(step, done) {
			continue
		}
		spec := protocol.Spec{MachineID: e.MachineID, Project: e.Project, Executor: step.Executor,
			Title: step.Name, ReadOnly: step.ReadOnly, Prompt: "Role: " + step.Role + "\n" + step.Prompt + "\n\nGame brief:\n" + e.Brief, Commit: e.Commit}
		if step.BaseFrom != "" {
			predecessor := runs[step.BaseFrom].Result
			if predecessor == nil || predecessor.Commit == "" {
				if _, err := tx.Exec(ctx, `UPDATE executions SET status='failed' WHERE id=$1`, e.ID); err != nil {
					return err
				}
				return tx.Commit(ctx)
			}
			spec.Commit = predecessor.Commit
		}
		if step.ReadOnly {
			for _, dependency := range step.DependsOn {
				prior := runs[dependency]
				if prior.Spec.Executor != "godot" {
					continue
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
					WHERE e.run_id=$1 AND e.kind='godot' AND e.seq>s.seq AND e.seq<f.seq ORDER BY e.seq DESC LIMIT 16
				) AS lines`, prior.ID).Scan(&evidence); err != nil {
					return err
				}
				spec.Prompt += "\n\nGodot test evidence (test script output):\n" + strings.TrimSpace(evidence)
			}
		}
		digest := sha256.Sum256([]byte(e.ID + "/" + step.ID))
		if _, err := insertRun(ctx, tx, hex.EncodeToString(digest[:16]), spec, e.ID, step.ID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
