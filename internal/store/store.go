package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

const leaseInterval = "interval '45 seconds'"
const runColumns = "id, spec, status, lease, created, updated, result, execution_id, step_id"

var ErrConflict = errors.New("run state or lease changed")

type Store struct{ db *pgxpool.Pool }

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	tx, err := db.Begin(ctx)
	if err == nil {
		err = prepare(ctx, tx)
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("prepare database: %w", err)
	}
	return &Store{db: db}, nil
}

func prepare(ctx context.Context, tx pgx.Tx) error {
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(483739118)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, schema); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Close() { s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.Ping(ctx) }

func newID() (string, error) {
	var value [16]byte
	_, err := rand.Read(value[:])
	return hex.EncodeToString(value[:]), err
}

func (s *Store) Enroll(ctx context.Context, name string) (protocol.Enrollment, error) {
	var enrollment protocol.Enrollment
	id, err := newID()
	if err != nil {
		return enrollment, err
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return enrollment, err
	}
	enrollment.Token = hex.EncodeToString(key[:])
	digest := sha256.Sum256([]byte(enrollment.Token))
	row := s.db.QueryRow(ctx, `INSERT INTO machines (id, name, credential_hash) VALUES ($1,$2,$3)
		RETURNING id,name,executors,projects,seen`, id, name, digest[:])
	enrollment.Machine, err = scanMachine(row)
	return enrollment, err
}

func (s *Store) AuthRunner(ctx context.Context, token string) (string, error) {
	digest := sha256.Sum256([]byte(token))
	var id string
	err := s.db.QueryRow(ctx, `SELECT id FROM machines WHERE credential_hash=$1`, digest[:]).Scan(&id)
	return id, err
}

func scanMachine(row pgx.Row) (protocol.Machine, error) {
	var m protocol.Machine
	err := row.Scan(&m.ID, &m.Name, &m.Executors, &m.Projects, &m.Seen)
	return m, err
}

func (s *Store) Machines(ctx context.Context) ([]protocol.Machine, error) {
	rows, err := s.db.Query(ctx, `SELECT id,name,executors,projects,seen FROM machines ORDER BY name,id LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []protocol.Machine{}
	for rows.Next() {
		m, err := scanMachine(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

func (s *Store) Heartbeat(ctx context.Context, id string, h protocol.Heartbeat) error {
	_, err := s.db.Exec(ctx, `UPDATE machines SET executors=$2,projects=$3,seen=now() WHERE id=$1`, id, h.Executors, h.Projects)
	return err
}

func (s *Store) expire(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `UPDATE runs SET status='lost',updated=now(),result='{"status":"lost","detail":"Runner lease expired"}'
		WHERE status IN ('claimed','running','canceling') AND lease_expires <= now()`)
	return err
}

func (s *Store) Sweep(ctx context.Context) error { return s.expire(ctx) }

func scanRun(row pgx.Row, showLease bool) (protocol.Run, error) {
	var run protocol.Run
	var spec, result []byte
	err := row.Scan(&run.ID, &spec, &run.Status, &run.Lease, &run.Created, &run.Updated, &result, &run.ExecutionID, &run.StepID)
	if err == nil {
		err = json.Unmarshal(spec, &run.Spec)
	}
	if err == nil && result != nil {
		err = json.Unmarshal(result, &run.Result)
	}
	if !showLease {
		run.Lease = ""
	}
	return run, err
}

func insertRun(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string, spec protocol.Spec, executionID, stepID string) (protocol.Run, error) {
	row := db.QueryRow(ctx, `INSERT INTO runs (id,machine_id,spec,status,execution_id,step_id)
		VALUES ($1,$2,$3,'queued',$4,$5) ON CONFLICT(id) DO UPDATE SET id=excluded.id
		WHERE runs.spec=excluded.spec AND runs.execution_id=excluded.execution_id AND runs.step_id=excluded.step_id
		RETURNING `+runColumns, id, spec.MachineID, spec, executionID, stepID)
	run, err := scanRun(row, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return run, ErrConflict
	}
	return run, err
}

func (s *Store) CreateRun(ctx context.Context, id string, spec protocol.Spec) (protocol.Run, error) {
	return insertRun(ctx, s.db, id, spec, "", "")
}

func (s *Store) Runs(ctx context.Context, executionID string) ([]protocol.Run, error) {
	if err := s.expire(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT `+runColumns+` FROM runs WHERE $1='' OR execution_id=$1 ORDER BY created DESC,id LIMIT 200`, executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []protocol.Run{}
	for rows.Next() {
		run, err := scanRun(rows, false)
		if err != nil {
			return nil, err
		}
		list = append(list, run)
	}
	return list, rows.Err()
}

func (s *Store) Run(ctx context.Context, id string) (protocol.Run, error) {
	if err := s.expire(ctx); err != nil {
		return protocol.Run{}, err
	}
	return scanRun(s.db.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1`, id), false)
}

func (s *Store) Events(ctx context.Context, id string, after int) ([]protocol.Event, error) {
	rows, err := s.db.Query(ctx, `SELECT seq,kind,body FROM run_events WHERE run_id=$1 AND seq>$2 ORDER BY seq LIMIT 200`, id, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []protocol.Event{}
	for rows.Next() {
		var event protocol.Event
		if err := rows.Scan(&event.Seq, &event.Kind, &event.Text); err != nil {
			return nil, err
		}
		list = append(list, event)
	}
	return list, rows.Err()
}

func (s *Store) Claim(ctx context.Context, machine string) (*protocol.Run, error) {
	if err := s.expire(ctx); err != nil {
		return nil, err
	}
	lease, err := newID()
	if err != nil {
		return nil, err
	}
	row := s.db.QueryRow(ctx, `UPDATE runs SET status='claimed',lease=$2,lease_expires=now()+`+leaseInterval+`,updated=now()
		WHERE id=(SELECT r.id FROM runs r JOIN machines m ON m.id=r.machine_id
		WHERE r.machine_id=$1 AND r.status='queued' AND m.seen > now()-`+leaseInterval+`
		AND r.spec->>'executor'=ANY(m.executors) AND r.spec->>'project'=ANY(m.projects)
		ORDER BY r.created,r.id LIMIT 1 FOR UPDATE OF r SKIP LOCKED) RETURNING `+runColumns, machine, lease)
	run, err := scanRun(row, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &run, err
}

func (s *Store) Start(ctx context.Context, machine, id, lease string) error {
	tag, err := s.db.Exec(ctx, `UPDATE runs SET status='running',lease_expires=now()+`+leaseInterval+`,updated=now()
		WHERE machine_id=$1 AND id=$2 AND lease=$3 AND lease_expires>now() AND status IN ('claimed','running')`, machine, id, lease)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return err
}

func (s *Store) Cancel(ctx context.Context, id string) (protocol.Run, error) {
	if err := s.expire(ctx); err != nil {
		return protocol.Run{}, err
	}
	row := s.db.QueryRow(ctx, `UPDATE runs SET status=CASE WHEN status IN ('queued','claimed') THEN 'canceled'
		WHEN status='running' THEN 'canceling' ELSE status END,updated=now() WHERE id=$1 RETURNING `+runColumns, id)
	return scanRun(row, false)
}

func (s *Store) Report(ctx context.Context, machine, id string, report protocol.Report) (protocol.RunReply, error) {
	var reply protocol.RunReply
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return reply, err
	}
	defer tx.Rollback(ctx)
	var currentResult []byte
	err = tx.QueryRow(ctx, `SELECT status,result FROM runs WHERE machine_id=$1 AND id=$2 AND lease=$3
		AND (lease_expires>now() OR status IN ('succeeded','failed','canceled')) FOR UPDATE`, machine, id, report.Lease).Scan(&reply.Status, &currentResult)
	if errors.Is(err, pgx.ErrNoRows) {
		return reply, ErrConflict
	}
	if err != nil {
		return reply, err
	}
	if reply.Status != "running" && reply.Status != "canceling" {
		if report.Result != nil && currentResult != nil && (reply.Status == report.Result.Status || reply.Status == "canceled") {
			return reply, nil
		}
		return reply, ErrConflict
	}
	for _, event := range report.Events {
		_, err := tx.Exec(ctx, `INSERT INTO run_events (run_id,seq,kind,body) VALUES ($1,$2,$3,$4)
			ON CONFLICT(run_id,seq) DO NOTHING`, id, event.Seq, event.Kind, event.Text)
		if err != nil {
			return reply, err
		}
	}
	if report.Result != nil {
		result := *report.Result
		if reply.Status == "canceling" {
			result = protocol.Result{Status: "canceled", Detail: "Runner confirmed the process stopped"}
		}
		reply.Status = result.Status
		_, err = tx.Exec(ctx, `UPDATE runs SET status=$2,result=$3,updated=now() WHERE id=$1`, id, result.Status, result)
	} else {
		_, err = tx.Exec(ctx, `UPDATE runs SET lease_expires=now()+`+leaseInterval+`,updated=now() WHERE id=$1`, id)
	}
	if err != nil {
		return reply, err
	}
	return reply, tx.Commit(ctx)
}
