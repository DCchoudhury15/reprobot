// Package store keeps reports, the job queue and results in Postgres.
//
// Jobs are handed out with leases. A worker claims a job, extends the lease
// with Heartbeat while it runs, and ends with Complete or Fail. Complete,
// Fail and Heartbeat only work while the caller still owns the lease, so a
// worker whose lease expired and was taken over cannot write a result.
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DCchoudhury15/reprobot/internal/result"
)

// Job states.
const (
	JobQueued  = "queued"
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
)

var (
	// ErrLeaseLost means the job is not running under this worker's lease
	// any more (it expired and another worker took it, or it already ended).
	ErrLeaseLost = errors.New("store: lease lost")
	// ErrNotFound means there is no report with that id.
	ErrNotFound = errors.New("store: not found")
)

// maxBackoff caps the wait before a failed job is tried again.
const maxBackoff = 5 * time.Minute

// maxErrorBytes caps the stored error text.
const maxErrorBytes = 4000

// Store is a Postgres-backed store. It is safe for concurrent use.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects to Postgres and checks the connection. It does not run
// migrations; call Migrate for that.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

// Close closes all connections.
func (s *Store) Close() { s.pool.Close() }

// Ping checks that the database answers.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Pool returns the connection pool, for tests and admin tools.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// ReportInput is a new report. The files already sit in Dir.
type ReportInput struct {
	ID             uuid.UUID // uuid.Nil lets the database pick one
	Project        string
	Reporter       string // optional
	IdempotencyKey string // optional
	ReproHash      string // optional
	Dir            string // directory with repro.yaml and the reporter files
}

// CreateReport stores a report and queues one job for it, in one
// transaction. If IdempotencyKey is set and a report with that key already
// exists, it returns the existing ids with created=false and writes nothing.
func (s *Store) CreateReport(ctx context.Context, in ReportInput) (reportID uuid.UUID, jobID int64, created bool, err error) {
	var idArg any
	if in.ID != uuid.Nil {
		idArg = in.ID
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, 0, false, err
	}
	defer tx.Rollback(ctx)

	// ON CONFLICT DO NOTHING also covers two requests with the same key at
	// the same time: the second insert waits for the first transaction and
	// then inserts nothing.
	err = tx.QueryRow(ctx, `
		INSERT INTO reports (id, project, reporter, idempotency_key, repro_hash, dir)
		VALUES (coalesce($1::uuid, gen_random_uuid()), $2, nullif($3, ''), nullif($4, ''), nullif($5, ''), $6)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id`,
		idArg, in.Project, in.Reporter, in.IdempotencyKey, in.ReproHash, in.Dir,
	).Scan(&reportID)
	if errors.Is(err, pgx.ErrNoRows) {
		tx.Rollback(ctx)
		err = s.pool.QueryRow(ctx, `
			SELECT r.id, j.id FROM reports r JOIN jobs j ON j.report_id = r.id
			WHERE r.idempotency_key = $1
			ORDER BY j.id LIMIT 1`, in.IdempotencyKey,
		).Scan(&reportID, &jobID)
		if err != nil {
			return uuid.Nil, 0, false, fmt.Errorf("find report by idempotency key: %w", err)
		}
		return reportID, jobID, false, nil
	}
	if err != nil {
		return uuid.Nil, 0, false, err
	}
	if err := tx.QueryRow(ctx,
		"INSERT INTO jobs (report_id) VALUES ($1) RETURNING id", reportID,
	).Scan(&jobID); err != nil {
		return uuid.Nil, 0, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, 0, false, err
	}
	return reportID, jobID, true, nil
}

// Job is a claimed job.
type Job struct {
	ID             int64
	ReportID       uuid.UUID
	Project        string
	Dir            string
	Attempts       int // including this one
	MaxAttempts    int
	LeaseExpiresAt time.Time
}

// Claim leases the oldest runnable job to workerID: a queued job whose
// run_after has passed, or a running job whose lease expired. A job with an
// expired lease that already used all its attempts is marked failed instead.
// Claim returns nil, nil when there is no work.
func (s *Store) Claim(ctx context.Context, workerID string, lease time.Duration) (*Job, error) {
	if workerID == "" {
		return nil, errors.New("store: empty worker id")
	}
	if lease <= 0 {
		return nil, errors.New("store: lease must be positive")
	}
	for {
		job, again, err := s.claimOnce(ctx, workerID, lease)
		if err != nil || !again {
			return job, err
		}
	}
}

// claimOnce looks at one candidate job. again is true when it marked a job
// failed and the caller should look for another one.
func (s *Store) claimOnce(ctx context.Context, workerID string, lease time.Duration) (job *Job, again bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	var (
		id                    int64
		state                 string
		attempts, maxAttempts int
	)
	err = tx.QueryRow(ctx, `
		SELECT id, state, attempts, max_attempts FROM jobs
		WHERE (state = 'queued' AND run_after <= now())
		   OR (state = 'running' AND lease_expires_at < now())
		ORDER BY id
		LIMIT 1
		FOR UPDATE SKIP LOCKED`).Scan(&id, &state, &attempts, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	if state == JobRunning && attempts >= maxAttempts {
		if _, err := tx.Exec(ctx, `
			UPDATE jobs SET state = 'failed',
				last_error = 'lease of ' || coalesce(lease_owner, '') || ' expired on the last attempt',
				lease_expires_at = NULL, updated_at = now()
			WHERE id = $1`, id); err != nil {
			return nil, false, err
		}
		return nil, true, tx.Commit(ctx)
	}

	j := &Job{}
	err = tx.QueryRow(ctx, `
		UPDATE jobs j SET
			last_error = CASE WHEN j.state = 'running'
				THEN 'lease of ' || coalesce(j.lease_owner, '') || ' expired'
				ELSE j.last_error END,
			state = 'running',
			attempts = j.attempts + 1,
			lease_owner = $2,
			lease_expires_at = now() + make_interval(secs => $3),
			updated_at = now()
		FROM reports r
		WHERE j.id = $1 AND r.id = j.report_id
		RETURNING j.id, j.report_id, r.project, r.dir, j.attempts, j.max_attempts, j.lease_expires_at`,
		id, workerID, lease.Seconds(),
	).Scan(&j.ID, &j.ReportID, &j.Project, &j.Dir, &j.Attempts, &j.MaxAttempts, &j.LeaseExpiresAt)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return j, false, nil
}

// Heartbeat extends the lease of a running job by lease from now. It returns
// ErrLeaseLost if workerID does not hold the lease any more.
func (s *Store) Heartbeat(ctx context.Context, jobID int64, workerID string, lease time.Duration) error {
	if lease <= 0 {
		return errors.New("store: lease must be positive")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET lease_expires_at = now() + make_interval(secs => $3), updated_at = now()
		WHERE id = $1 AND lease_owner = $2 AND state = 'running'`,
		jobID, workerID, lease.Seconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Complete stores the result and marks the job done, in one transaction.
// It returns ErrLeaseLost and writes nothing if workerID does not hold the
// lease.
func (s *Store) Complete(ctx context.Context, jobID int64, workerID string, res *result.Result) error {
	if res == nil {
		return errors.New("store: nil result")
	}
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	var signature string
	if res.Crash != nil {
		signature = res.Crash.Signature
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var reportID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE jobs SET state = 'done', lease_expires_at = NULL, updated_at = now()
		WHERE id = $1 AND lease_owner = $2 AND state = 'running'
		RETURNING report_id`, jobID, workerID).Scan(&reportID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO results (report_id, state, signature, result)
		VALUES ($1, $2, nullif($3, ''), $4)`,
		reportID, string(res.State), cleanText(signature), json.RawMessage(jsonbSafe(data)),
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Fail records a failed attempt. If the job has attempts left it goes back
// to the queue after a backoff of 1s * 2^attempts (at most 5 minutes);
// otherwise it is marked failed. It returns ErrLeaseLost if workerID does
// not hold the lease.
func (s *Store) Fail(ctx context.Context, jobID int64, workerID string, errText string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET
			state = CASE WHEN attempts < max_attempts THEN 'queued' ELSE 'failed' END,
			run_after = CASE WHEN attempts < max_attempts
				THEN now() + make_interval(secs => least(power(2::float8, attempts), $4))
				ELSE run_after END,
			last_error = $3,
			lease_owner = NULL,
			lease_expires_at = NULL,
			updated_at = now()
		WHERE id = $1 AND lease_owner = $2 AND state = 'running'`,
		jobID, workerID, truncate(cleanText(errText), maxErrorBytes), maxBackoff.Seconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Report is a report with the state of its job and its result, if any.
type Report struct {
	ID          uuid.UUID
	Project     string
	Reporter    string
	ReproHash   string
	Dir         string
	CreatedAt   time.Time
	JobID       int64
	JobState    string
	Attempts    int
	MaxAttempts int
	LastError   string
	Signature   string
	Result      *result.Result // nil until the job is done
}

// GetReport returns one report. It returns ErrNotFound if there is none.
func (s *Store) GetReport(ctx context.Context, id uuid.UUID) (*Report, error) {
	r := &Report{}
	var raw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT r.id, r.project, coalesce(r.reporter, ''), coalesce(r.repro_hash, ''), r.dir, r.created_at,
			coalesce(j.id, 0), coalesce(j.state, ''), coalesce(j.attempts, 0), coalesce(j.max_attempts, 0),
			coalesce(j.last_error, ''), coalesce(res.signature, ''), res.result
		FROM reports r
		LEFT JOIN LATERAL (
			SELECT * FROM jobs WHERE jobs.report_id = r.id ORDER BY jobs.id DESC LIMIT 1
		) j ON true
		LEFT JOIN results res ON res.report_id = r.id
		WHERE r.id = $1`, id,
	).Scan(&r.ID, &r.Project, &r.Reporter, &r.ReproHash, &r.Dir, &r.CreatedAt,
		&r.JobID, &r.JobState, &r.Attempts, &r.MaxAttempts,
		&r.LastError, &r.Signature, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if raw != nil {
		r.Result = &result.Result{}
		if err := json.Unmarshal(raw, r.Result); err != nil {
			return nil, fmt.Errorf("decode result: %w", err)
		}
	}
	return r, nil
}

// maxDuplicates caps how many duplicates Duplicates returns.
const maxDuplicates = 100

// Duplicates returns other reports of the same project whose result has the
// same crash signature, oldest first. An empty signature never matches.
func (s *Store) Duplicates(ctx context.Context, project, signature string, excludeReportID uuid.UUID) ([]uuid.UUID, error) {
	if signature == "" {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT res.report_id FROM results res
		JOIN reports r ON r.id = res.report_id
		WHERE r.project = $1 AND res.signature = $2 AND res.report_id <> $3
		ORDER BY r.created_at, r.id
		LIMIT $4`, project, signature, excludeReportID, maxDuplicates)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// cleanText makes s storable in a text column: valid UTF-8 without NUL.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "�")
	return strings.ReplaceAll(s, "\x00", "�")
}

// truncate cuts s to at most n bytes without splitting a UTF-8 sequence.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// jsonbSafe replaces the escape \u0000 in encoded JSON with �, because
// jsonb rejects NUL characters. Crash output can contain NUL bytes.
func jsonbSafe(b []byte) []byte {
	if !bytes.Contains(b, []byte(`\u0000`)) {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' || i+1 >= len(b) {
			out = append(out, b[i])
			continue
		}
		// Every escape starts with a backslash; skip it as a unit so an
		// escaped backslash followed by "u0000" is left alone.
		if bytes.HasPrefix(b[i:], []byte(`\u0000`)) {
			out = append(out, `�`...)
			i += len(`\u0000`) - 1
			continue
		}
		out = append(out, b[i], b[i+1])
		i++
	}
	return out
}
