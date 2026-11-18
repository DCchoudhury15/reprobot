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

// RateLimitError means the owner already sent MaxPerHour reports in the
// last hour.
type RateLimitError struct {
	RetryAfter time.Duration // until the oldest of them leaves the window
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("store: too many reports, retry in %s", e.RetryAfter.Round(time.Second))
}

// rateLockClass is the first key of the per-owner advisory lock that makes
// the rate limit exact. Two-key advisory locks do not share keys with the
// one-key lock Migrate takes.
const rateLockClass int32 = 0x7262 // "rb"

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
	Owner          string // token that submitted it; optional
	IdempotencyKey string // optional, unique per Owner
	ReproHash      string // optional
	Dir            string // directory with repro.yaml and the reporter files
	// MaxPerHour, when above 0 and Owner is set, caps the reports Owner
	// may create in any hour. A repeat of an existing IdempotencyKey does
	// not count.
	MaxPerHour int
}

// CreateReport stores a report and queues one job for it, in one
// transaction. If IdempotencyKey is set and a report with that key already
// exists for the same Owner, it returns the existing ids with created=false
// and writes nothing. Past MaxPerHour it returns a *RateLimitError.
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
	limited := in.Owner != "" && in.MaxPerHour > 0
	if limited {
		// Held until commit, so two requests of one owner count one after
		// the other and cannot both slip under the limit.
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1, hashtext($2))", rateLockClass, in.Owner); err != nil {
			return uuid.Nil, 0, false, err
		}
	}

	// ON CONFLICT DO NOTHING also covers two requests with the same key at
	// the same time: the second insert waits for the first transaction and
	// then inserts nothing.
	err = tx.QueryRow(ctx, `
		INSERT INTO reports (id, project, reporter, idempotency_key, repro_hash, dir, owner)
		VALUES (coalesce($1::uuid, gen_random_uuid()), $2, nullif($3, ''), nullif($4, ''), nullif($5, ''), $6, nullif($7, ''))
		ON CONFLICT ((coalesce(owner, '')), idempotency_key) DO NOTHING
		RETURNING id`,
		idArg, in.Project, in.Reporter, in.IdempotencyKey, in.ReproHash, in.Dir, in.Owner,
	).Scan(&reportID)
	if errors.Is(err, pgx.ErrNoRows) {
		tx.Rollback(ctx)
		err = s.pool.QueryRow(ctx, `
			SELECT r.id, j.id FROM reports r JOIN jobs j ON j.report_id = r.id
			WHERE r.idempotency_key = $1 AND coalesce(r.owner, '') = $2
			ORDER BY j.id LIMIT 1`, in.IdempotencyKey, in.Owner,
		).Scan(&reportID, &jobID)
		if err != nil {
			return uuid.Nil, 0, false, fmt.Errorf("find report by idempotency key: %w", err)
		}
		return reportID, jobID, false, nil
	}
	if err != nil {
		return uuid.Nil, 0, false, err
	}
	if limited {
		// The count includes the new report, which rollback removes.
		if err := recentLimit(ctx, tx, in.Owner, in.MaxPerHour+1); err != nil {
			return uuid.Nil, 0, false, err
		}
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
	return s.CompleteSigned(ctx, jobID, workerID, res, nil)
}

// CompleteSigned is Complete that also stores envelope, the signed result,
// byte for byte. A nil envelope stores none.
func (s *Store) CompleteSigned(ctx context.Context, jobID int64, workerID string, res *result.Result, envelope []byte) error {
	if res == nil {
		return errors.New("store: nil result")
	}
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	var signature, site string
	if res.Crash != nil {
		signature, site = res.Crash.Signature, res.Crash.Site
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
		INSERT INTO results (report_id, state, signature, site, result, envelope)
		VALUES ($1, $2, nullif($3, ''), nullif($4, ''), $5, $6)`,
		reportID, string(res.State), cleanText(signature), cleanText(site), json.RawMessage(jsonbSafe(data)), envelopeArg(envelope),
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

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// recentLimit returns a *RateLimitError when owner has at least limit
// reports from the last hour.
func recentLimit(ctx context.Context, q querier, owner string, limit int) error {
	var n int
	var retry float64
	err := q.QueryRow(ctx, `
		SELECT count(*), coalesce(extract(epoch FROM min(created_at) + interval '1 hour' - now()), 0)
		FROM reports WHERE owner = $1 AND created_at > now() - interval '1 hour'`, owner,
	).Scan(&n, &retry)
	if err != nil {
		return err
	}
	if n < limit {
		return nil
	}
	return &RateLimitError{RetryAfter: max(time.Duration(retry*float64(time.Second)), time.Second)}
}

// CheckRate returns a *RateLimitError when owner already has maxPerHour
// reports from the last hour. It is a cheap early check: CreateReport
// enforces the limit exactly.
func (s *Store) CheckRate(ctx context.Context, owner string, maxPerHour int) error {
	if owner == "" || maxPerHour <= 0 {
		return nil
	}
	return recentLimit(ctx, s.pool, owner, maxPerHour)
}

// Report is a report with the state of its job and its result, if any.
type Report struct {
	ID          uuid.UUID
	Project     string
	Reporter    string
	Owner       string // empty when submitted without a token
	ReproHash   string
	Dir         string
	CreatedAt   time.Time
	JobID       int64
	JobState    string
	Attempts    int
	MaxAttempts int
	LastError   string
	Signature   string
	Site        string
	Result      *result.Result // nil until the job is done
	Envelope    []byte         // the signed result; nil when not signed
}

// GetReport returns one report. It returns ErrNotFound if there is none.
func (s *Store) GetReport(ctx context.Context, id uuid.UUID) (*Report, error) {
	r := &Report{}
	var raw []byte
	var env *string
	err := s.pool.QueryRow(ctx, `
		SELECT r.id, r.project, coalesce(r.reporter, ''), coalesce(r.owner, ''), coalesce(r.repro_hash, ''), r.dir, r.created_at,
			coalesce(j.id, 0), coalesce(j.state, ''), coalesce(j.attempts, 0), coalesce(j.max_attempts, 0),
			coalesce(j.last_error, ''), coalesce(res.signature, ''), coalesce(res.site, ''), res.result, res.envelope
		FROM reports r
		LEFT JOIN LATERAL (
			SELECT * FROM jobs WHERE jobs.report_id = r.id ORDER BY jobs.id DESC LIMIT 1
		) j ON true
		LEFT JOIN results res ON res.report_id = r.id
		WHERE r.id = $1`, id,
	).Scan(&r.ID, &r.Project, &r.Reporter, &r.Owner, &r.ReproHash, &r.Dir, &r.CreatedAt,
		&r.JobID, &r.JobState, &r.Attempts, &r.MaxAttempts,
		&r.LastError, &r.Signature, &r.Site, &raw, &env)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if env != nil {
		r.Envelope = []byte(*env)
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
// same crash signature, oldest first, in two lists. dups crash on the same
// site, or one of the two has no site. related share the signature but
// crash on a different line: the same call path, maybe a different bug.
// An empty signature never matches. With onlyOwner set, only reports of
// that owner count. Each list holds at most maxDuplicates.
func (s *Store) Duplicates(ctx context.Context, project, signature, site string, excludeReportID uuid.UUID, onlyOwner *string) (dups, related []uuid.UUID, err error) {
	if signature == "" {
		return nil, nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, other FROM (
			SELECT res.report_id AS id, r.created_at,
				(nullif($4, '') IS NOT NULL AND res.site IS NOT NULL AND res.site <> $4) AS other,
				row_number() OVER (
					PARTITION BY (nullif($4, '') IS NOT NULL AND res.site IS NOT NULL AND res.site <> $4)
					ORDER BY r.created_at, r.id) AS n
			FROM results res
			JOIN reports r ON r.id = res.report_id
			WHERE r.project = $1 AND res.signature = $2 AND res.report_id <> $3
				AND ($6::text IS NULL OR r.owner = $6)
		) m
		WHERE n <= $5
		ORDER BY n`, project, signature, excludeReportID, site, maxDuplicates, onlyOwner)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var other bool
		if err := rows.Scan(&id, &other); err != nil {
			return nil, nil, err
		}
		if other {
			related = append(related, id)
		} else {
			dups = append(dups, id)
		}
	}
	return dups, related, rows.Err()
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

func envelopeArg(env []byte) any {
	if env == nil {
		return nil
	}
	return string(env)
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
