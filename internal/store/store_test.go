package store

import (
	"context"
	"errors"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/jackc/pgx/v5"
)

func integrationStore(t *testing.T) (context.Context, *Store, string) {
	t.Helper()
	databaseURL := os.Getenv("AGENTRIB_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AGENTRIB_TEST_DATABASE_URL is unset")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	id, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	name := "test_" + id
	if _, err := conn.Exec(ctx, `CREATE SCHEMA `+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(cleanup, `DROP SCHEMA `+name+` CASCADE`)
		_ = conn.Close(cleanup)
	})
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", name)
	parsed.RawQuery = query.Encode()
	databaseURL = parsed.String()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return ctx, db, databaseURL
}

func TestLeaseCancellationAndReplay(t *testing.T) {
	ctx, db, url := integrationStore(t)
	enrollment, err := db.Enroll(ctx, "test machine")
	if err != nil {
		t.Fatal(err)
	}
	machine, err := db.AuthRunner(ctx, enrollment.Token)
	if err != nil || machine != enrollment.Machine.ID {
		t.Fatalf("authenticate machine: %q %v", machine, err)
	}
	if err := db.Heartbeat(ctx, machine, protocol.Heartbeat{Executors: []string{"codex"}, Projects: []string{"moon"}}); err != nil {
		t.Fatal(err)
	}
	id, _ := newID()
	spec := protocol.Spec{MachineID: machine, Project: "moon", Executor: "codex", Title: "Level 1", Prompt: "Implement level 1"}
	for i := 0; i < 2; i++ {
		if _, err := db.CreateRun(ctx, id, spec); err != nil {
			t.Fatal(err)
		}
	}
	mismatch := spec
	mismatch.Title = "Different task"
	if _, err := db.CreateRun(ctx, id, mismatch); !errors.Is(err, ErrConflict) {
		t.Fatalf("reuse id with different input: %v", err)
	}
	const claimers = 5
	type attempt struct {
		run *protocol.Run
		err error
	}
	claims := make(chan attempt, claimers)
	var group sync.WaitGroup
	for i := 0; i < claimers; i++ {
		group.Add(1)
		go func() { defer group.Done(); run, err := db.Claim(ctx, machine); claims <- attempt{run, err} }()
	}
	group.Wait()
	close(claims)
	var claimed *protocol.Run
	for claim := range claims {
		if claim.err != nil {
			t.Fatal(claim.err)
		}
		if claim.run != nil {
			if claimed != nil {
				t.Fatal("run claimed twice")
			}
			claimed = claim.run
		}
	}
	if claimed == nil || claimed.ID != id || claimed.Status != "claimed" {
		t.Fatalf("bad claim: %+v", claimed)
	}
	if err := db.Start(ctx, machine, id, "wrong"); !errors.Is(err, ErrConflict) {
		t.Fatalf("incorrect lease: %v", err)
	}
	if err := db.Start(ctx, machine, id, claimed.Lease); err != nil {
		t.Fatal(err)
	}
	event := protocol.Event{Seq: 1, Kind: "status", Text: "Started"}
	for i := 0; i < 2; i++ {
		if _, err := db.Report(ctx, machine, id, protocol.Report{Lease: claimed.Lease, Events: []protocol.Event{event}}); err != nil {
			t.Fatal(err)
		}
	}
	if run, err := db.Cancel(ctx, id); err != nil || run.Status != "canceling" {
		t.Fatalf("request cancel: %+v %v", run, err)
	}
	finish := protocol.Report{Lease: claimed.Lease, Result: &protocol.Result{Status: "canceled", Detail: "stopped"}}
	for i := 0; i < 2; i++ {
		if result, err := db.Report(ctx, machine, id, finish); err != nil || result.Status != "canceled" {
			t.Fatalf("acknowledge cancel: %+v %v", result, err)
		}
	}
	other, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if run, err := other.Run(ctx, id); err != nil || run.Status != "canceled" || run.Lease != "" {
		t.Fatalf("restore run: %+v %v", run, err)
	}
	if events, err := other.Events(ctx, id, 0); err != nil || len(events) != 1 || events[0] != event {
		t.Fatalf("restore events: %+v %v", events, err)
	}
	newRun, _ := newID()
	if _, err := db.CreateRun(ctx, newRun, spec); err != nil {
		t.Fatal(err)
	}
	stale, err := db.Claim(ctx, machine)
	if err != nil || stale == nil {
		t.Fatalf("claim retry: %+v %v", stale, err)
	}
	if _, err := db.db.Exec(ctx, `UPDATE runs SET lease_expires=now()-interval '1 second' WHERE id=$1`, newRun); err != nil {
		t.Fatal(err)
	}
	if err := db.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Start(ctx, machine, newRun, stale.Lease); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired lease starts: %v", err)
	}
	if run, err := db.Run(ctx, newRun); err != nil || run.Status != "lost" {
		t.Fatalf("expired run: %+v %v", run, err)
	}
}
