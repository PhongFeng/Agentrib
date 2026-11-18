package store

import (
	"context"
	"errors"
	"slices"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/jackc/pgx/v5"
)

var ErrProjectExists = errors.New("该项目标识已被使用")
var ErrProjectLimit = errors.New("这台执行机器已达到 64 个项目的上限")
var ErrProjectCreation = errors.New("这台执行机器尚未启用项目创建")

func (s *Store) Projects(ctx context.Context, machine string) ([]protocol.GameProject, error) {
	rows, err := s.db.Query(ctx, `SELECT keys.id, coalesce(p.name, keys.id),
		coalesce(p.engine, m.project_engines->>keys.id, 'godot'), coalesce(m.project_workspaces->>keys.id, ''),
		CASE WHEN p.id IS NULL THEN 'ready' WHEN p.error<>'' THEN 'failed' WHEN NOT p.workspace_created THEN 'pending'
		     WHEN keys.id=ANY(m.projects) AND p.engine=coalesce(m.project_engines->>keys.id, 'godot') THEN 'ready'
		     ELSE 'unavailable' END,
		coalesce(p.error, '')
		FROM machines m CROSS JOIN LATERAL (
			SELECT unnest(m.projects) AS id UNION SELECT id FROM game_projects WHERE machine_id=m.id
		) keys LEFT JOIN game_projects p ON p.machine_id=m.id AND p.id=keys.id
		WHERE m.id=$1 ORDER BY keys.id`, machine)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (protocol.GameProject, error) {
		var project protocol.GameProject
		err := row.Scan(&project.ID, &project.Name, &project.Engine, &project.Workspace, &project.Status, &project.Error)
		return project, err
	})
}

func (s *Store) RequestProject(ctx context.Context, machineID string, input protocol.ProjectInput) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize reservations with one another and with Runner inventory updates.
	machine, err := scanMachine(tx.QueryRow(ctx, `SELECT `+machineColumns+` FROM machines WHERE id=$1 FOR UPDATE`, machineID))
	if err != nil {
		return err
	}
	if !machine.CreateProjects {
		return ErrProjectCreation
	}
	var name, engine, failure string
	err = tx.QueryRow(ctx, `SELECT name,engine,error FROM game_projects WHERE machine_id=$1 AND id=$2`, machineID, input.ID).Scan(&name, &engine, &failure)
	switch {
	case err == nil:
		if name != input.Name || engine != input.Engine {
			return ErrProjectExists
		}
		if failure == "" {
			return nil
		}
	case errors.Is(err, pgx.ErrNoRows):
		if slices.Contains(machine.Projects, input.ID) {
			return ErrProjectExists
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM (
			SELECT unnest($2::text[]) AS id UNION SELECT id FROM game_projects WHERE machine_id=$1
		) projects`, machineID, machine.Projects).Scan(&count); err != nil {
			return err
		}
		if count >= protocol.MaxProjects {
			return ErrProjectLimit
		}
	default:
		return err
	}
	attempt, err := newID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO game_projects (machine_id,id,name,engine,attempt) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (machine_id,id) DO UPDATE SET attempt=excluded.attempt,error='',requested=now()`,
		machineID, input.ID, input.Name, input.Engine, attempt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func nextProjectRequest(ctx context.Context, tx pgx.Tx, machine string, h protocol.Heartbeat) (*protocol.ProjectRequest, error) {
	if result := h.ProjectResult; result != nil {
		if _, err := tx.Exec(ctx, `UPDATE game_projects SET workspace_created=($3=''), error=$3
			WHERE machine_id=$1 AND attempt=$2 AND NOT workspace_created AND error=''
			AND ($3<>'' OR (id=ANY($4) AND engine=$5::jsonb->>id AND ($6::jsonb->>id)<>''))`,
			machine, result.Attempt, result.Error, h.Projects, h.ProjectEngines, h.ProjectWorkspaces); err != nil {
			return nil, err
		}
	}
	var project protocol.ProjectRequest
	err := tx.QueryRow(ctx, `SELECT id,name,engine,attempt FROM game_projects
		WHERE machine_id=$1 AND NOT workspace_created AND error='' ORDER BY requested,id LIMIT 1`, machine).
		Scan(&project.ID, &project.Name, &project.Engine, &project.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &project, err
}

func (s *Store) ProjectCommits(ctx context.Context, machine, project string) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT result->>'commit' AS commit FROM runs
		WHERE machine_id=$1 AND spec->>'project'=$2 AND status='succeeded' AND coalesce(result->>'commit','')<>''
		GROUP BY result->>'commit' ORDER BY max(updated) DESC,commit LIMIT 20`, machine, project)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
