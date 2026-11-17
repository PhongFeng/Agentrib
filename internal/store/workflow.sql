CREATE TABLE IF NOT EXISTS workflows (
    id text NOT NULL,
    version int NOT NULL CHECK (version>0),
    name text NOT NULL,
    steps jsonb NOT NULL,
    PRIMARY KEY (id,version)
);

ALTER TABLE workflows ADD COLUMN IF NOT EXISTS engine text NOT NULL DEFAULT 'godot';

CREATE TABLE IF NOT EXISTS executions (
    id text PRIMARY KEY,
    workflow_id text NOT NULL,
    version int NOT NULL,
    machine_id text NOT NULL REFERENCES machines(id),
    project text NOT NULL,
    brief text NOT NULL,
    commit_sha text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'running',
    created timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workflow_id,version) REFERENCES workflows(id,version)
);

CREATE TABLE IF NOT EXISTS step_approvals (
    execution_id text NOT NULL REFERENCES executions(id),
    step_id text NOT NULL,
    approved timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (execution_id,step_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS workflow_run_step ON runs(execution_id,step_id) WHERE execution_id<>'';
