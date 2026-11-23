package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// The public response is an explicit projection. Operator records contain private fields.
type PublicOverview struct {
	Machines       int               `json:"machines"`
	OnlineMachines int               `json:"online_machines"`
	Projects       int               `json:"projects"`
	Recent         []PublicExecution `json:"recent"`
}

type PublicExecution struct {
	Project string    `json:"project"`
	Created time.Time `json:"created"`
	Status  string    `json:"status"`
}

func (s *Store) PublicOverview(ctx context.Context) (PublicOverview, error) {
	var overview PublicOverview
	err := s.db.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE seen > now() - interval '45 seconds'),
		(SELECT count(*) FROM (
			SELECT id AS machine_id, unnest(projects) AS project FROM machines
			UNION SELECT machine_id, id FROM game_projects
		) inventory)
		FROM machines`).Scan(&overview.Machines, &overview.OnlineMachines, &overview.Projects)
	if err != nil {
		return overview, err
	}
	rows, err := s.db.Query(ctx, `SELECT coalesce(p.name, e.project), e.created, e.status
		FROM executions e LEFT JOIN game_projects p ON p.machine_id=e.machine_id AND p.id=e.project
		WHERE NOT e.archived ORDER BY e.created DESC, e.id DESC LIMIT 10`)
	if err != nil {
		return overview, err
	}
	overview.Recent, err = pgx.CollectRows(rows, pgx.RowToStructByPos[PublicExecution])
	return overview, err
}
