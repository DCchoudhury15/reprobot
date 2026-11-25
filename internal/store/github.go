package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/DCchoudhury15/reprobot/internal/result"
)

// MaxGitHubCommentAttempts is how often posting a verdict comment is tried.
const MaxGitHubCommentAttempts = 8

// GitHubComment is a finished report whose verdict still has to be posted
// to its advisory.
type GitHubComment struct {
	ReportID uuid.UUID
	Ref      GitHubRef
	Attempts int // including this one
	Result   *result.Result
	Fix      *result.FixCheck // nil without a fix check
}

// ClaimGitHubComment takes the oldest finished GitHub report whose comment
// is due, and holds it for hold: until then no other caller gets it. It
// returns nil, nil when none is due. A job that failed for good has no
// result and gets no comment.
func (s *Store) ClaimGitHubComment(ctx context.Context, hold time.Duration) (*GitHubComment, error) {
	c := &GitHubComment{}
	var res, fix []byte
	err := s.pool.QueryRow(ctx, `
		UPDATE reports r SET
			github_comment_attempts = r.github_comment_attempts + 1,
			github_comment_after = now() + make_interval(secs => $1)
		FROM results x
		-- The conditions are checked again here: if another caller took the
		-- row in between, Postgres re-checks them on the updated row and
		-- this caller gets nothing instead of the same comment.
		WHERE x.report_id = r.id
		  AND r.github_commented_at IS NULL AND r.github_comment_attempts < $2
		  AND (r.github_comment_after IS NULL OR r.github_comment_after <= now())
		  AND r.id = (
			SELECT r2.id FROM reports r2 JOIN results x2 ON x2.report_id = r2.id
			WHERE r2.github_ghsa IS NOT NULL AND r2.github_commented_at IS NULL
			  AND r2.github_comment_attempts < $2
			  AND (r2.github_comment_after IS NULL OR r2.github_comment_after <= now())
			ORDER BY r2.created_at
			LIMIT 1
			FOR UPDATE OF r2 SKIP LOCKED)
		RETURNING r.id, r.github_installation, r.github_repo, r.github_ghsa, r.github_comment_attempts, x.result, x.fix`,
		hold.Seconds(), MaxGitHubCommentAttempts,
	).Scan(&c.ReportID, &c.Ref.Installation, &c.Ref.Repo, &c.Ref.GHSA, &c.Attempts, &res, &fix)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Result = &result.Result{}
	if err := json.Unmarshal(res, c.Result); err != nil {
		return nil, fmt.Errorf("report %s: result: %w", c.ReportID, err)
	}
	if len(fix) > 0 {
		c.Fix = &result.FixCheck{}
		if err := json.Unmarshal(fix, c.Fix); err != nil {
			return nil, fmt.Errorf("report %s: fix check: %w", c.ReportID, err)
		}
	}
	return c, nil
}

// GitHubCommented records that the comment was posted.
func (s *Store) GitHubCommented(ctx context.Context, reportID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE reports SET github_commented_at = now(), github_comment_after = NULL, github_comment_error = NULL
		WHERE id = $1`, reportID)
	return err
}

// GitHubCommentFailed records a failed post. The comment is tried again
// after retry, until MaxGitHubCommentAttempts.
func (s *Store) GitHubCommentFailed(ctx context.Context, reportID uuid.UUID, retry time.Duration, errText string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE reports SET github_comment_after = now() + make_interval(secs => $2), github_comment_error = $3
		WHERE id = $1`, reportID, retry.Seconds(), truncate(cleanText(errText), 2000))
	return err
}
