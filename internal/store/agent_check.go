package store

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/jackc/pgx/v5"
)

var ErrAgentCheckUnavailable = errors.New("Runner 离线、需要更新或该 Agent 的 CLI 尚未就绪")

func (s *Store) AgentChecks(ctx context.Context, machine string) ([]protocol.Run, error) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT ON (spec->>'executor') `+runColumns+` FROM runs
		WHERE machine_id=$1 AND spec->>'agent_check'='true' ORDER BY spec->>'executor',created DESC,id DESC`, machine)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []protocol.Run{}
	for rows.Next() {
		run, err := scanRun(rows, false)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Store) CheckAgent(ctx context.Context, machineID, executor string) (protocol.Run, error) {
	agent, ok := protocol.FindAgent(executor)
	if !ok {
		return protocol.Run{}, ErrAgentCheckUnavailable
	}
	if err := s.expire(ctx); err != nil {
		return protocol.Run{}, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return protocol.Run{}, err
	}
	defer tx.Rollback(ctx)
	// Serialize requests for this Runner so repeated clicks reuse a pending check.
	machine, err := scanMachine(tx.QueryRow(ctx, `SELECT `+machineColumns+` FROM machines WHERE id=$1 FOR UPDATE`, machineID))
	if err != nil {
		return protocol.Run{}, err
	}
	if !machine.CanCheckAgents || time.Since(machine.Seen) >= 45*time.Second || !slices.Contains(machine.Executors, executor) {
		return protocol.Run{}, ErrAgentCheckUnavailable
	}
	run, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs
		WHERE machine_id=$1 AND spec->>'agent_check'='true' AND spec->>'executor'=$2
		AND status IN ('queued','claimed','running','canceling')
		ORDER BY created DESC,id DESC LIMIT 1`, machineID, executor), false)
	if !errors.Is(err, pgx.ErrNoRows) {
		return run, err
	}
	id, err := newID()
	if err != nil {
		return protocol.Run{}, err
	}
	run, err = insertRun(ctx, tx, id, protocol.Spec{
		MachineID: machineID, Executor: executor, Title: agent.Name + " 可用性测试", AgentCheck: true,
	}, "", "", 1)
	if err != nil {
		return protocol.Run{}, err
	}
	return run, tx.Commit(ctx)
}
