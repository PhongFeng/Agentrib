CREATE TABLE IF NOT EXISTS machines (
    id text PRIMARY KEY,
    name text NOT NULL,
    credential_hash bytea UNIQUE NOT NULL,
    executors text[] NOT NULL DEFAULT '{}',
    projects text[] NOT NULL DEFAULT '{}',
    seen timestamptz NOT NULL DEFAULT 'epoch'
);

ALTER TABLE machines ADD COLUMN IF NOT EXISTS project_engines jsonb NOT NULL DEFAULT '{}';

CREATE TABLE IF NOT EXISTS runs (
    id text PRIMARY KEY,
    machine_id text NOT NULL REFERENCES machines(id),
    spec jsonb NOT NULL,
    status text NOT NULL CHECK (status IN ('queued', 'claimed', 'running', 'canceling', 'succeeded', 'failed', 'canceled', 'lost')),
    lease text NOT NULL DEFAULT '',
    lease_expires timestamptz,
    result jsonb,
    execution_id text NOT NULL DEFAULT '',
    step_id text NOT NULL DEFAULT '',
    created timestamptz NOT NULL DEFAULT now(),
    updated timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS runs_claim ON runs (machine_id, created, id) WHERE status = 'queued';

CREATE TABLE IF NOT EXISTS run_events (
    run_id text NOT NULL REFERENCES runs(id),
    seq integer NOT NULL CHECK (seq > 0),
    kind text NOT NULL,
    body text NOT NULL,
    PRIMARY KEY (run_id, seq)
);
