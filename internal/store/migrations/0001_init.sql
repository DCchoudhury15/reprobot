CREATE TABLE reports (
	id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	project text NOT NULL,
	reporter text,
	idempotency_key text UNIQUE,
	repro_hash text,
	dir text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE jobs (
	id bigserial PRIMARY KEY,
	report_id uuid NOT NULL REFERENCES reports (id),
	state text NOT NULL DEFAULT 'queued'
		CHECK (state IN ('queued', 'running', 'done', 'failed')),
	attempts int NOT NULL DEFAULT 0,
	max_attempts int NOT NULL DEFAULT 3,
	lease_owner text,
	lease_expires_at timestamptz,
	run_after timestamptz NOT NULL DEFAULT now(),
	last_error text,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX jobs_state_run_after_idx ON jobs (state, run_after);
CREATE INDEX jobs_report_id_idx ON jobs (report_id);

CREATE TABLE results (
	report_id uuid PRIMARY KEY REFERENCES reports (id),
	state text NOT NULL,
	signature text,
	result jsonb NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX results_signature_idx ON results (signature);
