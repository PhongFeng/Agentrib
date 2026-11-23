package store

import (
	"testing"
)

func TestPublicOverview(t *testing.T) {
	ctx, db, _ := integrationStore(t)
	empty, err := db.PublicOverview(ctx)
	if err != nil || empty.Recent == nil || len(empty.Recent) != 0 {
		t.Fatalf("empty overview: %+v, %v", empty, err)
	}
	_, err = db.db.Exec(ctx, `
		INSERT INTO machines (id,name,credential_hash,projects,seen) VALUES
			('a','private machine','a','{first,legacy}',now()), ('b','other machine','b','{}','epoch');
		INSERT INTO game_projects (machine_id,id,name,engine,attempt) VALUES ('a','first','First game','godot','attempt');
		INSERT INTO workflows (id,version,name,steps) VALUES ('w',1,'private workflow','[]');
		INSERT INTO executions (id,workflow_id,version,machine_id,project,brief,created,archived)
			SELECT i::text,'w',1,'a',CASE WHEN i=11 THEN 'first' ELSE 'legacy' END,
				'private brief',now()+i*interval '1 minute',i=12 FROM generate_series(1,12) i;`)
	if err != nil {
		t.Fatal(err)
	}
	overview, err := db.PublicOverview(ctx)
	if err != nil || overview.Machines != 2 || overview.OnlineMachines != 1 || overview.Projects != 2 ||
		len(overview.Recent) != 10 || overview.Recent[0].Project != "First game" || overview.Recent[1].Project != "legacy" {
		t.Fatalf("public overview: %+v, %v", overview, err)
	}
}
