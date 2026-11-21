package store

import (
	"context"
	"slices"
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

// Bind the decision to the rejected run so a stale tab cannot release a newer gate.
func (s *Store) DecideQA(ctx context.Context, id, runID, action string, input protocol.QADecision) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	e, err := scanExecution(tx.QueryRow(ctx, `SELECT `+executionColumns+` FROM executions WHERE id=$1 AND NOT archived FOR UPDATE`, id))
	if err != nil {
		return err
	}
	if e.Status != "running" && e.Status != "waiting_qa" {
		return ErrConflict
	}
	e, err = hydrate(ctx, tx, e)
	if err != nil {
		return err
	}
	for step, review := range e.Reviews {
		if review.RunID != runID || !waitingForQA(review.Phase) {
			continue
		}
		d := e.Decisions[runID]
		switch action {
		case "assign":
			report := slices.IndexFunc(e.Runs, func(run protocol.Run) bool { return run.ID == runID && run.StepID == step })
			if review.Phase != "product_review" || report < 0 || !rejectedQA(e.Runs[report]) ||
				!protocol.ValidAssignments(input.Assignments, accountableBugs(e.Runs[report].Result.Bugs), review.Roles) {
				return ErrConflict
			}
			dispatch, err := newID()
			if err != nil {
				return err
			}
			d.Dispatch, d.Assignments, d.Note = dispatch, input.Assignments, input.Note
		case "recheck":
			if review.Phase != "product_review" || strings.TrimSpace(input.Note) == "" {
				return ErrConflict
			}
			d.Recheck, d.Note = true, input.Note
		case "environment_ready":
			if review.Phase != "environment" {
				return ErrConflict
			}
			d.EnvironmentReady = true
		case "defer_ticket":
			if review.Phase != "environment" || len(input.Deferred) != 1 {
				return ErrConflict
			}
			for ticketID, reason := range input.Deferred {
				if !slices.Contains(review.EnvironmentTickets, ticketID) || strings.TrimSpace(reason) == "" {
					return ErrConflict
				}
				if d.Deferred == nil {
					d.Deferred = make(map[string]string)
				}
				d.Deferred[ticketID] = strings.TrimSpace(reason)
			}
		case "stop":
			if _, err := tx.Exec(ctx, `UPDATE executions SET status='canceled',qa_phase='' WHERE id=$1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE runs SET status=CASE WHEN status='running' THEN 'canceling' ELSE 'canceled' END,
				finished_at=CASE WHEN status IN ('queued','claimed') THEN now() ELSE finished_at END,updated=now()
				WHERE execution_id=$1 AND status IN ('queued','claimed','running')`, id); err != nil {
				return err
			}
			return tx.Commit(ctx)
		default:
			return ErrConflict
		}
		e.Decisions[runID] = d
		if _, err := tx.Exec(ctx, `UPDATE executions SET status='running',qa_phase='',qa_decisions=$2 WHERE id=$1`, id, e.Decisions); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	return ErrConflict
}
